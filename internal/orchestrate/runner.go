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

// ExecRunner runs commands with os/exec.
type ExecRunner struct {
	Out, Err io.Writer
	// Verbose echoes each command before running it.
	Verbose bool
}

func (r ExecRunner) Run(dir, name string, args ...string) error {
	if r.Verbose {
		fmt.Fprintf(r.Err, "$ %s %s\n", name, strings.Join(args, " "))
	}
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, r.Out, r.Err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (r ExecRunner) Check(dir, name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Run() == nil
}

func (r ExecRunner) Output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
	}
	return strings.TrimSpace(string(out)), err
}

func (r ExecRunner) Attach(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (r ExecRunner) LookPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}
