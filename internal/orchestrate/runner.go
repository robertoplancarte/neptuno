// Package orchestrate drives docker compose, overmind and tmux to bring a
// project's services up and down.
package orchestrate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/engine"
)

// Runner runs external commands. Tests swap in a fake.
type Runner interface {
	// Run runs a command in dir, streaming its output to the runner's log.
	Run(dir, name string, args ...string) error
	// Check runs a command quietly and reports whether it succeeded.
	Check(dir, name string, args ...string) bool
	// Output runs a command quietly and returns its trimmed stdout.
	Output(dir, name string, args ...string) (string, error)
	// Attach hands the terminal to a command until it exits.
	Attach(dir, name string, args ...string) error
	// LookPath reports whether a program is installed.
	LookPath(name string) error
}

// ExecRunner runs commands with os/exec. Callers write docker commands,
// which it runs with the machine's container engine, so `docker compose
// ps` runs `podman compose ps` where Podman is the engine.
type ExecRunner struct {
	Out, Err io.Writer
	// Verbose echoes each command before running it.
	Verbose bool
	// Engine runs the docker commands, engine.Current() when nil.
	Engine *engine.Engine
}

func (r ExecRunner) engine() engine.Engine {
	if r.Engine != nil {
		return *r.Engine
	}
	return engine.Current()
}

// command builds the command for name and args, translated for the engine.
func (r ExecRunner) command(dir, name string, args ...string) *exec.Cmd {
	name, args = r.engine().Command(name, args...)
	if r.Verbose {
		fmt.Fprintf(r.Err, "$ %s %s\n", name, strings.Join(args, " "))
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd
}

// failed wraps a command's error with the command line it ran.
func failed(cmd *exec.Cmd, err error) error {
	return fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
}

func (r ExecRunner) Run(dir, name string, args ...string) error {
	cmd := r.command(dir, name, args...)
	cmd.Stdout, cmd.Stderr = r.Out, r.Err
	if err := cmd.Run(); err != nil {
		return failed(cmd, err)
	}
	return nil
}

// Stream is Run that also copies the command's output to w. The command's
// output is then not a terminal, so docker compose prints it plainly.
func (r ExecRunner) Stream(w io.Writer, dir, name string, args ...string) error {
	cmd := r.command(dir, name, args...)
	cmd.Stdout, cmd.Stderr = io.MultiWriter(r.Out, w), io.MultiWriter(r.Err, w)
	if err := cmd.Run(); err != nil {
		return failed(cmd, err)
	}
	return nil
}

func (r ExecRunner) Check(dir, name string, args ...string) bool {
	r.Verbose = false
	return r.command(dir, name, args...).Run() == nil
}

func (r ExecRunner) Output(dir, name string, args ...string) (string, error) {
	r.Verbose = false
	cmd := r.command(dir, name, args...)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
	}
	return strings.TrimSpace(string(out)), err
}

func (r ExecRunner) Attach(dir, name string, args ...string) error {
	r.Verbose = false
	cmd := r.command(dir, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// LookPath reports whether a program is installed. For docker it checks
// the engine's programs, such as podman.
func (r ExecRunner) LookPath(name string) error {
	names := []string{name}
	if name == engine.Docker {
		names = r.engine().Programs()
	}
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			return err
		}
	}
	return nil
}
