// Package add implements `neptuno add`: it brings a repo into a project,
// either by cloning an existing one as a submodule or by starting a new one
// from a stack template, and wires it into compose.yml and the Caddyfile.
package add

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/scaffold"
	"github.com/robertoplancarte/neptuno/internal/stacks"
	"github.com/robertoplancarte/neptuno/internal/state"
)

// Options configures Add.
type Options struct {
	// Name is the repo name, which becomes its folder and compose service.
	Name string
	// URL, when set, is cloned into repos/<name> as a git submodule.
	URL string
	// Stack names the template; empty means stacks.Default.
	Stack string
	// Port overrides the stack's dev server port.
	Port int
	// NoGenerate skips the stack's generate command.
	NoGenerate bool
}

// Adder adds repos to a project.
type Adder struct {
	Project *project.Project
	Runner  orchestrate.Runner
	Log     io.Writer
}

// Add brings the repo in. Every step skips work that is already done, so
// running it again after a failure picks up where it stopped.
func (a *Adder) Add(opts Options) error {
	p := a.Project
	name := opts.Name
	if err := scaffold.ValidateName(name); err != nil {
		return err
	}
	if name == "_shared" {
		return errors.New(`"_shared" is reserved for environments/_shared`)
	}
	inCompose, err := composeHasService(p.Path("compose.yml"), name)
	if err != nil {
		return err
	}
	if inCompose && !p.IsRepo(name) {
		return fmt.Errorf("compose.yml already has a %q service that is not a repo; pick another name", name)
	}

	stackName := opts.Stack
	if stackName == "" {
		stackName = stacks.Default
	}
	stack, err := stacks.Get(p.Path("stacks"), stackName)
	if err != nil {
		return err
	}
	data := stacks.Data{Name: name, Project: p.Name, Port: stack.Port}
	if opts.Port > 0 {
		data.Port = opts.Port
	}

	// 1. The source code.
	repoDir := p.Path("repos", name)
	isNew := false
	switch {
	case !isEmptyDir(repoDir):
		fmt.Fprintf(a.Log, "repos/%s already has files; leaving them as they are\n", name)
	case opts.URL != "":
		if err := a.addSubmodule(opts.URL, name); err != nil {
			return err
		}
	default:
		isNew = true
		if err := a.writeAppFiles(stack, data, repoDir); err != nil {
			return err
		}
	}

	// 2. The companion files.
	for _, f := range []struct{ stackFile, target string }{
		{"Dockerfile", filepath.Join("dockerfiles", name, "Dockerfile")},
		{"Procfile", filepath.Join("procfiles", name, "Procfile")},
		{"base.env", filepath.Join("environments", name, "base.env")},
	} {
		content, err := stack.Render(f.stackFile, data)
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(a.Log, "warning: the %s stack has no %s, so %s was not written (add `extends:` to its stack.yml to inherit one)\n", stack.Name, f.stackFile, filepath.ToSlash(f.target))
			continue
		}
		if err != nil {
			return err
		}
		if err := a.writeIfMissing(f.target, content); err != nil {
			return err
		}
	}
	local := fmt.Sprintf("# Variables %s gets in the local configuration, on top of base.env.\n", name)
	if err := a.writeIfMissing(filepath.Join("environments", name, "local.env"), []byte(local)); err != nil {
		return err
	}

	// 3. compose.yml and the Caddyfile.
	if err := a.addComposeService(name, stack); err != nil {
		return err
	}
	if err := a.addCaddySite(name, data.Port); err != nil {
		return err
	}

	// 4. A new app: run the stack's generator in the repo's container, then
	// give the repo its own git history.
	if isNew {
		if !opts.NoGenerate {
			if err := a.generate(stack, data); err != nil {
				return err
			}
		}
		if _, err := os.Stat(filepath.Join(repoDir, ".git")); errors.Is(err, fs.ErrNotExist) {
			if a.Runner.LookPath("git") != nil {
				fmt.Fprintf(a.Log, "warning: git not found, so repos/%s is not a git repository yet\n", name)
			} else if err := a.Runner.Run(repoDir, "git", "init", "--quiet"); err != nil {
				return err
			}
		}
	}

	fmt.Fprintf(a.Log, "\nAdded %s from the %s stack. Next:\n  neptuno up %s\n  open http://%s.localhost\n", name, stack.Name, name, name)
	if isNew {
		fmt.Fprintf(a.Log, "When it has a remote, make it a submodule of this project with:\n  git submodule add <git-url> repos/%s\n", name)
	}
	return nil
}

func (a *Adder) addSubmodule(url, name string) error {
	root := a.Project.Root
	if a.Runner.LookPath("git") != nil {
		return errors.New("git not found on PATH")
	}
	if !a.Runner.Check(root, "git", "rev-parse", "--git-dir") {
		return fmt.Errorf("%s is not a git repository; run `git init` there first so repos can be submodules", root)
	}
	fmt.Fprintf(a.Log, "Cloning %s into repos/%s\n", url, name)
	return a.Runner.Run(root, "git", "submodule", "add", url, filepath.ToSlash(filepath.Join("repos", name)))
}

func (a *Adder) writeAppFiles(stack *stacks.Stack, data stacks.Data, repoDir string) error {
	files, err := stack.AppFiles(data)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	for rel, content := range files {
		if err := a.writeIfMissing(filepath.Join("repos", data.Name, filepath.FromSlash(rel)), content); err != nil {
			return err
		}
	}
	return nil
}

// writeIfMissing writes a file relative to the project root unless it
// already exists, so a user's edits are never overwritten.
func (a *Adder) writeIfMissing(rel string, content []byte) error {
	path := a.Project.Path(rel)
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(a.Log, "  kept    %s\n", filepath.ToSlash(rel))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(a.Log, "  created %s\n", filepath.ToSlash(rel))
	return nil
}

func (a *Adder) addComposeService(name string, stack *stacks.Stack) error {
	p := a.Project
	if ok, err := composeHasService(p.Path("compose.yml"), name); err != nil || ok {
		if ok {
			fmt.Fprintf(a.Log, "  kept    compose.yml service %s\n", name)
		}
		return err
	}
	var deps []string
	for _, s := range stack.Services {
		if _, ok := p.Services[s]; ok {
			deps = append(deps, s)
		}
	}
	path := p.Path("compose.yml")
	src, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out, err := insertService(src, serviceSpec{Name: name, DependsOn: deps})
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(a.Log, "  added   compose.yml service %s\n", name)
	return nil
}

func (a *Adder) addCaddySite(name string, port int) error {
	path := a.Project.Path("Caddyfile")
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // no proxy in this project
	}
	if err != nil {
		return err
	}
	out, changed := appendCaddySite(src, name, port)
	if !changed {
		fmt.Fprintf(a.Log, "  kept    Caddyfile site %s.localhost\n", name)
		return nil
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(a.Log, "  added   Caddyfile site http://%s.localhost -> %s:%d\n", name, name, port)
	return nil
}

// generate runs the stack's generate command in a one-off container of the
// new service, with repos/<name> mounted at /app.
func (a *Adder) generate(stack *stacks.Stack, data stacks.Data) error {
	command, err := stack.GenerateCommand(data)
	if err != nil || command == "" {
		return err
	}
	if a.Runner.LookPath("docker") != nil {
		return fmt.Errorf("the %s stack runs its generator in docker, which was not found; rerun `neptuno add %s --stack %s` once it is installed", stack.Name, data.Name, stack.Name)
	}

	// Reload so the new repo and service are known, then write env files:
	// compose refuses to run a service whose env_file is missing.
	p, err := project.Load(a.Project.Root)
	if err != nil {
		return err
	}
	st, err := state.Load(p.Root)
	if err != nil {
		return err
	}
	if err := (&orchestrate.Orchestrator{Project: p}).WriteEnvFiles(st, nil); err != nil {
		return err
	}

	// Containers run as root; hand the generated files back to the user on
	// Linux, where bind mounts keep the container's owner.
	if runtime.GOOS == "linux" && os.Getuid() > 0 {
		command = fmt.Sprintf("(%s) && chown -R %d:%d /app", command, os.Getuid(), os.Getgid())
	}
	fmt.Fprintf(a.Log, "Generating the %s app in its container: %s\n", stack.Name, command)
	err = a.Runner.Run(p.Root, "docker", "compose", "run", "--rm", "--no-deps", "--build", data.Name, "sh", "-c", command)
	if err != nil {
		return fmt.Errorf("%w\nFix the problem, empty repos/%s, and rerun `neptuno add %s --stack %s`; finished steps are skipped", err, data.Name, data.Name, stack.Name)
	}
	return nil
}

func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
}
