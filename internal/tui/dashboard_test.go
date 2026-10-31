package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

func dashboardRows() []orchestrate.ServiceStatus {
	return []orchestrate.ServiceStatus{
		{Name: "api", Repo: true, RunMode: "container", Container: "running", Overmind: true, Window: true, Env: "local",
			Processes: []orchestrate.ProcessStatus{{Name: "web", Status: "running"}, {Name: "worker", Status: "running"}},
			Git: &orchestrate.GitStatus{Branch: "main", Commit: "abc1234", Subject: "Add login", Author: "Roberto",
				Date: time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)}},
		{Name: "docs", Repo: true, RunMode: "host", Env: "local"},
		{Name: "postgres", Container: "running", Health: "healthy", Ports: []string{"5432->5432"}},
	}
}

func newTestDashboard() *dashboard {
	m := newDashboard(DashboardOptions{Project: "acme", Status: dashboardRows})
	m.now = func() time.Time { return time.Date(2026, 10, 30, 14, 0, 0, 0, time.Local) }
	m.Update(statusMsg(dashboardRows()))
	return m
}

func key(m *dashboard, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd = m.Update(msg)
	}
	return cmd
}

func TestDashboardView(t *testing.T) {
	m := newTestDashboard()
	view := m.View()
	for _, want := range []string{
		"REPO", "SERVICE", "api", "● running", "2/2 running", "main", "abc1234", "Oct 30 12:00 (2h ago)",
		"docs", "○ stopped", "postgres", "5432->5432",
		"Add login", "Roberto",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestDashboardKeys(t *testing.T) {
	m := newTestDashboard()

	key(m, "down", "enter")
	if !strings.Contains(m.message, "docs has no window") {
		t.Errorf("enter on a stopped repo: %q", m.message)
	}
	key(m, "c")
	if !strings.Contains(m.message, "container") {
		t.Errorf("claude on a host repo: %q", m.message)
	}
	key(m, "r")
	if !strings.Contains(m.message, "not running") {
		t.Errorf("restart on a stopped repo: %q", m.message)
	}

	key(m, " ", "down", " ")
	if got := m.targets(); !reflect.DeepEqual(got, []string{"docs", "postgres"}) {
		t.Errorf("targets = %v", got)
	}
	if cmd := key(m, "u"); cmd == nil || len(m.selected) != 0 {
		t.Errorf("u returned %v and left %v selected", cmd, m.selected)
	}
	if got := m.targets(); !reflect.DeepEqual(got, []string{"postgres"}) {
		t.Errorf("targets after up = %v, want the cursor row", got)
	}

	m.Update(statusMsg(dashboardRows()[:1]))
	if m.cursor != 0 {
		t.Errorf("cursor = %d after rows shrank", m.cursor)
	}
}

func TestRenderStatus(t *testing.T) {
	out := RenderStatus(dashboardRows())
	if strings.Contains(out, "> ") || !strings.Contains(out, "abc1234") {
		t.Errorf("RenderStatus:\n%s", out)
	}
}
