package orchestrate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/procfile"
)

// ClaudeOptions configures Claude.
type ClaudeOptions struct {
	// Args are passed to claude when its pane is started, such as
	// --continue. A pane that is already open keeps its session.
	Args []string
	// Detach selects the pane without attaching to the session.
	Detach bool
}

// Claude shows the project's Claude Code session: the pane below the
// dashboard, in the first window of the project's tmux session, where
// Claude runs on the host in the project folder. It opens the dashboard
// window and the pane when either is missing, selects the pane, and
// attaches to the session, or switches to it inside tmux.
//
// Claude edits code in repos/ directly and runs the repos' own tools in
// their containers with neptuno exec, so nothing about Claude lives in the
// containers.
func (o *Orchestrator) Claude(opts ClaudeOptions) error {
	if o.Runner.LookPath("tmux") != nil {
		return errors.New("tmux not found on PATH")
	}
	if o.Runner.LookPath("claude") != nil {
		return errors.New("claude not found on PATH; install Claude Code from https://claude.com/claude-code")
	}
	if _, err := o.SyncMCP(); err != nil {
		fmt.Fprintf(o.Log, "warning: could not write the MCP configs: %v\n", err)
	}
	if err := o.ensureDashboardWith(opts.Args); err != nil {
		return err
	}
	pane := o.claudePane()
	if pane == "" {
		if err := o.splitClaude(opts.Args); err != nil {
			return err
		}
		if pane = o.claudePane(); pane == "" {
			return errors.New("could not find the Claude Code pane in the dashboard window")
		}
	} else if len(opts.Args) > 0 {
		fmt.Fprintln(o.Log, "Claude Code is already open below the dashboard, so the arguments for claude were not used.")
	}
	_ = o.Runner.Run(o.Project.Root, "tmux", "select-window", "-t", o.target(DashboardWindow))
	_ = o.Runner.Run(o.Project.Root, "tmux", "select-pane", "-t", pane)
	if opts.Detach {
		return nil
	}
	return o.attach()
}

// claudePane is the ID of the Claude Code pane in the dashboard window, or
// "" when it has none.
func (o *Orchestrator) claudePane() string {
	out, err := o.Runner.Output(o.Project.Root, "tmux", "list-panes", "-t", o.target(DashboardWindow),
		"-F", "#{pane_id}\t#{pane_start_command}")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		id, command, _ := strings.Cut(line, "\t")
		if strings.Contains(command, strings.TrimSpace(claudeMark)) {
			return id
		}
	}
	return ""
}

// claudeCommand runs Claude Code in the dashboard's pane, keeping the pane
// open on failure so the error can be read.
func claudeCommand(args []string) string {
	command := "claude"
	for _, a := range args {
		command += " " + procfile.Quote(a)
	}
	return claudeMark + command +
		`; status=$?; if [ $status -ne 0 ]; then echo; echo "claude exited with status $status. Press enter to close this pane."; read _; fi`
}
