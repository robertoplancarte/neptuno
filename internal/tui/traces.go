package tui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/proxy"
	"github.com/robertoplancarte/neptuno/internal/scaffold"
)

// tracesMsg carries the Playwright traces of the repos that use Playwright,
// once loaded.
type tracesMsg struct {
	list []orchestrate.Trace
	err  error
}

// loadTraces lists the traces off the UI goroutine.
func (m *dashboard) loadTraces() tea.Cmd {
	list := m.opts.Traces
	if list == nil {
		return nil
	}
	return func() tea.Msg {
		traces, err := list()
		return tracesMsg{traces, err}
	}
}

// setTraces takes a new list of traces, keeping the cursor on the trace it
// was on.
func (m *dashboard) setTraces(msg tracesMsg) {
	before, on := m.currentTrace()
	m.traces, m.tracesErr = msg.list, msg.err
	if on {
		for i, t := range m.visibleTraces() {
			if t.Repo == before.Repo && t.File == before.File {
				m.cursor = len(m.rows) + len(m.volumes) + i
				break
			}
		}
	}
	m.clampCursor()
}

// showTraces says whether the dashboard has a Traces list: only when a repo
// uses Playwright, since only browser-driven tests leave traces.
func (m *dashboard) showTraces() bool {
	if m.opts.Traces == nil {
		// `neptuno status` doesn't look for traces.
		return false
	}
	for _, r := range m.rows {
		if r.Repo && slices.Contains(r.DependsOn, orchestrate.PlaywrightService) {
			return true
		}
	}
	return false
}

// visibleTraces are the traces the Traces list shows.
func (m *dashboard) visibleTraces() []orchestrate.Trace {
	if !m.showTraces() {
		return nil
	}
	return m.traces
}

// currentTrace is the trace under the cursor, if it is on one.
func (m *dashboard) currentTrace() (orchestrate.Trace, bool) {
	list := m.visibleTraces()
	i := m.cursor - len(m.rows) - len(m.volumes)
	if m.onAddRepo || i < 0 || i >= len(list) {
		return orchestrate.Trace{}, false
	}
	return list[i], true
}

// sections is how many lists the left side has.
func (m *dashboard) sections() int {
	if m.showTraces() {
		return 4
	}
	return 3
}

// traceURL is where the playwright service's Trace Viewer opens t: at the
// playwright service's site, as the Caddyfile and the proxy's published
// port make it.
func (m *dashboard) traceURL(t orchestrate.Trace) string {
	site := scaffold.PlaywrightURL
	for _, r := range m.rows {
		if r.Name == orchestrate.PlaywrightService {
			for _, l := range r.URLs {
				if !l.Pattern && strings.Contains(l.URL, "playwright.localhost") {
					site = l.URL
					break
				}
			}
		}
	}
	return scaffold.TraceViewerURL(site, t.Repo, t.File)
}

// playwrightDown says why the Trace Viewer can't open now, or "" when the
// playwright service is running, or not listed.
func (m *dashboard) playwrightDown() string {
	for _, r := range m.rows {
		if r.Name == orchestrate.PlaywrightService && !r.Running() {
			return "the playwright service is " + r.State() + "; start it (u on it in Services) to open traces"
		}
	}
	return ""
}

// traceKey handles the keys that act on the trace under the cursor. It
// reports whether it handled key.
func (m *dashboard) traceKey(t orchestrate.Trace, key string) (tea.Cmd, bool) {
	switch key {
	case "enter", "o":
		return m.openTrace(t), true
	case "y":
		if err := m.copy(m.traceURL(t)); err != nil {
			m.message = warnStyle.Render("copy failed: " + err.Error())
		} else {
			m.message = "copied the Trace Viewer link for " + t.Test
		}
		return nil, true
	case "f":
		return m.refresh(), true
	case "u", "s", "r", "e", "c", "m", " ", "x":
		m.message = "that key works on repos and services; enter opens a trace"
		return nil, true
	}
	return nil, false
}

// openTrace opens a trace in the Trace Viewer, in the browser.
func (m *dashboard) openTrace(t orchestrate.Trace) tea.Cmd {
	if why := m.playwrightDown(); why != "" {
		m.message = warnStyle.Render(why)
		return nil
	}
	open, url := m.opts.OpenURL, m.traceURL(t)
	if open == nil {
		open = openInBrowser
	}
	m.message = "opening " + t.Test + " in the Trace Viewer…"
	return func() tea.Msg {
		if err := open(url); err != nil {
			return doneMsg{"open the trace (" + url + ")", err}
		}
		return nil
	}
}

// openInBrowser opens url in the default browser.
func openInBrowser(url string) error {
	name, args := "xdg-open", []string{url}
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	}
	return exec.Command(name, args...).Start()
}

// traceCells is a trace's columns in the left panel.
func (m *dashboard) traceCells(t orchestrate.Trace) []string {
	return []string{traceTest(t), resultLabel(t), t.Repo, strings.TrimSuffix(relative(m.now().Sub(t.Finished)), " ago")}
}

// traceTest is the test's name without the test_ prefix Rails adds, such
// as CheckoutTest#paying_by_card.
func traceTest(t orchestrate.Trace) string {
	return strings.Replace(t.Test, "#test_", "#", 1)
}

func resultLabel(t orchestrate.Trace) string {
	switch {
	case t.Failed():
		return "✗ " + t.Result
	case t.Result == "passed":
		return "✓ passed"
	case t.Result == "":
		return "· trace"
	}
	return "· " + t.Result
}

func resultStyle(t orchestrate.Trace) func(...string) string {
	switch {
	case t.Failed():
		return errStyle.Render
	case t.Result == "passed":
		return okStyle.Render
	}
	return headerStyle.Render
}

// noTraces is the empty Traces list's line.
const noTraces = "none yet: failed system tests save one"

// traceLines renders the Traces list, scrolled so the cursor shows.
func (m *dashboard) traceLines(height, width int) []string {
	list := m.visibleTraces()
	switch {
	case m.tracesErr != nil && len(list) == 0:
		return []string{errStyle.Render(m.tracesErr.Error())}
	case len(list) == 0:
		return []string{labelStyle.Render(noTraces)}
	}
	cells := make([][]string, len(list))
	for i, t := range list {
		cells[i] = m.traceCells(t)
	}
	widths := columnWidths(cells)
	start := 0
	if j := m.cursor - len(m.rows) - len(m.volumes); j >= height {
		start = j - height + 1
	}
	var lines []string
	for j := start; j < len(cells) && len(lines) < height; j++ {
		plain := "  " + formatRow(cells[j], widths)
		if len(m.rows)+len(m.volumes)+j == m.cursor {
			plain = ansi.Truncate(plain, width, "…")
			lines = append(lines, selectedRow.Render(plain+strings.Repeat(" ", max(0, width-lipgloss.Width(plain)))))
			continue
		}
		res := cells[j][1]
		lines = append(lines, strings.Replace(plain, res, resultStyle(list[j])(res), 1))
	}
	return lines
}

// traceTitle is the right panel's title for a trace: its panel and the
// Actions tab, which [ and ] switch between.
func (m *dashboard) traceTitle() string {
	if m.tab == tabActions {
		return labelStyle.Render("Trace │ ") + tabActive.Render("Actions")
	}
	return tabActive.Render("Trace") + labelStyle.Render(" │ Actions")
}

// tracePanel describes the trace under the cursor and how to open it.
func (m *dashboard) tracePanel(t orchestrate.Trace, width int) string {
	var lines []string
	add := func(label, value string) {
		lines = append(lines, labelStyle.Render(fmt.Sprintf("%-11s", label))+value)
	}
	lines = append(lines, nameStyle.Render(traceTest(t)), "")
	add("Repo", t.Repo)
	if t.Result != "" {
		add("Result", resultStyle(t)(resultLabel(t)))
	}
	when := t.Finished.Local().Format("Mon Jan 2 2006 15:04:05") + " (" + relative(m.now().Sub(t.Finished)) + ")"
	if t.Duration > 0 {
		when += labelStyle.Render(fmt.Sprintf(" · ran %.1fs", t.Duration.Seconds()))
	}
	add("Finished", when)
	if t.Location != "" {
		add("Test", t.Location)
	}
	if t.Failure != "" {
		for i, l := range strings.Split(ansi.Wordwrap(t.Failure, max(20, width-11), ""), "\n") {
			label := ""
			if i == 0 {
				label = "Failure"
			}
			add(label, errStyle.Render(l))
		}
	}
	add("File", filepath.Join("repos", t.Repo, orchestrate.TracesDir, t.File))
	add("Size", byteSize(t.Size))
	lines = append(lines, "")
	if why := m.playwrightDown(); why != "" {
		lines = append(lines, warningLine(why), "")
	}
	lines = append(lines,
		keyStyle.Render("enter")+" opens it in Playwright's Trace Viewer, in your browser:",
		"  "+hyperlink(proxy.Link{URL: m.traceURL(t)}),
		keyStyle.Render("y")+" copies that link.",
		"",
		labelStyle.Render("Step through each action with its screenshot, the DOM before and"),
		labelStyle.Render("after, console output and network calls."))
	if t.Location != "" {
		lines = append(lines, "", labelStyle.Render("Run it again:"),
			"  neptuno exec "+t.Repo+" -- bin/rails test "+t.Location)
	}
	lines = append(lines, "", labelStyle.Render("Failed system tests keep their traces. To keep every test's, run with"),
		labelStyle.Render("PLAYWRIGHT_TRACE=all, such as:"),
		"  neptuno exec "+t.Repo+" -- env PLAYWRIGHT_TRACE=all bin/rails test:system")
	return strings.Join(lines, "\n")
}

// repoTraces sums up a repo's traces for its Info tab, or "" for a repo
// that doesn't use Playwright.
func (m *dashboard) repoTraces(r orchestrate.ServiceStatus) string {
	if !r.Repo || !slices.Contains(r.DependsOn, orchestrate.PlaywrightService) {
		return ""
	}
	n, failed := 0, 0
	for _, t := range m.traces {
		if t.Repo == r.Name {
			n++
			if t.Failed() {
				failed++
			}
		}
	}
	if n == 0 {
		return labelStyle.Render("none yet; a failed system test saves one in " + orchestrate.TracesDir)
	}
	s := fmt.Sprintf("%d saved", n)
	if failed > 0 {
		s += ", " + errStyle.Render(fmt.Sprintf("%d failed", failed))
	}
	return s + labelStyle.Render("  (in the Traces list)")
}

// byteSize is a size such as 12 KB.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}
