package cli

import (
	"errors"
	"os"
	"os/exec"

	"github.com/mattn/go-isatty"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
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
			var service string
			if cmd.ArgsLenAtDash() == 0 {
				if service = serviceFromFolder(cmd, o.Project); service == "" {
					return errors.New("name a repo before --, or run this inside one of its folders")
				}
			} else {
				service, args = args[0], args[1:]
			}
			wd, _ := os.Getwd()
			c, err := o.ExecCommand(orchestrate.ExecOptions{
				Service: service,
				Args:    args,
				Dir:     wd,
				TTY:     isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd()),
			})
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
