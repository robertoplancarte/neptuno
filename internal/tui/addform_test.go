package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robertoplancarte/neptuno/internal/stacks"
)

func typeText(m *dashboard, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func formDashboard(t *testing.T) *dashboard {
	m := newTestDashboard()
	m.opts.Neptuno, m.opts.Root = "echo", t.TempDir()
	m.width, m.height = 140, 40
	m.opts.Stacks = func() ([]*stacks.Stack, error) { return stacks.List("") }
	return m
}

func TestAddFormBuildsTheAddCommand(t *testing.T) {
	m := formDashboard(t)
	key(m, "n")
	if m.form == nil {
		t.Fatal("n did not open the add-repo form")
	}
	if s := m.form.current(); s == nil || s.Name != stacks.Default {
		t.Fatalf("form starts on stack %v, want %s", s, stacks.Default)
	}
	// Letters go into the name, even ones that are hotkeys elsewhere.
	typeText(m, "shopq")
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	key(m, "down")
	if m.form == nil || m.form.name != "shop" {
		t.Fatalf("name = %q, want shop", m.form.name)
	}
	typeText(m, "git@github.com:acme/shop.git")
	key(m, "down")
	for m.form.current().Name != "rails" {
		key(m, "right")
	}
	if !m.form.on["tailwind"] || m.form.on["action_text"] {
		t.Errorf("rails options did not start at their defaults: %v", m.form.on)
	}
	// Options are sorted by name: action_text, mailpit, storage_s3, tailwind.
	key(m, "down", " ", "down", "down", "down", " ")
	view := m.View()
	for _, want := range []string{"Add a repo", "shop", "rails", "[x] action_text", "[ ] tailwind", "Active Storage"} {
		if !strings.Contains(view, want) {
			t.Errorf("form view missing %q:\n%s", want, view)
		}
	}
	want := []string{"add", "shop", "git@github.com:acme/shop.git", "--stack", "rails", "--with", "action_text,mailpit", "--without", "storage_s3,tailwind"}
	if got := m.form.args(); !reflect.DeepEqual(got, want) {
		t.Errorf("args = %q\nwant   %q", got, want)
	}

	key(m, "down", " ", "down")
	if !m.form.wildcard || m.form.row != m.form.submitRow() {
		t.Fatalf("wildcard %t, row %d", m.form.wildcard, m.form.row)
	}
	cmd := key(m, "enter")
	if cmd == nil || m.form.added != "shop" || !m.actions["shop"].running {
		t.Fatalf("enter on the button did not run add: %v %+v", cmd, m.form)
	}
	if !strings.Contains(m.View(), "Adding shop") {
		t.Errorf("form does not show the add running:\n%s", m.View())
	}
	drain(t, m, cmd)
	if m.form != nil || m.focus != "shop" || m.tab != tabLogs {
		t.Errorf("after add: form %v, focus %q, tab %d", m.form, m.focus, m.tab)
	}
	if got := m.actions["shop"].lines; len(got) != 1 || !strings.HasPrefix(got[0], "add shop git@github.com:acme/shop.git --stack rails") || !strings.HasSuffix(got[0], "--wildcard") {
		t.Errorf("ran %q", got)
	}
	// The cursor moves to the new repo once the status lists it.
	rows := append(dashboardRows(), dashboardRows()[0])
	rows[len(rows)-1].Name = "shop"
	m.Update(statusMsg(rows))
	if r, ok := m.current(); !ok || r.Name != "shop" || m.focus != "" {
		t.Errorf("cursor on %q after the status listed shop", r.Name)
	}
}

func TestAddFormChecksTheName(t *testing.T) {
	m := formDashboard(t)
	key(m, "n")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.form.err == "" || m.form.added != "" {
		t.Errorf("empty name: err %q", m.form.err)
	}
	typeText(m, "postgres")
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if !strings.Contains(m.form.err, "not a repo") {
		t.Errorf("service name: err %q", m.form.err)
	}
	key(m, "esc")
	if m.form != nil {
		t.Error("esc did not close the form")
	}
	if cmd := key(m, "u"); cmd == nil {
		t.Error("hotkeys don't work after the form closes")
	}
}

func TestAddFormKeepsFailuresOnScreen(t *testing.T) {
	m := formDashboard(t)
	m.opts.Neptuno = "false"
	key(m, "n")
	typeText(m, "web")
	cmd := m.submitAdd()
	drain(t, m, cmd)
	if m.form == nil || !strings.Contains(m.View(), "failed") {
		t.Fatalf("failed add closed the form:\n%s", m.View())
	}
	key(m, "esc")
	if m.form != nil {
		t.Error("esc did not hide the failed add")
	}
}

func TestActionsTabListsAndRunsActions(t *testing.T) {
	m := newVolumesDashboard(t)
	key(m, "[")
	if m.tab != tabActions {
		t.Fatalf("[ from Info went to tab %d, want Actions", m.tab)
	}
	view := m.View()
	for _, want := range []string{"Actions for api", "Open api's window", "Start api", "Stop api", "Restart api's processes",
		"Switch api's env (now local)", "Run Claude Code in api's container", "Add a repo", "choose action"} {
		if !strings.Contains(view, want) {
			t.Errorf("Actions tab missing %q:\n%s", want, view)
		}
	}

	// enter focuses the list, arrows move in it, and enter runs the entry.
	key(m, "enter")
	if !m.actionsFocus || m.cursor != 0 {
		t.Fatalf("enter did not focus the list: %t, cursor %d", m.actionsFocus, m.cursor)
	}
	key(m, "down")
	if m.actionCursor != 1 || m.cursor != 0 {
		t.Fatalf("down moved the list to %d and the cursor to %d", m.actionCursor, m.cursor)
	}
	cmd := key(m, "enter")
	if cmd == nil || m.actionsFocus || m.actions["api"] == nil || m.actions["api"].name != "up api" {
		t.Fatalf("choosing Start did not run up: %v %+v", cmd, m.actions["api"])
	}
	drain(t, m, cmd)

	// Unavailable actions say why; choosing one gives the usual message.
	key(m, "down")
	if r, _ := m.current(); r.Name != "docs" {
		t.Fatalf("cursor on %q", r.Name)
	}
	m.tab = tabActions
	if view := m.View(); !strings.Contains(view, "not running; start it with u") || !strings.Contains(view, "runs on the host") {
		t.Errorf("docs' actions don't say why they can't run:\n%s", view)
	}
	list := m.availableActions()
	key(m, "enter")
	for i, a := range list {
		if a.key == "r" {
			m.actionCursor = i
		}
	}
	key(m, "enter")
	if !strings.Contains(m.message, "not running") {
		t.Errorf("restart from the list: %q", m.message)
	}

	// Services get fewer actions, volumes their own.
	key(m, "down")
	if view := m.View(); strings.Contains(view, "Restart postgres") || !strings.Contains(view, "Stop postgres") {
		t.Errorf("postgres' actions:\n%s", view)
	}
	moveTo(t, m, "postgres_data")
	m.tab = tabActions
	if view := m.View(); !strings.Contains(view, "Clear postgres_data, deleting its data") || !strings.Contains(view, "│ ") {
		t.Errorf("volume actions:\n%s", view)
	}
	key(m, "enter", "enter")
	if m.confirm != "postgres_data" || !strings.Contains(m.View(), "Press y to delete it") {
		t.Errorf("choosing Clear did not ask to confirm:\n%s", m.View())
	}
	key(m, "x")
	if m.confirm != "" {
		t.Error("confirm survived another key")
	}

	// Hotkeys work while the list has the cursor.
	key(m, "enter", "n")
	if m.form == nil || m.actionsFocus {
		t.Errorf("n in the focused list: form %v, focus %t", m.form, m.actionsFocus)
	}
}
