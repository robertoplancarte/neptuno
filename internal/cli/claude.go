package cli

import (
	"errors"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/spf13/cobra"
)

func newClaudeCommand() *cobra.Command {
	var detach bool
	cmd := &cobra.Command{
		Use:   "claude [repo] [-- claude-args...]",
		Short: "Run Claude Code inside a repo's container",
		Long: "Opens Claude Code inside the repo's running container, in a claude-<repo>\n" +
			"window of the project's tmux session, starting the container if needed.\n" +
			"Claude sees the repo's code, tools and services exactly as the app does.\n\n" +
			"The repo defaults to the one whose folder you are in. Sign-in and settings\n" +
			"live in .neptuno/claude, mounted into every repo's container, so you sign in\n" +
			"once per project. Quitting Claude leaves the repo's processes running.",
		Example: "  neptuno claude api\n" +
			"  neptuno claude api -- --continue\n" +
			"  cd repos/api && neptuno claude",
		RunE: func(cmd *cobra.Command, args []string) error {
			var claudeArgs []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				args, claudeArgs = args[:dash], args[dash:]
			}
			if len(args) > 1 {
				return errors.New("name one repo; pass arguments for claude after --")
			}
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			var repo string
			if len(args) == 1 {
				repo = args[0]
			} else if repo = serviceFromFolder(cmd, o.Project); repo == "" {
				return errors.New("name a repo, or run this inside one of its folders")
			}
			return o.Claude(orchestrate.ClaudeOptions{Repo: repo, Args: claudeArgs, Detach: detach})
		},
	}
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "open the window without attaching to it")
	return cmd
}
