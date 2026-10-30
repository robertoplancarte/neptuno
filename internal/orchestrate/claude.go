package orchestrate

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/procfile"
	"github.com/robertoplancarte/neptuno/internal/project"
	"gopkg.in/yaml.v3"
)

// ClaudeConfigDir is where Claude Code keeps its sign-in and settings in a
// repo's container. compose mounts .neptuno/claude there.
const ClaudeConfigDir = "/root/.claude"

// ClaudeOptions configures Claude.
type ClaudeOptions struct {
	Repo string
	// Args are passed to claude, such as a prompt or --continue.
	Args []string
	// Detach opens the window without attaching to it.
	Detach bool
}

// ClaudeWindow is the tmux window Claude Code runs in for a repo.
func ClaudeWindow(repo string) string {
	return "claude-" + repo
}

// Claude runs Claude Code inside the repo's running container, in its own
// window of the project's tmux session, starting the container first if it
// is not running. It runs outside overmind, so quitting Claude never stops
// the repo's processes.
func (o *Orchestrator) Claude(opts ClaudeOptions) error {
	p, repo := o.Project, opts.Repo
	if !p.IsRepo(repo) {
		return fmt.Errorf("unknown repo %q", repo)
	}
	if p.RunMode(repo) == project.RunHost {
		return fmt.Errorf("%s runs on the host (run: host), so run claude in repos/%s directly", repo, repo)
	}
	if _, ok := p.Services[repo]; !ok {
		return fmt.Errorf("compose.yml has no %s service to run Claude Code in", repo)
	}
	if err := o.checkTools(true, false); err != nil {
		return err
	}
	if o.Runner.LookPath("tmux") != nil {
		return errors.New("tmux not found on PATH")
	}
	if err := os.MkdirAll(p.Path(".neptuno", "claude"), 0o755); err != nil {
		return err
	}
	if !o.mountsClaudeConfig(repo) {
		fmt.Fprintf(o.Log, "warning: the %s service does not mount %s, so Claude Code's sign-in is lost when the container is recreated.\n"+
			"Add \"./.neptuno/claude:%s\" to its volumes in compose.yml, or set CLAUDE_CODE_OAUTH_TOKEN in environments/_shared/*.local.env.\n",
			repo, ClaudeConfigDir, ClaudeConfigDir)
	}

	running, _ := o.Runner.Output(p.Root, "docker", "compose", "ps", "--quiet", "--status", "running", repo)
	if running == "" {
		fmt.Fprintf(o.Log, "%s is not running; starting it first\n", repo)
		if err := o.Up(UpOptions{Selected: []string{repo}, Detach: true}); err != nil {
			return err
		}
	}

	window := ClaudeWindow(repo)
	if o.hasWindow(window) {
		fmt.Fprintf(o.Log, "Claude Code is already open in tmux window %s:%s\n", o.SessionName(), window)
	} else {
		q := procfile.Quote
		args := []string{"docker", "compose", "exec", q(repo), "claude"}
		for _, a := range opts.Args {
			args = append(args, q(a))
		}
		// Keep the window open on failure, such as claude missing from an
		// older image, so the error can be read.
		command := strings.Join(args, " ") +
			`; status=$?; if [ $status -ne 0 ]; then echo; echo "claude exited with status $status. Press enter to close this window."; read _; fi`

		fmt.Fprintf(o.Log, "Starting Claude Code in the %s container\n", repo)
		var err error
		if o.hasSession() {
			err = o.Runner.Run(p.Root, "tmux", "new-window", "-d", "-t", "="+o.SessionName()+":", "-n", window, "-c", p.Root, command)
		} else {
			err = o.Runner.Run(p.Root, "tmux", "new-session", "-d", "-s", o.SessionName(), "-n", window, "-c", p.Root, command)
		}
		if err != nil {
			return err
		}
	}

	if opts.Detach {
		fmt.Fprintf(o.Log, "Attach with: tmux attach -t %s:%s\n", o.SessionName(), window)
		return nil
	}
	_ = o.Runner.Run(p.Root, "tmux", "select-window", "-t", o.target(window))
	return o.attach()
}

// mountsClaudeConfig reports whether compose.yml or compose.override.yml
// mounts something at the repo's Claude config dir.
func (o *Orchestrator) mountsClaudeConfig(repo string) bool {
	for _, name := range []string{"compose.yml", "compose.override.yml"} {
		b, err := os.ReadFile(o.Project.Path(name))
		if err != nil {
			continue
		}
		var cf struct {
			Services map[string]struct {
				Volumes []any `yaml:"volumes"`
			} `yaml:"services"`
		}
		if yaml.Unmarshal(b, &cf) != nil {
			continue
		}
		for _, v := range cf.Services[repo].Volumes {
			switch v := v.(type) {
			case string:
				if strings.HasSuffix(v, ":"+ClaudeConfigDir) || strings.Contains(v, ":"+ClaudeConfigDir+":") {
					return true
				}
			case map[string]any:
				if v["target"] == ClaudeConfigDir {
					return true
				}
			}
		}
	}
	return false
}
