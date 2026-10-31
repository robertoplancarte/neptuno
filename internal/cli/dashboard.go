package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/tui"
	"github.com/spf13/cobra"
)

// runDashboard backs `neptuno` with no arguments: the interactive dashboard
// inside a project, help outside one, and a plain table without a terminal.
func runDashboard(cmd *cobra.Command) error {
	o, err := loadOrchestrator(cmd)
	if errors.Is(err, project.ErrNotFound) {
		fmt.Fprintf(cmd.ErrOrStderr(), "%v\nRun `neptuno init <name>` to create one.\n\n", err)
		return cmd.Help()
	}
	if err != nil {
		return err
	}
	if !isatty.IsTerminal(os.Stdout.Fd()) && !isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		fmt.Fprint(cmd.OutOrStdout(), tui.RenderStatus(o.Status()))
		return nil
	}
	return tui.RunDashboard(tui.DashboardOptions{
		Project: o.Project.Name,
		Root:    o.Project.Root,
		Session: o.SessionName(),
		Neptuno: o.Neptuno,
		Status:  o.Status,
	})
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print every repo and service with its state and git commit",
		Long: "Prints what the dashboard shows, once: each repo's state, overmind\n" +
			"processes, branch, commit and commit date, and each service's container\n" +
			"state and ports. Run `neptuno` with no arguments for the interactive dashboard.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), tui.RenderStatus(o.Status()))
			return nil
		},
	}
}
