package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/mattn/go-isatty"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/tui"
	"github.com/spf13/cobra"
)

// runDashboard backs `neptuno` with no arguments: inside a project it opens
// the dashboard in the project's tmux session, attaching to the session or
// switching to it; outside one it prints help, and without a terminal it
// prints a plain table. Without tmux the dashboard runs in this terminal.
func runDashboard(cmd *cobra.Command) error {
	o, err := loadOrchestrator(cmd)
	if errors.Is(err, project.ErrNotFound) {
		fmt.Fprintf(cmd.ErrOrStderr(), "%v\nRun `neptuno init <name>` to create one.\n\n", err)
		return cmd.Help()
	}
	if err != nil {
		return err
	}
	if !isTerminal() {
		fmt.Fprint(cmd.OutOrStdout(), tui.RenderStatus(o.Status()))
		return nil
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "tmux not found on PATH, so the dashboard runs in this terminal.")
		return dashboardHere(o)
	}
	return o.OpenDashboard()
}

// newDashboardCommand runs the dashboard in the current terminal. The
// dashboard window of the project's session runs it.
func newDashboardCommand() *cobra.Command {
	return &cobra.Command{
		Use:   orchestrate.DashboardCommand,
		Short: "Run the dashboard in this terminal, outside the tmux session",
		Long: "Runs the dashboard right here instead of in the project's tmux session.\n" +
			"`neptuno` with no arguments opens it in the session's neptuno window.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			if !isTerminal() {
				fmt.Fprint(cmd.OutOrStdout(), tui.RenderStatus(o.Status()))
				return nil
			}
			return dashboardHere(o)
		},
	}
}

func isTerminal() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

func dashboardHere(o *orchestrate.Orchestrator) error {
	return tui.RunDashboard(tui.DashboardOptions{
		Project: o.Project.Name,
		Root:    o.Project.Root,
		Session: o.SessionName(),
		Neptuno: o.Neptuno,
		Status:  o.Status,
		Vars:    o.EnvVars,
		Logs:    o.Logs,
		MCP:     o.MCPSummary,
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
