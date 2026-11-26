package cli

import (
	"fmt"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/spf13/cobra"
)

func newClaudeCommand() *cobra.Command {
	var detach bool
	cmd := &cobra.Command{
		Use:   "claude [-- claude-args...]",
		Short: "Open the project's Claude Code, below the dashboard",
		Long: "Shows Claude Code in the pane below the dashboard, in the first window of the\n" +
			"project's tmux session, opening the window and the pane if either is\n" +
			"missing. Claude runs on this machine in the project folder: it edits the\n" +
			"code in repos/ directly, runs each repo's tools in its container with\n" +
			"`neptuno exec <repo> -- <command>`, and has a subagent per repo in\n" +
			".claude/agents for work inside one repo. .mcp.json gives it the repos' MCP\n" +
			"servers, and .claude/settings.json lets it edit repos/ and run neptuno exec\n" +
			"without asking.\n\n" +
			"Arguments after -- go to claude when the pane starts.",
		Example: "  neptuno claude\n" +
			"  neptuno claude -- --continue",
		RunE: func(cmd *cobra.Command, args []string) error {
			var claudeArgs []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				args, claudeArgs = args[:dash], args[dash:]
			}
			if len(args) > 0 {
				return fmt.Errorf("Claude Code now runs on this machine for the whole project, not in a repo's container. "+
					"Run `neptuno claude` and ask it to work on %s: it hands the task to %s's subagent, which runs commands with `neptuno exec %s`", args[0], args[0], args[0])
			}
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			return o.Claude(orchestrate.ClaudeOptions{Args: claudeArgs, Detach: detach})
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "select the pane without attaching to the session")
	return cmd
}
