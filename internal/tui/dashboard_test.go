package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robertoplancarte/neptuno/internal/env"
	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/proxy"
)

func dashboardRows() []orchestrate.ServiceStatus {
	return []orchestrate.ServiceStatus{
		{Name: "api", Repo: true, RunMode: "container", Container: "running", Overmind: true, Window: true, Env: "local", Envs: []string{"local", "staging", "production"},
			Processes: []orchestrate.ProcessStatus{{Name: "web", Status: "running"}, {Name: "worker", Status: "running"}},
			Git: &orchestrate.GitStatus{Branch: "main", Commit: "abc1234", Subject: "Add login", Author: "Roberto",
				Date: time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)}},
		{Name: "docs", Repo: true, RunMode: "host", Env: "staging", Envs: []string{"local", "staging"}},
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
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
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

func TestDashboardEnvs(t *testing.T) {
	m := newTestDashboard()
	if view := m.View(); !strings.Contains(view, "[local] staging production") || !strings.Contains(view, "local [staging]") {
		t.Errorf("view does not list each repo's envs:\n%s", view)
	}

	key(m, "e")
	if m.envs == nil || m.envs.cursor != 0 {
		t.Fatalf("e did not open the chooser on the current env: %+v", m.envs)
	}
	if view := m.View(); !strings.Contains(view, "Switch api's env") || !strings.Contains(view, "recreates api's container") {
		t.Errorf("chooser view:\n%s", view)
	}
	key(m, "esc")
	if m.envs != nil {
		t.Error("esc left the chooser open")
	}
	if cmd := key(m, "e", "enter"); cmd != nil || !strings.Contains(m.message, "already uses local") {
		t.Errorf("choosing the current env ran %v, message %q", cmd, m.message)
	}
	if cmd := key(m, "e", "down", "down", "enter"); cmd == nil || m.envs != nil {
		t.Errorf("switching to production returned %v, chooser %v", cmd, m.envs)
	}

	key(m, "down", "down", "e")
	if !strings.Contains(m.message, "envs belong to repos") || m.envs != nil {
		t.Errorf("e on a compose service: %q", m.message)
	}
}

func TestSwitchEnvCommands(t *testing.T) {
	rows := dashboardRows()
	if got, want := switchEnvCommands(rows[0], "staging"), [][]string{{"env", "use", "api=staging"}, {"up", "--detach", "api"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("running container repo: %v", got)
	}
	if got, want := switchEnvCommands(rows[1], "local"), [][]string{{"env", "use", "docs=local"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("stopped repo: %v", got)
	}
	rows[1].Overmind = true
	if got, want := switchEnvCommands(rows[1], "local"), [][]string{{"env", "use", "docs=local"}, {"restart", "docs"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("running host repo: %v", got)
	}
}

func TestDashboardPanels(t *testing.T) {
	m := newTestDashboard()
	m.opts.Vars = func(repo string) ([]env.Var, error) {
		return []env.Var{{Key: "PORT", Value: "3000", Source: "environments/" + repo + "/base.env"}}, nil
	}
	m.opts.Logs = func(name string, _ time.Time) (string, error) { return "line 1\nline 2\n" + name + " booted", nil }
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 30 {
		t.Errorf("view has %d lines, want 30", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w > 120 {
			t.Errorf("line %d is %d wide:\n%s", i, w, l)
		}
	}
	for _, want := range []string{"Project", "Repos 2", "Services 1", "Info", "Logs", "abc1234", "Add login", "enter open"} {
		if !strings.Contains(view, want) {
			t.Errorf("panel view missing %q:\n%s", want, view)
		}
	}

	// ] moves to the Env tab, which loads off the UI goroutine.
	cmd := key(m, "]")
	if m.tab != tabEnv || cmd == nil || !strings.Contains(m.View(), "Loading") {
		t.Fatalf("] did not switch to a loading Env tab: tab %d", m.tab)
	}
	m.Update(panelMsg{m.panelKey(), envText(m.rows[0], m.opts.Vars)})
	if view := m.View(); !strings.Contains(view, "PORT=3000") || !strings.Contains(view, "environments/api/base.env") {
		t.Errorf("Env tab:\n%s", view)
	}
	// A late answer for another row is dropped.
	m.Update(panelMsg{"1:docs:staging", "stale"})
	if strings.Contains(m.View(), "stale") {
		t.Error("stale panel content shown")
	}

	key(m, "]")
	m.Update(panelMsg{m.panelKey(), logsText(m.rows[0], m.opts.Logs, time.Time{})})
	if view := m.View(); m.tab != tabLogs || !strings.Contains(view, "api booted") {
		t.Errorf("Logs tab:\n%s", view)
	}
	key(m, "]", "]")
	if m.tab != tabInfo {
		t.Errorf("tabs do not wrap: %d", m.tab)
	}

	// tab jumps to the services list and back to where it was.
	key(m, "down", "tab")
	if r, _ := m.current(); r.Name != "postgres" {
		t.Errorf("tab went to %s, want postgres", r.Name)
	}
	key(m, "tab")
	if r, _ := m.current(); r.Name != "docs" {
		t.Errorf("tab back went to %s, want docs", r.Name)
	}
	if !strings.Contains(logsText(m.rows[1], m.opts.Logs, time.Time{}), "runs on the host") {
		t.Error("host repo logs should point at its window")
	}
}

func TestDetailsShowsProxyURLs(t *testing.T) {
	m := newTestDashboard()
	r := dashboardRows()[0]
	r.URLs = []proxy.Link{{URL: "http://api.localhost"}, {URL: "http://*.api.localhost", Pattern: true}}
	got := m.details(r)
	link := "\x1b]8;;http://api.localhost\x07"
	if !strings.Contains(got, link) || !strings.Contains(got, "\x1b]8;;\x07") {
		t.Errorf("details has no OSC 8 link to http://api.localhost:\n%q", got)
	}
	if strings.Contains(got, "\x1b]8;;http://*.api.localhost") {
		t.Errorf("wildcard should not be a link:\n%q", got)
	}
	plain := ansi.Strip(got)
	if !strings.Contains(plain, "URL        http://api.localhost\n") || !strings.Contains(plain, "           http://*.api.localhost\n") {
		t.Errorf("details =\n%s", plain)
	}
}

func TestDetailsShowsMCPServers(t *testing.T) {
	m := newTestDashboard()
	r := dashboardRows()[0]
	r.MCP = []mcp.Server{
		{Repo: r.Name, Name: "hyperdrive", HostURL: "http://api.localhost/_hyperdrive/mcp", ContainerURL: "http://api:3000/_hyperdrive/mcp"},
		{Repo: r.Name, Name: "tools"},
	}
	r.MCPHealth = map[string]mcp.Health{r.Name + "-hyperdrive": mcp.Up}
	plain := ansi.Strip(m.details(r))
	for _, want := range []string{
		"MCP        " + r.Name + "-hyperdrive  http://api.localhost/_hyperdrive/mcp  ● answering\n",
		"           in containers http://api:3000/_hyperdrive/mcp\n",
		"           " + r.Name + "-tools  no Caddyfile site\n",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("details missing %q:\n%s", want, plain)
		}
	}
	if cells := m.repoCells(r); cells[0] != r.Name+" (mcp)" {
		t.Errorf("repo cell = %q", cells[0])
	}
}

func TestHealthLabel(t *testing.T) {
	r := dashboardRows()[0]
	id := r.Name + "-x"
	cases := map[mcp.Health]string{mcp.Up: "● answering", mcp.Down: "● not answering", mcp.NotFound: "● nothing at this path"}
	for h, want := range cases {
		r.MCPHealth = map[string]mcp.Health{id: h}
		if got := ansi.Strip(healthLabel(r, id)); got != want {
			t.Errorf("healthLabel(%s) = %q, want %q", h, got, want)
		}
	}
	r.Container, r.Overmind = "", false
	if got := ansi.Strip(healthLabel(r, id)); got != "stopped" {
		t.Errorf("healthLabel of a stopped repo = %q", got)
	}
}

func TestMCPMenu(t *testing.T) {
	rows := dashboardRows()
	rows[0].MCP = []mcp.Server{{Repo: rows[0].Name, Name: "hyperdrive", HostURL: "http://api.localhost/mcp", ContainerURL: "http://api:3000/mcp"}}
	var copied []string
	var ran []string
	m := newDashboard(DashboardOptions{Project: "acme", Status: func() []orchestrate.ServiceStatus { return rows },
		Copy: func(s string) error { copied = append(copied, s); return nil },
		Exec: func(name string, args ...string) (string, error) {
			ran = append(ran, name+" "+strings.Join(args, " "))
			return "", nil
		}})
	m.Update(statusMsg(rows))

	key(m, "m")
	if m.menu == nil || len(m.menu.items) != 4 {
		t.Fatalf("menu = %+v", m.menu)
	}
	if !strings.Contains(ansi.Strip(m.menuView()), "> Copy api-hyperdrive's URL for this machine") {
		t.Errorf("menu view =\n%s", ansi.Strip(m.menuView()))
	}
	key(m, "enter")
	key(m, "m", "down", "enter")
	want := []string{"http://api.localhost/mcp", "claude mcp add --transport http --scope user api-hyperdrive http://api.localhost/mcp"}
	if !reflect.DeepEqual(copied, want) {
		t.Errorf("copied %q, want %q", copied, want)
	}
	key(m, "m", "down", "down")
	if cmd := key(m, "enter"); cmd != nil {
		cmd()
	}
	if len(ran) != 1 || ran[0] != "claude mcp add --transport http --scope user api-hyperdrive http://api.localhost/mcp" {
		t.Errorf("ran %q", ran)
	}

	key(m, "down") // web, which has no MCP servers
	key(m, "m")
	if m.menu != nil || !strings.Contains(m.message, "declares no MCP servers") {
		t.Errorf("opened a menu for a repo without servers: %q", m.message)
	}
}

func TestProjectPanelMCPLine(t *testing.T) {
	m := newTestDashboard()
	if len(m.projectLines()) != 2 {
		t.Errorf("project panel shows MCP with no servers declared: %q", m.projectLines())
	}
	m.Update(mcpMsg(orchestrate.MCPSummary{Servers: 2, ProjectFileCurrent: true}))
	lines := m.projectLines()
	if got := ansi.Strip(lines[len(lines)-1]); got != "MCP 2 · ✓ .mcp.json · ✗ desktop (neptuno mcp desktop)" {
		t.Errorf("MCP line = %q", got)
	}
}

func TestWarningsShowAndWrap(t *testing.T) {
	m := newTestDashboard()
	r := dashboardRows()[0]
	r.Warnings = []string{"web could not start: port 3000 is held by pid 88 (puma), left by an earlier run; neptuno restart api stops it"}
	if plain := ansi.Strip(m.details(r)); !strings.Contains(plain, "⚠ web could not start: port 3000 is held by pid 88") {
		t.Errorf("details has no warning:\n%s", plain)
	}
	lines := wrapWarnings(strings.Split(m.details(r), "\n"), 40)
	for _, l := range lines {
		if ansi.StringWidth(l) > 40 && strings.Contains(l, "port 3000") {
			t.Errorf("warning line not wrapped: %q", ansi.Strip(l))
		}
	}
	got := ansi.Strip(StatusWarnings([]orchestrate.ServiceStatus{r}, []string{"overmind for shop (pid 9) is still running"}))
	if got != "\n⚠ "+r.Name+": "+r.Warnings[0]+"\n⚠ overmind for shop (pid 9) is still running\n" {
		t.Errorf("StatusWarnings = %q", got)
	}
	if StatusWarnings([]orchestrate.ServiceStatus{dashboardRows()[0]}, nil) != "" {
		t.Error("StatusWarnings printed something with nothing to warn about")
	}

	m.Update(hostMsg{"overmind for shop (pid 9) is still running"})
	if lines := m.projectLines(); !strings.Contains(ansi.Strip(lines[len(lines)-1]), "overmind for shop") {
		t.Errorf("project panel: %q", lines)
	}
}
