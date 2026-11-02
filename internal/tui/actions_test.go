package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// drain runs an action's commands until it ends, as bubbletea would.
func drain(t *testing.T, m *dashboard, cmd tea.Cmd) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for cmd != nil && time.Now().Before(deadline) {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			// The action's command is the first; the rest load the panel.
			msg = batch[0]()
		}
		a, ok := msg.(actionMsg)
		if !ok {
			return
		}
		_, cmd = m.Update(a)
		if a.done {
			return
		}
	}
	t.Fatal("action did not end")
}

func TestDashboardRunsActionsInTheBackground(t *testing.T) {
	m := newTestDashboard()
	m.opts.Neptuno, m.opts.Root = "echo", t.TempDir()
	m.width, m.height = 120, 30

	cmd := key(m, "u")
	a := m.actions["api"]
	if cmd == nil || a == nil || !a.running {
		t.Fatalf("u did not start an action: %v %+v", cmd, a)
	}
	if m.tab != tabLogs {
		t.Errorf("tab = %d, want the Logs tab", m.tab)
	}
	if cmd := key(m, "r"); cmd != nil || !strings.Contains(m.message, "busy") {
		t.Errorf("restart during up ran %v, message %q", cmd, m.message)
	}

	drain(t, m, cmd)
	if a.running || a.err != nil || !reflect.DeepEqual(a.lines, []string{"up --detach api"}) {
		t.Errorf("action after it ended: %+v", a)
	}
	if !strings.Contains(m.message, "up api done") {
		t.Errorf("message = %q", m.message)
	}
	m.panel = panelMsg{key: m.panelKey(), text: "container says hi"}
	if view := m.View(); !strings.Contains(view, "neptuno up api") || !strings.Contains(view, "up --detach api") || !strings.Contains(view, "container says hi") {
		t.Errorf("Logs tab does not show the action's output:\n%s", view)
	}

	m.opts.Neptuno = "false"
	drain(t, m, key(m, "r"))
	if a := m.actions["api"]; a.err == nil || !strings.Contains(m.message, "restart api failed") {
		t.Errorf("failing restart: %+v, message %q", a, m.message)
	}
}

func TestSplitLines(t *testing.T) {
	for _, tc := range []struct {
		in, rest string
		lines    []string
	}{
		{"a\nb", "b", []string{"a"}},
		{"a\r\nb\n", "", []string{"a", "b"}},
		{"50%\r100%\rdone\n", "", []string{"50%", "100%", "done"}},
		{"a\r", "a\r", nil},
		{"\n\n", "", []string{"", ""}},
	} {
		lines, rest := splitLines(tc.in)
		if !reflect.DeepEqual(lines, tc.lines) || rest != tc.rest {
			t.Errorf("splitLines(%q) = %q, %q; want %q, %q", tc.in, lines, rest, tc.lines, tc.rest)
		}
	}
}
