package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robertoplancarte/neptuno/internal/env"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

// The right-hand panel's tabs.
const (
	tabInfo = iota
	tabEnv
	tabLogs
)

var tabNames = []string{"Info", "Env", "Logs"}

var (
	focusColor   = lipgloss.Color("2")
	borderColor  = lipgloss.Color("8")
	titleFocused = lipgloss.NewStyle().Foreground(focusColor).Bold(true)
	keyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	tabActive    = lipgloss.NewStyle().Foreground(focusColor).Bold(true).Underline(true)
	selectedRow  = lipgloss.NewStyle().Reverse(true)
)

// panelMsg carries the Env or Logs tab's content for a row.
type panelMsg struct {
	key  string
	text string
}

// panelKey names what the right panel's Env or Logs tab shows now, so a
// late answer for another row or tab is dropped.
func (m *dashboard) panelKey() string {
	r, ok := m.current()
	if !ok || m.tab == tabInfo {
		return ""
	}
	return fmt.Sprintf("%d:%s:%s", m.tab, r.Name, r.Env)
}

// fetchPanel loads the Env or Logs tab's content off the UI goroutine.
func (m *dashboard) fetchPanel() tea.Cmd {
	key := m.panelKey()
	r, _ := m.current()
	switch {
	case key == "":
		return nil
	case m.tab == tabEnv:
		vars := m.opts.Vars
		return func() tea.Msg { return panelMsg{key, envText(r, vars)} }
	default:
		logs := m.opts.Logs
		return func() tea.Msg { return panelMsg{key, logsText(r, logs)} }
	}
}

func envText(r orchestrate.ServiceStatus, vars func(string) ([]env.Var, error)) string {
	if !r.Repo {
		return r.Name + " takes its environment from compose.yml.\nNeptuno manages env files for repos only."
	}
	if vars == nil {
		return ""
	}
	list, err := vars(r.Name)
	if err != nil {
		return errStyle.Render(err.Error())
	}
	if len(list) == 0 {
		return "No variables for " + r.Name + " in " + r.Env + "."
	}
	width := 0
	for _, v := range list {
		width = max(width, len(v.Key)+len(v.Value)+1)
	}
	lines := []string{labelStyle.Render(fmt.Sprintf("%s, env %s (e to switch)", r.Name, r.Env)), ""}
	for _, v := range list {
		pad := strings.Repeat(" ", width-len(v.Key)-len(v.Value)-1)
		lines = append(lines, keyStyle.Render(v.Key)+"="+v.Value+pad+"  "+labelStyle.Render("# "+v.Source))
	}
	return strings.Join(lines, "\n")
}

func logsText(r orchestrate.ServiceStatus, logs func(string) (string, error)) string {
	if r.Repo && r.RunMode == "host" {
		return r.Name + " runs on the host, outside docker.\nPress enter to open its window, which shows each process's output."
	}
	if logs == nil {
		return ""
	}
	out, err := logs(r.Name)
	if err != nil {
		return errStyle.Render(err.Error())
	}
	if strings.TrimSpace(out) == "" {
		return labelStyle.Render("No container output for " + r.Name + " yet.")
	}
	return out
}

// layoutView is the dashboard in panels: the project, repos and services on
// the left, a tabbed panel for the row under the cursor on the right, and the
// keys along the bottom.
func (m *dashboard) layoutView() string {
	width, height := m.width, m.height
	leftW := min(max(width*2/5, 36), 64)
	rightW := width - leftW
	bodyH := height - 1

	var repos, svcs []int
	for i, r := range m.rows {
		if r.Repo {
			repos = append(repos, i)
		} else {
			svcs = append(svcs, i)
		}
	}
	projectH := 4
	rest := bodyH - projectH
	inRepos := false
	if r, ok := m.current(); ok {
		inRepos = r.Repo
	}
	// The focused list takes the room the other doesn't need.
	other := len(svcs)
	if !inRepos {
		other = len(repos)
	}
	otherH := min(max(other+2, 3), max(3, rest/3))
	repoH, svcH := rest-otherH, otherH
	if !inRepos {
		repoH, svcH = otherH, rest-otherH
	}

	left := lipgloss.JoinVertical(lipgloss.Left,
		box("Project", m.projectLines(), leftW, projectH, false),
		box(fmt.Sprintf("Repos %d", len(repos)), m.listLines(repos, repoH-2, leftW-2), leftW, repoH, inRepos),
		box(fmt.Sprintf("Services %d", len(svcs)), m.listLines(svcs, svcH-2, leftW-2), leftW, svcH, !inRepos),
	)
	right := box(m.tabTitle(), m.rightLines(bodyH-2, rightW-2), rightW, bodyH, false)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" + m.keyBar(width)
}

func (m *dashboard) projectLines() []string {
	running := 0
	for _, r := range m.rows {
		if r.Running() {
			running++
		}
	}
	line := nameStyle.Render(m.opts.Project) + labelStyle.Render(fmt.Sprintf("  %d/%d up", running, len(m.rows)))
	updated := "loading…"
	if !m.updated.IsZero() {
		updated = "updated " + m.updated.Format("15:04:05")
	}
	return []string{line, labelStyle.Render(m.opts.Session + " · " + updated)}
}

// listLines renders the rows at idx, scrolled so the cursor shows.
func (m *dashboard) listLines(idx []int, height, width int) []string {
	if len(idx) == 0 {
		return []string{labelStyle.Render("none")}
	}
	cells := make([][]string, len(idx))
	for j, i := range idx {
		cells[j] = m.listCells(m.rows[i])
	}
	widths := columnWidths(cells)
	start := 0
	for j, i := range idx {
		if i == m.cursor && j >= height {
			start = j - height + 1
		}
	}
	var lines []string
	for j := start; j < len(idx) && len(lines) < height; j++ {
		r := m.rows[idx[j]]
		c := append([]string(nil), cells[j]...)
		mark := " "
		if m.selected[r.Name] {
			mark = "✓"
		}
		plain := mark + " " + formatRow(c, widths)
		if idx[j] == m.cursor {
			plain = ansi.Truncate(plain, width, "…")
			lines = append(lines, selectedRow.Render(plain+strings.Repeat(" ", max(0, width-lipgloss.Width(plain)))))
			continue
		}
		// Color the state dot and word after padding, so columns line up.
		line := strings.Replace(plain, c[1], stateStyle(r).Render(c[1]), 1)
		lines = append(lines, line)
	}
	return lines
}

// listCells is a row's columns in the left panels.
func (m *dashboard) listCells(r orchestrate.ServiceStatus) []string {
	cells := []string{r.Name, stateLabel(r)}
	if !r.Repo {
		return append(cells, strings.Join(r.Ports, " "))
	}
	git := "-"
	if g := r.Git; g != nil {
		git = g.Branch + " " + g.Commit
		if g.Dirty {
			git += "*"
		}
		if !g.Date.IsZero() {
			git += " " + strings.TrimSuffix(relative(m.now().Sub(g.Date)), " ago")
		}
	}
	return append(cells, git, r.Env)
}

func (m *dashboard) tabTitle() string {
	var parts []string
	for i, name := range tabNames {
		if i == m.tab {
			parts = append(parts, tabActive.Render(name))
		} else {
			parts = append(parts, labelStyle.Render(name))
		}
	}
	return strings.Join(parts, labelStyle.Render(" │ "))
}

// rightLines is the right panel's content: the env chooser when open, else
// the active tab, scrolled.
func (m *dashboard) rightLines(height, width int) []string {
	var text string
	r, ok := m.current()
	switch {
	case m.envs != nil:
		text = m.envView()
	case !ok:
		text = "compose.yml has no services yet; add a repo with `neptuno add`."
	case m.tab == tabInfo:
		text = m.details(r)
	case m.panel.key != m.panelKey():
		text = labelStyle.Render("Loading…")
	default:
		text = m.panel.text
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	// Logs follow the end, like tail -f, unless scrolled up.
	if m.tab == tabLogs && m.envs == nil {
		end := max(0, len(lines)-height-m.scroll)
		m.scroll = min(m.scroll, max(0, len(lines)-height))
		lines = lines[end:]
	} else {
		m.scroll = min(m.scroll, max(0, len(lines)-height))
		lines = lines[m.scroll:]
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(strings.ReplaceAll(l, "\t", "    "), width, "…")
	}
	return lines
}

func (m *dashboard) keyBar(width int) string {
	keys := [][2]string{{"enter", "open"}, {"u", "up"}, {"s", "stop"}, {"r", "restart"}, {"e", "env"},
		{"c", "claude"}, {"space", "select"}, {"tab", "panel"}, {"[ ]", "tab"}, {"q", "quit"}}
	if m.envs != nil {
		keys = [][2]string{{"↑↓", "choose"}, {"enter", "switch"}, {"esc", "cancel"}}
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, keyStyle.Render(k[0])+" "+k[1])
	}
	bar := strings.Join(parts, labelStyle.Render(" | "))
	if m.message != "" {
		bar = m.message + labelStyle.Render("  ·  ") + bar
	}
	return ansi.Truncate(bar, width, "…")
}

// box draws a rounded border around lines, with title in the top edge, at
// exactly w by h cells. A focused box has a colored border and title.
func box(title string, lines []string, w, h int, focused bool) string {
	if w < 4 || h < 2 {
		return ""
	}
	border := lipgloss.NewStyle().Foreground(borderColor)
	t := title
	if focused {
		border = lipgloss.NewStyle().Foreground(focusColor)
		if !strings.Contains(title, "\x1b") {
			t = titleFocused.Render(title)
		}
	}
	t = ansi.Truncate(" "+t+" ", w-4, "…")
	var b strings.Builder
	b.WriteString(border.Render("╭─") + t + border.Render(strings.Repeat("─", max(0, w-3-lipgloss.Width(t)))+"╮") + "\n")
	for i := 0; i < h-2; i++ {
		var l string
		if i < len(lines) {
			l = ansi.Truncate(lines[i], w-2, "…")
		}
		b.WriteString(border.Render("│") + l + strings.Repeat(" ", max(0, w-2-lipgloss.Width(l))) + border.Render("│") + "\n")
	}
	b.WriteString(border.Render("╰" + strings.Repeat("─", w-2) + "╯"))
	return b.String()
}
