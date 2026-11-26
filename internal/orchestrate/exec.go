package orchestrate

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/procfile"
	"github.com/robertoplancarte/neptuno/internal/project"
	"gopkg.in/yaml.v3"
)

// ExecOptions configures ExecCommand.
type ExecOptions struct {
	// Service is a repo or any other compose service.
	Service string
	// Args is the command. A single argument is a shell command, run with
	// sh -c; several are run as they are.
	Args []string
	// Dir is the folder neptuno exec runs in. Inside repos/<repo>, the
	// command runs in the same folder of the repo's container.
	Dir string
	// TTY gives the command a terminal, when neptuno exec has one.
	TTY bool
}

// Command is a program to run with the caller's stdin, stdout and stderr.
type Command struct {
	Dir  string
	Args []string
}

// ExecCommand works out how to run a command where a service runs: in its
// container with docker compose exec, or in repos/<repo> on the host for a
// repo with run: host. A service whose container isn't running is started
// first, with everything it depends on, as neptuno up --detach does.
func (o *Orchestrator) ExecCommand(opts ExecOptions) (Command, error) {
	p, name := o.Project, opts.Service
	_, isService := p.Services[name]
	if !p.IsRepo(name) && !isService {
		return Command{}, fmt.Errorf("unknown repo or service %q; neptuno status lists them", name)
	}
	if len(opts.Args) == 0 {
		return Command{}, errors.New("name a command to run, such as: neptuno exec " + name + " -- bin/rails test")
	}

	if p.IsRepo(name) && p.RunMode(name) == project.RunHost {
		command := opts.Args[0]
		if len(opts.Args) > 1 {
			command = shellJoin(opts.Args)
		}
		// host-exec gives the command the repo's env, in repos/<repo>.
		return Command{Dir: p.Root, Args: []string{o.Neptuno, "host-exec", name, command}}, nil
	}
	if !isService {
		return Command{}, fmt.Errorf("compose.yml has no %s service to run the command in", name)
	}
	if err := o.checkTools(true, false); err != nil {
		return Command{}, err
	}

	running, _ := o.Runner.Output(p.Root, "docker", "compose", "ps", "--quiet", "--status", "running", name)
	if running == "" {
		fmt.Fprintf(o.Log, "%s is not running; starting it first\n", name)
		if err := o.Up(UpOptions{Selected: []string{name}, Detach: true}); err != nil {
			return Command{}, err
		}
	}

	args := []string{"docker", "compose", "exec"}
	if !opts.TTY {
		args = append(args, "-T")
	}
	if dir := o.containerDir(name, opts.Dir); dir != "" {
		args = append(args, "--workdir", dir)
	}
	args = append(args, name)
	if len(opts.Args) == 1 {
		args = append(args, "sh", "-c", opts.Args[0])
	} else {
		args = append(args, opts.Args...)
	}
	return Command{Dir: p.Root, Args: args}, nil
}

// containerDir is the folder in the repo's container that matches dir on
// the host, when dir is inside repos/<repo> and compose mounts that folder
// into the container, or "" to use the container's own working directory.
func (o *Orchestrator) containerDir(repo, dir string) string {
	if dir == "" || !o.Project.IsRepo(repo) {
		return ""
	}
	rel, ok := under(o.Project.Path("repos", repo), dir)
	if !ok {
		return ""
	}
	target := o.repoMount(repo)
	if target == "" {
		return ""
	}
	return path.Join(target, filepath.ToSlash(rel))
}

// repoMount is where compose.yml or compose.override.yml mounts
// repos/<repo> in the repo's container, such as /app, or "".
func (o *Orchestrator) repoMount(repo string) string {
	want := filepath.Clean(o.Project.Path("repos", repo))
	found := ""
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
			var source, target string
			switch v := v.(type) {
			case string:
				parts := strings.Split(v, ":")
				if len(parts) < 2 {
					continue
				}
				source, target = parts[0], parts[1]
			case map[string]any:
				source, _ = v["source"].(string)
				target, _ = v["target"].(string)
			}
			if source == "" || target == "" {
				continue
			}
			if !filepath.IsAbs(source) {
				source = o.Project.Path(source)
			}
			if filepath.Clean(source) == want {
				found = target
			}
		}
	}
	return found
}

// under reports whether dir is base or inside it, and its path relative
// to base.
func under(base, dir string) (string, bool) {
	resolve := func(p string) string {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return p
	}
	rel, err := filepath.Rel(resolve(base), resolve(dir))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = procfile.Quote(a)
	}
	return strings.Join(quoted, " ")
}
