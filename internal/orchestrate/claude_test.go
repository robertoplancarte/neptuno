package orchestrate

import (
	"reflect"
	"strings"
	"testing"
)

func TestClaudeOpensThePaneBelowTheDashboard(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)

	if err := o.Claude(ClaudeOptions{Args: []string{"--continue"}}); err != nil {
		t.Fatal(err)
	}
	root := o.Project.Root
	want := []string{
		"tmux new-session -d -s neptuno-acme -n neptuno -c " + root + " /bin/neptuno dashboard",
		"tmux split-window -v -d -l 50% -t =neptuno-acme:neptuno -c " + root + " " + claudeCommand([]string{"--continue"}),
		"tmux select-window -t =neptuno-acme:neptuno",
		"tmux select-pane -t %9",
		"attach: tmux attach-session -t =neptuno-acme",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
	if !strings.HasPrefix(claudeCommand(nil), claudeMark+"claude;") {
		t.Errorf("claudeCommand(nil) = %q", claudeCommand(nil))
	}
}

func TestClaudeSelectsTheOpenPane(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-0/default,1,0")
	o, f := setup(t)
	f.sessions, f.windows[DashboardWindow] = true, true
	f.panes = "%1\t/bin/neptuno dashboard\n%4\t" + claudeCommand(nil) + "\n"

	if err := o.Claude(ClaudeOptions{Detach: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux select-window -t =neptuno-acme:neptuno",
		"tmux select-pane -t %4",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestClaudeReopensAClosedPane(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-0/default,1,0")
	o, f := setup(t)
	f.sessions, f.windows[DashboardWindow] = true, true
	f.panes = "%1\t/bin/neptuno dashboard\n"

	if err := o.Claude(ClaudeOptions{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux split-window -v -d -l 50% -t =neptuno-acme:neptuno -c " + o.Project.Root + " " + claudeCommand(nil),
		"tmux select-window -t =neptuno-acme:neptuno",
		"tmux select-pane -t %9",
		"tmux switch-client -t =neptuno-acme",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestClaudeNeedsClaude(t *testing.T) {
	o, f := setup(t)
	f.missing = map[string]bool{"claude": true}
	if err := o.Claude(ClaudeOptions{}); err == nil || !strings.Contains(err.Error(), "claude not found") {
		t.Errorf("err = %v", err)
	}
}
