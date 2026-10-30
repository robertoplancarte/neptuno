package orchestrate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/env"
	"github.com/robertoplancarte/neptuno/internal/procfile"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/state"
)

// Orchestrator brings a project's services up and down.
type Orchestrator struct {
	Project *project.Project
	Runner  Runner
	// Log receives progress messages and warnings.
	Log io.Writer
	// Neptuno is the path of the neptuno binary, used by host-mode
	// Procfiles.
	Neptuno string
}

// UpOptions configures Up.
type UpOptions struct {
	// Selected are the services and repos the user picked.
	Selected []string
	// Set holds --set KEY=value overrides for every repo.
	Set map[string]string
	// Detach skips attaching to the tmux session.
	Detach bool
}

// Plan is what Up will start.
type Plan struct {
	// Services is the selection plus its dependencies, dependencies first.
	Services []string
	// Compose are the services started with docker compose.
	Compose []string
	// Repos are the repos that get an overmind window.
	Repos []string
}

// SessionName is the outer tmux session for the project.
func (o *Orchestrator) SessionName() string {
	return "neptuno-" + strings.NewReplacer(".", "-", ":", "-").Replace(o.Project.Name)
}

// Plan resolves the selection's dependencies.
func (o *Orchestrator) Plan(selected []string) (Plan, error) {
	p := o.Project
	all, err := p.Closure(selected)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Services: all}
	for _, name := range all {
		if p.IsRepo(name) && p.RunMode(name) == project.RunHost {
			plan.Repos = append(plan.Repos, name)
			continue
		}
		plan.Compose = append(plan.Compose, name)
		if p.IsRepo(name) {
			plan.Repos = append(plan.Repos, name)
		}
	}
	return plan, nil
}

// Up writes env files and Procfiles, starts the containers, opens one
// overmind window per repo in the outer tmux session, and attaches to it.
func (o *Orchestrator) Up(opts UpOptions) error {
	p := o.Project
	if len(opts.Selected) == 0 {
		return errors.New("nothing selected")
	}
	plan, err := o.Plan(opts.Selected)
	if err != nil {
		return err
	}

	st, err := state.Load(p.Root)
	if err != nil {
		return err
	}

	// compose.yml can point any repo's env_file at .neptuno/env, so write
	// them all: docker compose refuses to load a project with one missing.
	if err := o.WriteEnvFiles(st, opts.Set); err != nil {
		return err
	}

	var windows []string
	for _, repo := range plan.Repos {
		o.warnIfEmpty(repo)
		ok, err := o.writeProcfile(repo)
		if err != nil {
			return err
		}
		if ok {
			windows = append(windows, repo)
		}
	}

	if err := o.checkTools(len(plan.Compose) > 0, len(windows) > 0); err != nil {
		return err
	}
	if err := o.hook("before_up", p.Config.Hooks.BeforeUp); err != nil {
		return err
	}

	before := o.containerIDs(windows)
	if len(plan.Compose) > 0 {
		fmt.Fprintf(o.Log, "Starting containers: %s\n", strings.Join(plan.Compose, ", "))
		args := append([]string{"compose", "up", "--detach"}, plan.Compose...)
		if err := o.Runner.Run(p.Root, "docker", args...); err != nil {
			return err
		}
	}
	after := o.containerIDs(windows)

	for _, repo := range windows {
		// A recreated container (new env or image) takes the old exec'd
		// processes with it, so overmind has to start over.
		recreated := before[repo] != "" && before[repo] != after[repo]
		if err := o.openWindow(repo, recreated); err != nil {
			return err
		}
	}

	if err := o.hook("after_up", p.Config.Hooks.AfterUp); err != nil {
		return err
	}

	st.LastUp = opts.Selected
	if err := state.Save(p.Root, st); err != nil {
		return err
	}

	if len(windows) == 0 || opts.Detach {
		if len(windows) > 0 {
			fmt.Fprintf(o.Log, "Attach with: tmux attach -t %s\n", o.SessionName())
		}
		return nil
	}
	return o.attach()
}

// WriteEnvFiles resolves every repo's env into .neptuno/env/<repo>.env.
func (o *Orchestrator) WriteEnvFiles(st state.State, set map[string]string) error {
	p := o.Project
	resolver := env.Resolver{Dir: p.Path("environments")}
	for _, repo := range p.Repos {
		vars, err := resolver.Resolve(repo, st.EnvFor(repo, p.Config.DefaultEnv), set)
		if err != nil {
			return err
		}
		if err := env.Write(EnvFile(p, repo), vars); err != nil {
			return err
		}
	}
	return nil
}

// EnvFile is where a repo's resolved env is written.
func EnvFile(p *project.Project, repo string) string {
	return p.Path(".neptuno", "env", repo+".env")
}

func (o *Orchestrator) generatedProcfile(repo string) string {
	return o.Project.Path(".neptuno", "procfiles", repo, "Procfile")
}

// socket is the overmind control socket, relative to the project root:
// overmind runs there, and unix socket paths are limited to about 100 bytes,
// which an absolute path under a deep home folder can exceed.
func socket(repo string) string {
	return filepath.Join(".neptuno", "run", repo+".sock")
}

// writeProcfile wraps procfiles/<repo>/Procfile. It reports false when the
// repo has no Procfile, so it runs as a container only.
func (o *Orchestrator) writeProcfile(repo string) (bool, error) {
	p := o.Project
	entries, err := procfile.ParseFile(p.Path("procfiles", repo, "Procfile"))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(o.Log, "warning: procfiles/%s/Procfile is missing, so %s runs without overmind\n", repo, repo)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		fmt.Fprintf(o.Log, "warning: procfiles/%s/Procfile has no processes\n", repo)
		return false, nil
	}
	if p.RunMode(repo) == project.RunHost {
		entries = procfile.WrapHost(entries, o.Neptuno, repo)
	} else {
		entries = procfile.WrapContainer(entries, repo)
	}
	return true, procfile.Write(o.generatedProcfile(repo), entries)
}

func (o *Orchestrator) warnIfEmpty(repo string) {
	entries, err := os.ReadDir(o.Project.Path("repos", repo))
	if err == nil && len(entries) == 0 {
		fmt.Fprintf(o.Log, "warning: repos/%s is empty; run `neptuno sync` or `git submodule update --init`\n", repo)
	}
}

func (o *Orchestrator) checkTools(docker, overmind bool) error {
	var need []string
	if docker {
		need = append(need, "docker")
	}
	if overmind {
		need = append(need, "tmux", "overmind")
	}
	var missing []string
	for _, tool := range need {
		if o.Runner.LookPath(tool) != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s not found on PATH", strings.Join(missing, ", "))
	}
	return nil
}

func (o *Orchestrator) hook(name, script string) error {
	if script == "" {
		return nil
	}
	fmt.Fprintf(o.Log, "Running %s hook: %s\n", name, script)
	if err := o.Runner.Run(o.Project.Root, "sh", "-c", script); err != nil {
		return fmt.Errorf("%s hook: %w", name, err)
	}
	return nil
}

func (o *Orchestrator) hasSession() bool {
	return o.Runner.Check(o.Project.Root, "tmux", "has-session", "-t", "="+o.SessionName())
}

func (o *Orchestrator) target(repo string) string {
	return "=" + o.SessionName() + ":" + repo
}

func (o *Orchestrator) hasWindow(repo string) bool {
	return o.hasSession() && o.Runner.Check(o.Project.Root, "tmux", "list-panes", "-t", o.target(repo))
}

// openWindow starts the repo's overmind in its own window of the outer
// session, unless that window is already open.
func (o *Orchestrator) openWindow(repo string, restart bool) error {
	p := o.Project
	if o.hasWindow(repo) {
		if !restart && o.Runner.Check(p.Root, "overmind", "status", "-s", socket(repo)) {
			fmt.Fprintf(o.Log, "%s is already running in tmux window %s:%s\n", repo, o.SessionName(), repo)
			return nil
		}
		// The window outlived its overmind or its container, so replace it.
		_ = o.Runner.Check(p.Root, "overmind", "quit", "-s", socket(repo))
		if err := o.Runner.Run(p.Root, "tmux", "kill-window", "-t", o.target(repo)); err != nil {
			return err
		}
	}
	sock := socket(repo)
	if err := os.MkdirAll(p.Path(filepath.Dir(sock)), 0o755); err != nil {
		return err
	}
	_ = os.Remove(p.Path(sock)) // stale from a crashed overmind

	q := procfile.Quote
	command := fmt.Sprintf(
		"overmind start -f %s -d %s -s %s -w %s; "+
			`echo; echo "overmind exited with status $?. Press enter to close this window."; read _`,
		q(o.generatedProcfile(repo)), q(p.Root), q(sock), q(p.Name+"-"+repo))

	fmt.Fprintf(o.Log, "Starting overmind for %s\n", repo)
	if !o.hasSession() {
		return o.Runner.Run(p.Root, "tmux", "new-session", "-d", "-s", o.SessionName(), "-n", repo, "-c", p.Root, command)
	}
	return o.Runner.Run(p.Root, "tmux", "new-window", "-d", "-t", "="+o.SessionName()+":", "-n", repo, "-c", p.Root, command)
}

// containerIDs maps container-mode repos to their running container, if any.
func (o *Orchestrator) containerIDs(repos []string) map[string]string {
	ids := map[string]string{}
	for _, repo := range repos {
		if o.Project.RunMode(repo) == project.RunContainer {
			ids[repo], _ = o.Runner.Output(o.Project.Root, "docker", "compose", "ps", "--quiet", repo)
		}
	}
	return ids
}

func (o *Orchestrator) attach() error {
	if os.Getenv("TMUX") != "" {
		if !o.Runner.Check(o.Project.Root, "tmux", "switch-client", "-t", "="+o.SessionName()) {
			fmt.Fprintf(o.Log, "Switch with: tmux switch-client -t %s\n", o.SessionName())
		}
		return nil
	}
	return o.Runner.Attach(o.Project.Root, "tmux", "attach-session", "-t", "="+o.SessionName())
}

// Down stops overmind and the containers. With no names it stops everything
// and closes the tmux session.
func (o *Orchestrator) Down(names []string) error {
	p := o.Project
	if err := o.hook("before_down", p.Config.Hooks.BeforeDown); err != nil {
		return err
	}

	repos := names
	if len(names) == 0 {
		repos = p.Repos
	}
	for _, name := range names {
		if _, ok := p.Services[name]; !ok && !p.IsRepo(name) {
			return fmt.Errorf("unknown service %q", name)
		}
	}

	for _, repo := range repos {
		if !p.IsRepo(repo) {
			continue
		}
		if _, err := os.Stat(p.Path(socket(repo))); err == nil {
			fmt.Fprintf(o.Log, "Stopping overmind for %s\n", repo)
			if !o.Runner.Check(p.Root, "overmind", "quit", "-s", socket(repo)) {
				fmt.Fprintf(o.Log, "warning: overmind for %s did not answer; closing its window\n", repo)
			}
		}
		if o.hasWindow(repo) {
			_ = o.Runner.Run(p.Root, "tmux", "kill-window", "-t", o.target(repo))
		}
	}

	if len(names) == 0 {
		if o.hasSession() {
			_ = o.Runner.Run(p.Root, "tmux", "kill-session", "-t", "="+o.SessionName())
		}
		fmt.Fprintln(o.Log, "Stopping containers")
		return o.Runner.Run(p.Root, "docker", "compose", "down")
	}

	var compose []string
	for _, name := range names {
		if _, ok := p.Services[name]; ok {
			compose = append(compose, name)
		}
	}
	if len(compose) == 0 {
		return nil
	}
	fmt.Fprintf(o.Log, "Stopping containers: %s\n", strings.Join(compose, ", "))
	return o.Runner.Run(p.Root, "docker", append([]string{"compose", "stop"}, compose...)...)
}
