package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/procfile"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/spf13/cobra"
)

func newExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <repo|service> [--] <command...>",
		Short: "Run a command where a repo runs: in its container, or on the host",
		Long: "Runs a command in the repo's container with docker compose exec, with the\n" +
			"repo's language versions, packages, env and services, starting the\n" +
			"container first (as `neptuno up --detach` does) when it isn't running. A\n" +
			"repo with run: host runs it in repos/<repo> on this machine, with the\n" +
			"repo's env. Any other compose service works too, such as postgres.\n\n" +
			"A single argument is a shell command, run with sh -c, so it can hold &&\n" +
			"and pipes. Several arguments run as they are. Run from a subfolder of\n" +
			"repos/<repo>, the command runs in the same folder of the container. It\n" +
			"gets a terminal when neptuno exec has one, and exits with the command's\n" +
			"status.\n\n" +
			"Claude Code uses it to run a repo's tests and tools from the host. Inside\n" +
			"one of a repo's folders the repo can be left out: neptuno exec -- <command>.",
		Example: "  neptuno exec api -- bin/rails test\n" +
			"  neptuno exec api 'bin/rails db:migrate && bin/rails test'\n" +
			"  neptuno exec postgres -- psql -U postgres\n" +
			"  cd repos/api && neptuno exec -- bundle exec rspec",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			// What up prints goes to stderr, so stdout is the command's own.
			o.Runner = orchestrate.ExecRunner{Out: cmd.ErrOrStderr(), Err: cmd.ErrOrStderr()}
			wd, _ := os.Getwd()
			opts, err := execOptions(cmd, o.Project, args, cmd.ArgsLenAtDash())
			if err != nil {
				return err
			}
			opts.Dir = wd
			opts.TTY = isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
			c, err := execCommand(o, opts)
			if err != nil {
				return err
			}
			run := exec.Command(c.Args[0], c.Args[1:]...)
			run.Dir, run.Stdin, run.Stdout, run.Stderr = c.Dir, os.Stdin, os.Stdout, os.Stderr
			err = run.Run()
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				os.Exit(exit.ExitCode())
			}
			return err
		},
	}
	// Flags after the repo belong to the command, as in neptuno exec api ls -la.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// execCommand builds the command to run; tests replace it.
var execCommand = (*orchestrate.Orchestrator).ExecCommand

// execOptions splits neptuno exec's arguments into the repo or service and
// the command. dashAt is cobra's ArgsLenAtDash: 0 when the arguments start
// with --, so the repo comes from the current folder. Flag parsing stops at
// the repo, so a -- after it is still among args; only that one separator
// is dropped, and any later -- belongs to the command.
func execOptions(cmd *cobra.Command, p *project.Project, args []string, dashAt int) (orchestrate.ExecOptions, error) {
	if dashAt == 0 {
		service := serviceFromFolder(cmd, p)
		if service == "" {
			return orchestrate.ExecOptions{}, errors.New("name a repo before --, or run this inside one of its folders")
		}
		return orchestrate.ExecOptions{Service: service, Args: args}, nil
	}
	service, command := args[0], args[1:]
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if _, ok := p.Services[service]; !ok && !p.IsRepo(service) {
		msg := fmt.Sprintf("no repo or service named %q; neptuno status lists them", service)
		wd, _ := os.Getwd()
		if here := p.ServiceAt(wd); here != "" {
			msg += fmt.Sprintf(". To run it in %s, put -- first: neptuno exec -- %s", here, shellJoin(args))
		}
		return orchestrate.ExecOptions{}, errors.New(msg)
	}
	return orchestrate.ExecOptions{Service: service, Args: command}, nil
}

// shellJoin quotes args for a command line shown to the user.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = procfile.Quote(a)
	}
	return strings.Join(quoted, " ")
}
