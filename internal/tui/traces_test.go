package tui

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/proxy"
)

func testTraces() ([]orchestrate.Trace, error) {
	return []orchestrate.Trace{
		{Repo: "api", Test: "CheckoutTest#test_paying_by_card", Result: "failed", Location: "test/system/checkout_test.rb:12",
			Failure: `expected to find text "Paid" in "Card declined"`, File: "CheckoutTest_test_paying_by_card.zip",
			Finished: time.Date(2026, 10, 30, 13, 55, 0, 0, time.Local), Duration: 2300 * time.Millisecond, Size: 48 << 10},
		{Repo: "api", Test: "CartTest#test_adding", Result: "passed", File: "CartTest_test_adding.zip",
			Finished: time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)},
	}, nil
}

// playwrightRows are dashboardRows with api's container depending on the
// playwright service.
func playwrightRows(playwright string) func() []orchestrate.ServiceStatus {
	return func() []orchestrate.ServiceStatus {
		rows := dashboardRows()
		rows[0].DependsOn = []string{"postgres", "playwright"}
		return append(rows, orchestrate.ServiceStatus{Name: "playwright", Container: playwright})
	}
}

func newTracesDashboard(t *testing.T, playwright string) (*dashboard, *[]string) {
	var opened []string
	m := newTestDashboard()
	m.opts.Status, m.opts.Traces = playwrightRows(playwright), testTraces
	m.opts.OpenURL = func(u string) error { opened = append(opened, u); return nil }
	m.width, m.height = 140, 40
	m.Update(statusMsg(m.opts.Status()))
	m.Update(m.loadTraces()())
	return m, &opened
}

func TestDashboardHasNoTracesWithoutPlaywright(t *testing.T) {
	m := newTestDashboard()
	m.opts.Traces = testTraces
	m.width, m.height = 140, 40
	m.Update(m.loadTraces()())
	if view := m.View(); strings.Contains(view, "Traces") || strings.Contains(view, "CheckoutTest") {
		t.Errorf("a project with no Playwright repo shows traces:\n%s", view)
	}
	if m.total() != len(m.rows) || m.sections() != 3 {
		t.Errorf("the cursor reaches %d rows in %d lists", m.total(), m.sections())
	}
}

func TestDashboardListsTraces(t *testing.T) {
	m, opened := newTracesDashboard(t, "running")
	view := m.View()
	for _, want := range []string{"Traces 2", "CheckoutTest#paying_by_card", "✗ failed", "CartTest#adding", "✓ passed", "5m",
		"Traces     2 saved, 1 failed  (in the Traces list)"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}

	// tab cycles repos, services, volumes (none here) and traces.
	key(m, "tab", "tab")
	tr, ok := m.currentTrace()
	if !ok || tr.Test != "CheckoutTest#test_paying_by_card" {
		t.Fatalf("tab tab did not reach the traces: cursor %d", m.cursor)
	}
	view = m.View()
	for _, want := range []string{
		"Trace │ Actions", "Card declined", "test/system/checkout_test.rb:12", "ran 2.3s", "48 KB",
		"repos/api/tmp/playwright-traces/CheckoutTest_test_paying_by_card.zip",
		"neptuno exec api -- bin/rails test test/system/checkout_test.rb:12", "PLAYWRIGHT_TRACE=all",
		"enter open in Trace Viewer",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("trace panel missing %q:\n%s", want, view)
		}
	}
	if cmd := key(m, "u"); cmd != nil || !strings.Contains(m.message, "enter opens a trace") {
		t.Errorf("u on a trace ran %v, message %q", cmd, m.message)
	}

	cmd := key(m, "enter")
	if cmd == nil {
		t.Fatalf("enter did nothing: %q", m.message)
	}
	if msg := cmd(); msg != nil {
		t.Errorf("opening sent %v", msg)
	}
	if len(*opened) != 1 {
		t.Fatalf("opened %v", *opened)
	}
	u, err := url.Parse((*opened)[0])
	if err != nil || u.Host != "playwright.localhost" || u.Path != "/trace/index.html" ||
		u.Query().Get("trace") != "http://playwright.localhost/traces/api/CheckoutTest_test_paying_by_card.zip" {
		t.Errorf("opened %q", (*opened)[0])
	}

	key(m, "tab")
	if r, ok := m.current(); !ok || r.Name != "api" {
		t.Errorf("tab from the traces did not go back to the repos: cursor %d", m.cursor)
	}
}

func TestCompactDashboardListsTraces(t *testing.T) {
	m, _ := newTracesDashboard(t, "running")
	m.width, m.height = 60, 10
	key(m, "tab", "tab")
	view := ansi.Strip(m.View())
	for _, want := range []string{"TRACE", "RESULT", "> ", "CheckoutTest#paying_by_card", "enter open in Trace Viewer"} {
		if !strings.Contains(view, want) {
			t.Errorf("compact view missing %q:\n%s", want, view)
		}
	}
}

func TestDashboardKeepsCursorOnTraceWhenListChanges(t *testing.T) {
	m, _ := newTracesDashboard(t, "running")
	key(m, "tab", "tab", "down")
	if tr, _ := m.currentTrace(); tr.Test != "CartTest#test_adding" {
		t.Fatalf("cursor on %q", tr.Test)
	}
	// A new failure lands at the top of the list.
	list, _ := testTraces()
	list = append([]orchestrate.Trace{{Repo: "api", Test: "LoginTest#test_signing_in", Result: "error", File: "LoginTest_test_signing_in.zip",
		Finished: time.Date(2026, 10, 30, 13, 59, 0, 0, time.Local)}}, list...)
	m.Update(tracesMsg{list: list})
	if tr, _ := m.currentTrace(); tr.Test != "CartTest#test_adding" {
		t.Errorf("cursor moved to %q", tr.Test)
	}
}

func TestDashboardTracesNeedPlaywrightRunning(t *testing.T) {
	m, opened := newTracesDashboard(t, "exited")
	key(m, "tab", "tab")
	if cmd := key(m, "enter"); cmd != nil || len(*opened) != 0 || !strings.Contains(m.message, "playwright service is exited") {
		t.Errorf("enter with playwright stopped: %v, opened %v, message %q", cmd, *opened, m.message)
	}
	if view := m.View(); !strings.Contains(view, "start it (u on it in Services)") {
		t.Errorf("trace panel doesn't say playwright is stopped:\n%s", view)
	}
}

func TestDashboardEmptyTraces(t *testing.T) {
	m, _ := newTracesDashboard(t, "running")
	m.Update(tracesMsg{})
	view := m.View()
	for _, want := range []string{"Traces 0", noTraces, "none yet; a failed system test saves one in tmp/playwright-traces"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	// With nothing in it, tab skips the list.
	key(m, "tab", "tab")
	if _, ok := m.currentTrace(); ok || m.focusSection() != sectionRepos {
		t.Errorf("tab stopped on the empty Traces list: cursor %d", m.cursor)
	}
}

func TestTraceURLFollowsTheProxyPort(t *testing.T) {
	m, opened := newTracesDashboard(t, "running")
	rows := playwrightRows("running")()
	rows[len(rows)-1].URLs = []proxy.Link{{URL: "http://playwright.localhost:8080"}}
	m.Update(statusMsg(rows))
	key(m, "tab", "tab", "enter")()
	want := "http://playwright.localhost:8080/trace/index.html?trace=" +
		url.QueryEscape("http://playwright.localhost:8080/traces/api/CheckoutTest_test_paying_by_card.zip")
	if len(*opened) != 1 || (*opened)[0] != want {
		t.Errorf("opened %v, want %s", *opened, want)
	}
}
