// Package cli wires Neptuno's commands together.
package cli

import (
	"github.com/spf13/cobra"
)

// Version is set at build time with -ldflags "-X github.com/robertoplancarte/neptuno/internal/cli.Version=...".
var Version = "dev"

// NewRootCommand builds the neptuno command tree.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "neptuno",
		Short: "Environment as code for multi-repo development",
		Long: "Neptuno runs a multi-repo development environment by driving docker compose,\n" +
			"overmind and tmux. Run it with no arguments inside a project to open the\n" +
			"dashboard, which shows every repo and service, its state and its git\n" +
			"commit, and starts, stops, restarts and opens them.",
		Version:      Version,
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDashboard(cmd)
		},
	}
	root.AddCommand(
		newInitCommand(),
		newAddCommand(),
		newStacksCommand(),
		newUpCommand(),
		newDownCommand(),
		newStatusCommand(),
		newClaudeCommand(),
		newEnvCommand(),
		newRestartCommand(),
		newHostExecCommand(),
		newPaneDiedCommand(),
	)
	return root
}

// Execute runs the root command with os.Args.
func Execute() error {
	return NewRootCommand().Execute()
}
