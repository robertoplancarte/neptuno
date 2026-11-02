package cli

import (
	"os"

	"github.com/robertoplancarte/neptuno/internal/procfile"
	"github.com/robertoplancarte/neptuno/internal/procs"
	"github.com/spf13/cobra"
)

// newContainerExecCommand backs container-mode Procfiles: overmind runs it
// for each line, and it runs the command in the repo's container with
// docker compose exec, recorded under the overmind run, and stops it there
// when the line is stopped or its terminal goes away.
func newContainerExecCommand() *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:    "container-exec <service> <process> <command>",
		Hidden: true,
		Args:   cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			service, process, command := args[0], args[1], args[2]
			code := procs.Exec(procs.ExecOptions{
				Service: service,
				Process: process,
				Script:  procfile.ContainerScript(service, command, wait),
				Run:     os.Getenv(procs.RunEnv),
				Stdin:   os.Stdin,
				Stdout:  os.Stdout,
				Stderr:  os.Stderr,
			})
			os.Exit(code)
			return nil
		},
	}
	cmd.Flags().BoolVar(&wait, "wait-for-entrypoint", false, "wait for the container's entrypoint.sh to finish setup first")
	return cmd
}
