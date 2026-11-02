package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/robertoplancarte/neptuno/internal/env"
	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/procfile"
	"github.com/robertoplancarte/neptuno/internal/proxy"
)

// DashboardOptions configures the dashboard.
type DashboardOptions struct {
	// Project is the project's name and Root its folder.
	Project, Root string
	// Session is the project's tmux session.
	Session string
	// Neptuno is the neptuno binary, which runs the dashboard's actions.
	Neptuno string
	// Status reports every repo and service. It runs off the UI goroutine.
	Status func() []orchestrate.ServiceStatus
	// Refresh is how often Status runs. Zero means every two seconds.
	Refresh time.Duration
	// Vars resolves a repo's env, for the Env tab.
	Vars func(repo string) ([]env.Var, error)
	// Logs returns a service's recent container output, for the Logs tab.
	Logs func(name string) (string, error)
	// MCP reports how far the project's MCP servers are set up, for the
	// project panel. It runs off the UI goroutine.
	MCP func() orchestrate.MCPSummary
	// Volumes lists the project's named volumes, for the Volumes list. It
	// runs off the UI goroutine.
	Volumes func() ([]orchestrate.Volume, error)
	// InspectVolume looks inside a volume, for its panel.
	InspectVolume func(orchestrate.Volume) (orchestrate.VolumeDetail, error)
	// Copy puts text on the clipboard. Nil uses tmux's buffer inside tmux,
	// else pbcopy, wl-copy or xclip, else an OSC 52 escape.
	Copy func(text string) error
	// Exec runs a command in the project folder, for the MCP menu's
	// `claude mcp add`. Nil runs it for real.
	Exec func(name string, args ...string) (string, error)
}

type (
	statusMsg []orchestrate.ServiceStatus
	mcpMsg    orchestrate.MCPSummary
	tickMsg   struct{}
	doneMsg   struct {
		action string
		err    error
	}
)

type dashboard struct {
	opts     DashboardOptions
	rows     []orchestrate.ServiceStatus
	loaded   bool
	loading  bool
	updated  time.Time
	cursor   int
	selected map[string]bool
	width    int
	height   int
	message  string
	// tab is the right panel's tab, scroll how far it is scrolled, and
	// panel the Env or Logs content last loaded.
	tab    int
	scroll int
	panel  panelMsg
	// lastIn remembers the cursor in each left-hand list, by section, for
	// tab.
	now func() time.Time
	// envs is the open env chooser, or nil.
	envs *envChooser
	// menu is the open MCP menu, or nil.
	menu *mcpMenu
	// mcp is the project's MCP setup, once loaded.
	mcp    *orchestrate.MCPSummary
	lastIn map[int]int
	// actions holds each service's last background action, and nextAction
	// numbers them.
	actions    map[string]*action
	nextAction int
	// volumes are the project's named volumes once loaded, which the
	// cursor reaches after the services, and confirm the one waiting for a
	// y to be cleared.
	volumes    []orchestrate.Volume
	volumesErr error
	confirm    string
}

// envChooser picks the configuration a repo runs with.
type envChooser struct {
	row    orchestrate.ServiceStatus
	cursor int
}

var (
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	nameStyle  = lipgloss.NewStyle().Bold(true)
	labelStyle = lipgloss.NewStyle().Faint(true)
	linkStyle  = lipgloss.NewStyle().Underline(true)
)

func newDashboard(opts DashboardOptions) *dashboard {
	if opts.Refresh == 0 {
		opts.Refresh = 2 * time.Second
	}
	return &dashboard{opts: opts, selected: map[string]bool{}, now: time.Now, lastIn: map[int]int{}, actions: map[string]*action{}}
}

// RunDashboard shows the interactive dashboard until the user quits.
func RunDashboard(opts DashboardOptions) error {
	_, err := tea.NewProgram(newDashboard(opts), tea.WithAltScreen()).Run()
	return err
}

// RenderStatus is the dashboard's table as plain text, for `neptuno status`.
func RenderStatus(rows []orchestrate.ServiceStatus) string {
	d := newDashboard(DashboardOptions{})
	d.rows, d.loaded, d.cursor = rows, true, -1
	return d.table()
}

func (m *dashboard) Init() tea.Cmd { return m.refresh() }

func (m *dashboard) refresh() tea.Cmd {
	if m.loading || m.opts.Status == nil {
		return nil
	}
	m.loading = true
	status := m.opts.Status
	cmds := []tea.Cmd{func() tea.Msg { return statusMsg(status()) }, m.loadVolumes()}
	if summary := m.opts.MCP; summary != nil {
		cmds = append(cmds, func() tea.Msg { return mcpMsg(summary()) })
	}
	return tea.Batch(cmds...)
}

func (m *dashboard) tick() tea.Cmd {
	return tea.Tick(m.opts.Refresh, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		first := !m.loaded
		m.rows, m.loaded, m.loading, m.updated = msg, true, false, m.now()
		m.clampCursor()
		if first {
			return m, tea.Batch(m.tick(), m.fetchPanel())
		}
		return m, nil
	case mcpMsg:
		sum := orchestrate.MCPSummary(msg)
		m.mcp = &sum
		return m, nil
	case tickMsg:
		// Looking inside a volume can take a while, so its panel loads once
		// per visit, and again after it is cleared.
		panel := m.fetchPanel()
		if _, onVolume := m.currentVolume(); onVolume && m.panel.key == m.panelKey() {
			panel = nil
		}
		return m, tea.Batch(m.refresh(), m.tick(), panel)
	case panelMsg:
		if msg.key == m.panelKey() {
			m.panel = msg
		}
		return m, nil
	case volumesMsg:
		_, wasOn := m.currentVolume()
		m.volumes, m.volumesErr = msg.list, msg.err
		m.clampCursor()
		if _, on := m.currentVolume(); on && !wasOn {
			return m, m.fetchPanel()
		}
		return m, nil
	case actionMsg:
		return m, m.handleAction(msg)
	case doneMsg:
		if msg.err != nil {
			m.message = warnStyle.Render(fmt.Sprintf("%s failed: %v", msg.action, msg.err))
		} else {
			m.message = msg.action + " done"
		}
		return m, m.refresh()
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		cursor, tab := m.cursor, m.tab
		model, cmd := m.key(msg.String())
		if m.cursor != cursor || m.tab != tab {
			m.scroll = 0
			if m.cursor < m.total() {
				m.lastIn[m.section(m.cursor)] = m.cursor
			}
			cmd = tea.Batch(cmd, m.fetchPanel())
		}
		return model, cmd
	}
	return m, nil
}

// clampCursor keeps the cursor on a row after the lists change.
func (m *dashboard) clampCursor() {
	m.cursor = max(0, min(m.cursor, m.total()-1))
}

func (m *dashboard) current() (orchestrate.ServiceStatus, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return orchestrate.ServiceStatus{}, false
	}
	return m.rows[m.cursor], true
}

// targets are the selected rows, or the one under the cursor.
func (m *dashboard) targets() []string {
	var names []string
	for _, r := range m.rows {
		if m.selected[r.Name] {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		if r, ok := m.current(); ok {
			names = []string{r.Name}
		}
	}
	return names
}

func (m *dashboard) key(key string) (tea.Model, tea.Cmd) {
	m.message = ""
	if m.envs != nil {
		return m, m.envKey(key)
	}
	if m.menu != nil {
		return m, m.menuKey(key)
	}
	if v, onVolume := m.currentVolume(); onVolume {
		if cmd, handled := m.volumeKey(v, key); handled {
			return m, cmd
		}
	}
	m.confirm = ""
	row, ok := m.current()
	switch key {
	case "m":
		switch {
		case !ok:
		case len(row.MCP) == 0:
			m.message = row.Name + " declares no MCP servers; add them to neptuno.yml under repos." + row.Name + ".mcp"
		default:
			m.menu = newMCPMenu(row)
		}
	case "e":
		switch {
		case !ok:
		case !row.Repo:
			m.message = row.Name + " is a container from compose.yml; envs belong to repos"
		default:
			m.envs = &envChooser{row: row}
			for i, name := range row.Envs {
				if name == row.Env {
					m.envs.cursor = i
				}
			}
		}
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "tab", "right", "l":
		// Jump to the next list on the left that has rows, where the
		// cursor last was in it.
		m.jumpSection(1)
	case "shift+tab", "left", "h":
		m.jumpSection(-1)
	case "]":
		m.tab = (m.tab + 1) % len(tabNames)
	case "[":
		m.tab = (m.tab + len(tabNames) - 1) % len(tabNames)
	case "pgup", "ctrl+u", "pgdown", "ctrl+d":
		// Logs scroll back from the end; the other tabs scroll down.
		step := 10
		if (key == "pgup" || key == "ctrl+u") == (m.tab != tabLogs) {
			step = -10
		}
		m.scroll = max(0, m.scroll+step)
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < m.total()-1 {
			m.cursor++
		}
	case " ", "x":
		if ok {
			m.selected[row.Name] = !m.selected[row.Name]
		}
	case "a":
		all := len(m.selected) < len(m.rows)
		m.selected = map[string]bool{}
		if all {
			for _, r := range m.rows {
				m.selected[r.Name] = true
			}
		}
	case "f":
		return m, m.refresh()
	case "u":
		if names := m.targets(); len(names) > 0 {
			m.selected = map[string]bool{}
			return m, m.run("up "+strings.Join(names, " "), names, append([]string{"up", "--detach"}, names...)...)
		}
	case "s":
		if names := m.targets(); len(names) > 0 {
			m.selected = map[string]bool{}
			return m, m.run("down "+strings.Join(names, " "), names, append([]string{"down"}, names...)...)
		}
	case "r":
		switch {
		case !ok:
		case !row.Repo:
			m.message = "restart works on repos; press s then u to restart a service"
		case !row.Overmind:
			m.message = row.Name + " is not running; press u to start it"
		default:
			return m, m.run("restart "+row.Name, []string{row.Name}, "restart", row.Name)
		}
	case "c":
		switch {
		case !ok:
		case !row.Repo || row.RunMode != "container":
			m.message = "Claude Code runs in a repo's container"
		default:
			return m, m.foreground("claude "+row.Name, "claude", row.Name)
		}
	case "enter":
		switch {
		case !ok:
		case !row.Repo:
			m.message = row.Name + " is a container; it has no window"
		case !row.Window:
			m.message = row.Name + " has no window; press u to start it"
		default:
			return m, m.open(row.Name)
		}
	}
	return m, nil
}

// envKey handles a key while the env chooser is open.
func (m *dashboard) envKey(key string) tea.Cmd {
	c := m.envs
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "esc", "q", "e":
		m.envs = nil
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down", "j":
		if c.cursor < len(c.row.Envs)-1 {
			c.cursor++
		}
	case "enter":
		m.envs = nil
		if c.cursor >= len(c.row.Envs) {
			return nil
		}
		repo, config := c.row.Name, c.row.Envs[c.cursor]
		if config == c.row.Env {
			m.message = repo + " already uses " + config
			return nil
		}
		return m.runAll(fmt.Sprintf("switch %s to %s", repo, config), []string{repo}, switchEnvCommands(c.row, config)...)
	}
	return nil
}

// switchEnvCommands sets a repo's env and, when it is running, restarts it
// with the new one: up recreates the container, whose env_file changed, and
// overmind with it; a host repo's processes reread the env on restart.
func switchEnvCommands(r orchestrate.ServiceStatus, config string) [][]string {
	cmds := [][]string{{"env", "use", r.Name + "=" + config}}
	switch {
	case !r.Running():
	case r.RunMode == "host":
		cmds = append(cmds, []string{"restart", r.Name})
	default:
		cmds = append(cmds, []string{"up", "--detach", r.Name})
	}
	return cmds
}

// foreground runs an interactive neptuno command, such as claude, with the
// terminal, keeping its output on screen when it fails.
func (m *dashboard) foreground(action string, args ...string) tea.Cmd {
	command := procfile.Quote(m.opts.Neptuno)
	for _, a := range args {
		command += " " + procfile.Quote(a)
	}
	script := command + ` || { status=$?; echo; echo "Press enter to return to the dashboard."; read _; exit $status; }`
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = m.opts.Root
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return doneMsg{action, err} })
}

// open shows a repo's window: it switches to it inside tmux, or attaches to
// the session outside it, coming back here on detach.
func (m *dashboard) open(window string) tea.Cmd {
	session := "=" + m.opts.Session
	_ = exec.Command("tmux", "select-window", "-t", session+":"+window).Run()
	if os.Getenv("TMUX") != "" {
		return func() tea.Msg {
			if err := exec.Command("tmux", "switch-client", "-t", session).Run(); err != nil {
				return doneMsg{"open " + window, err}
			}
			return nil
		}
	}
	return tea.ExecProcess(exec.Command("tmux", "attach-session", "-t", session),
		func(err error) tea.Msg {
			if err != nil {
				return doneMsg{"open " + window, err}
			}
			return nil
		})
}

func (m *dashboard) View() string {
	if m.loaded && m.width >= 70 && m.height >= 14 {
		return m.layoutView()
	}
	return m.compactView()
}

// compactView is the dashboard as a plain list, for small terminals.
func (m *dashboard) compactView() string {
	var b strings.Builder
	title := nameStyle.Render("Neptuno") + " · " + m.opts.Project
	if !m.updated.IsZero() {
		title += labelStyle.Render("  updated " + m.updated.Format("15:04:05"))
	}
	b.WriteString(title + "\n\n")
	if !m.loaded {
		b.WriteString("Loading…\n")
		return b.String()
	}
	b.WriteString(m.table())
	if m.envs != nil {
		b.WriteString("\n" + m.envView())
	} else if v, ok := m.currentVolume(); ok {
		b.WriteString("\n" + m.volumePanel(v, max(40, m.width)) + "\n")
	} else if r, ok := m.current(); ok {
		b.WriteString("\n" + m.details(r))
	}
	if m.message != "" {
		b.WriteString("\n" + m.message + "\n")
	}
	help := "↑↓ move · ←→ list · space select · enter open window · u up · s stop · r restart · e env · c claude · f refresh · q quit"
	if m.envs != nil {
		help = "↑↓ move · enter switch · esc cancel"
	}
	if _, ok := m.currentVolume(); ok {
		help = "↑↓ move · ←→ list · enter clear · q quit"
		if m.confirm != "" {
			help = "y delete · any other key cancels"
		}
	}
	b.WriteString("\n" + helpStyle.Render(help) + "\n")
	if m.width > 0 {
		lines := strings.Split(b.String(), "\n")
		for i, l := range lines {
			lines[i] = lipgloss.NewStyle().MaxWidth(m.width).Render(l)
		}
		return strings.Join(lines, "\n")
	}
	return b.String()
}

// table lists the repos, then the other services, one row each.
func (m *dashboard) table() string {
	if len(m.rows) == 0 {
		return "compose.yml has no services yet; add a repo with `neptuno add`.\n"
	}
	repoHead := []string{"REPO", "STATE", "PROCESSES", "BRANCH", "COMMIT", "DATE", "ENV"}
	svcHead := []string{"SERVICE", "STATE", "PORTS"}
	var repos, svcs [][]string
	var repoIdx, svcIdx []int
	for i, r := range m.rows {
		if r.Repo {
			repos, repoIdx = append(repos, m.repoCells(r)), append(repoIdx, i)
		} else {
			svcs, svcIdx = append(svcs, serviceCells(r)), append(svcIdx, i)
		}
	}
	// Both tables share the name and state columns, so they line up.
	widths := columnWidths(append([][]string{repoHead, svcHead}, append(repos, svcs...)...))
	var b strings.Builder
	write := func(head []string, rows [][]string, idx []int) {
		if len(rows) == 0 {
			return
		}
		b.WriteString("    " + headerStyle.Render(formatRow(head, widths)) + "\n")
		for j, cells := range rows {
			r := m.rows[idx[j]]
			mark := "  "
			if m.selected[r.Name] {
				mark = "✓ "
			}
			cells = append([]string(nil), cells...)
			line := formatRow(cells, widths)
			// Color the state after padding, so escapes don't skew columns.
			state := cells[1]
			line = strings.Replace(line, state, stateStyle(r).Render(state), 1)
			if r.Repo {
				// ENV is the last column, so it can be colored in place.
				envs := cells[len(cells)-1]
				line = strings.TrimSuffix(line, envs) + envList(r, " ")
			}
			if idx[j] == m.cursor {
				b.WriteString(cursorStyle.Render("> ") + mark + line + "\n")
			} else {
				b.WriteString("  " + mark + line + "\n")
			}
		}
	}
	write(repoHead, repos, repoIdx)
	if len(repos) > 0 && len(svcs) > 0 {
		b.WriteString("\n")
	}
	write(svcHead, svcs, svcIdx)
	if len(m.volumes) > 0 {
		head := []string{"VOLUME", "STATE", "USED BY"}
		cells := [][]string{head}
		for _, v := range m.volumes {
			cells = append(cells, m.volumeCells(v))
		}
		vw := columnWidths(cells)
		b.WriteString("\n    " + headerStyle.Render(formatRow(head, vw)) + "\n")
		for j, c := range cells[1:] {
			line := formatRow(c, vw)
			line = strings.Replace(line, c[1], volumeStyleFor(c[1])(c[1]), 1)
			if len(m.rows)+j == m.cursor {
				b.WriteString(cursorStyle.Render("> ") + "  " + line + "\n")
			} else {
				b.WriteString("    " + line + "\n")
			}
		}
	}
	return b.String()
}

// jumpSection moves the cursor to the next list in direction dir (1 down,
// -1 up, wrapping around) that has rows, where the cursor last was in it.
func (m *dashboard) jumpSection(dir int) {
	s := m.section(m.cursor)
	for range 3 {
		s = (s + dir + 3) % 3
		if i, seen := m.lastIn[s]; seen && i < m.total() && m.section(i) == s {
			m.cursor = i
			return
		}
		for i := 0; i < m.total(); i++ {
			if m.section(i) == s {
				m.cursor = i
				return
			}
		}
	}
}

func (m *dashboard) repoCells(r orchestrate.ServiceStatus) []string {
	procs := "-"
	if r.Overmind {
		up := 0
		for _, p := range r.Processes {
			if p.Status == "running" {
				up++
			}
		}
		procs = fmt.Sprintf("%d/%d running", up, len(r.Processes))
	}
	branch, commit, date := "-", "-", "-"
	if g := r.Git; g != nil {
		branch, commit = g.Branch, g.Commit
		if g.Dirty {
			commit += "*"
		}
		if !g.Date.IsZero() {
			date = m.formatDate(g.Date)
		}
	}
	name := r.Name
	if len(r.MCP) > 0 {
		name += " (mcp)"
	}
	return []string{name, stateLabel(r), procs, branch, commit, date, plainEnvList(r)}
}

// plainEnvList lists a repo's envs with the active one in brackets, such as
// "[local] staging production".
func plainEnvList(r orchestrate.ServiceStatus) string {
	var out []string
	for _, name := range envNames(r) {
		if name == r.Env {
			name = "[" + name + "]"
		}
		out = append(out, name)
	}
	return strings.Join(out, " ")
}

// envList is plainEnvList with the active env highlighted and the others
// faint.
func envList(r orchestrate.ServiceStatus, sep string) string {
	var out []string
	for _, name := range envNames(r) {
		if name == r.Env {
			out = append(out, okStyle.Render("["+name+"]"))
		} else {
			out = append(out, labelStyle.Render(name))
		}
	}
	return strings.Join(out, sep)
}

func envNames(r orchestrate.ServiceStatus) []string {
	if len(r.Envs) == 0 && r.Env != "" {
		return []string{r.Env}
	}
	return r.Envs
}

// envView is the env chooser, in place of the details.
func (m *dashboard) envView() string {
	c := m.envs
	var b strings.Builder
	b.WriteString(nameStyle.Render("Switch "+c.row.Name+"'s env") + labelStyle.Render(" (now "+c.row.Env+")") + "\n")
	for i, name := range c.row.Envs {
		line := name
		if name == c.row.Env {
			line += labelStyle.Render("  current")
		}
		if i == c.cursor {
			b.WriteString(cursorStyle.Render("> "+name) + strings.TrimPrefix(line, name) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	switch {
	case !c.row.Running():
		b.WriteString(labelStyle.Render(c.row.Name+" is stopped; the env applies the next time it starts.") + "\n")
	case c.row.RunMode == "host":
		b.WriteString(labelStyle.Render("Switching restarts "+c.row.Name+"'s processes.") + "\n")
	default:
		b.WriteString(labelStyle.Render("Switching recreates "+c.row.Name+"'s container and restarts its processes.") + "\n")
	}
	return b.String()
}

func serviceCells(r orchestrate.ServiceStatus) []string {
	ports := strings.Join(r.Ports, ", ")
	if ports == "" {
		ports = "-"
	}
	return []string{r.Name, stateLabel(r), ports}
}

func stateLabel(r orchestrate.ServiceStatus) string {
	dot := "○"
	if r.Running() {
		dot = "●"
	}
	return dot + " " + r.State()
}

func stateStyle(r orchestrate.ServiceStatus) lipgloss.Style {
	switch r.State() {
	case "running":
		return okStyle
	case "stopped":
		return headerStyle
	case "partial", "starting", "created", "restarting", "paused":
		return warnStyle
	}
	return errStyle
}

func columnWidths(rows [][]string) []int {
	var w []int
	for _, row := range rows {
		for i, c := range row {
			if i >= len(w) {
				w = append(w, 0)
			}
			if n := lipgloss.Width(c); n > w[i] {
				w[i] = n
			}
		}
	}
	return w
}

func formatRow(cells []string, widths []int) string {
	var b strings.Builder
	for i, c := range cells {
		if i == len(cells)-1 {
			b.WriteString(c)
			break
		}
		b.WriteString(c + strings.Repeat(" ", widths[i]-lipgloss.Width(c)+2))
	}
	return b.String()
}

// details describes the row under the cursor.
func (m *dashboard) details(r orchestrate.ServiceStatus) string {
	var lines []string
	add := func(label, value string) {
		lines = append(lines, labelStyle.Render(fmt.Sprintf("%-11s", label))+value)
	}
	lines = append(lines, nameStyle.Render(r.Name))
	if r.Repo {
		mode := "in its container"
		if r.RunMode == "host" {
			mode = "on the host"
		}
		add("Runs", mode)
		add("Env", envList(r, " · ")+labelStyle.Render("  (e to switch)"))
		if g := r.Git; g != nil {
			commit := fmt.Sprintf("%s on %s · %s", g.Commit, g.Branch, g.Subject)
			add("Commit", commit)
			when := g.Author
			if !g.Date.IsZero() {
				when += ", " + g.Date.Local().Format("Mon Jan 2 2006 15:04") + " (" + relative(m.now().Sub(g.Date)) + ")"
			}
			add("By", when)
			if g.Dirty {
				add("Changes", warnStyle.Render("uncommitted changes in repos/"+r.Name))
			}
		} else {
			add("Commit", "repos/"+r.Name+" is not checked out")
		}
		if len(r.Processes) > 0 {
			var ps []string
			for _, p := range r.Processes {
				st := okStyle
				if p.Status != "running" {
					st = errStyle
				}
				ps = append(ps, p.Name+" "+st.Render(p.Status))
			}
			add("Processes", strings.Join(ps, " · "))
		}
		var win []string
		if r.Window {
			win = append(win, r.Name+" (enter)")
		}
		if r.Claude {
			win = append(win, "claude")
		}
		if len(win) > 0 {
			add("Windows", strings.Join(win, ", "))
		}
	}
	if r.Container != "" {
		c := r.Container
		if r.Health != "" {
			c += " (" + r.Health + ")"
		}
		add("Container", c)
	}
	if len(r.Ports) > 0 && r.Repo {
		add("Ports", strings.Join(r.Ports, ", "))
	}
	for i, l := range r.URLs {
		label := ""
		if i == 0 {
			label = "URL"
		}
		add(label, hyperlink(l))
	}
	for i, srv := range r.MCP {
		label := ""
		if i == 0 {
			label = "MCP"
		}
		url := labelStyle.Render("no Caddyfile site")
		if srv.HostURL != "" {
			url = hyperlink(proxy.Link{URL: srv.HostURL}) + "  " + healthLabel(r, srv.ID())
		}
		add(label, srv.ID()+"  "+url)
		if srv.ContainerURL != "" {
			add("", labelStyle.Render("in containers ")+srv.ContainerURL)
		}
	}
	if len(r.MCP) > 0 {
		add("", labelStyle.Render("m to copy these or add them to Claude Code"))
	}
	if len(r.DependsOn) > 0 {
		add("Depends on", strings.Join(r.DependsOn, ", "))
	}
	if vols := m.volumesFor(r.Name); len(vols) > 0 {
		add("Volumes", strings.Join(vols, ", ")+labelStyle.Render("  (in the Volumes list)"))
	}
	return strings.Join(lines, "\n") + "\n"
}

// healthLabel says whether an MCP server answered the last check.
func healthLabel(r orchestrate.ServiceStatus, id string) string {
	h, ok := r.MCPHealth[id]
	switch {
	case !r.Running():
		return labelStyle.Render("stopped")
	case !ok:
		return labelStyle.Render("checking")
	case h == mcp.Up:
		return okStyle.Render("● answering")
	case h == mcp.NotFound:
		return warnStyle.Render("● nothing at this path")
	}
	return errStyle.Render("● not answering")
}

// hyperlink shows a URL as an OSC 8 link, which terminals that support it
// make clickable and others print as plain text. A wildcard is shown as is,
// since it names many hosts.
func hyperlink(l proxy.Link) string {
	if l.Pattern {
		return l.URL
	}
	return ansi.SetHyperlink(l.URL) + linkStyle.Render(l.URL) + ansi.ResetHyperlink()
}

func (m *dashboard) formatDate(t time.Time) string {
	t = t.Local()
	layout := "Jan 2 15:04"
	if t.Year() != m.now().Year() {
		layout = "Jan 2 2006"
	}
	return t.Format(layout) + " (" + relative(m.now().Sub(t)) + ")"
}

// relative is a short age, such as 5m ago or 3d ago.
func relative(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%dy ago", int(d.Hours()/24/365))
}
