package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/procfile"
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
}

type (
	statusMsg []orchestrate.ServiceStatus
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
	message  string
	now      func() time.Time
	// envs is the open env chooser, or nil.
	envs *envChooser
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
)

func newDashboard(opts DashboardOptions) *dashboard {
	if opts.Refresh == 0 {
		opts.Refresh = 2 * time.Second
	}
	return &dashboard{opts: opts, selected: map[string]bool{}, now: time.Now}
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
	return func() tea.Msg { return statusMsg(status()) }
}

func (m *dashboard) tick() tea.Cmd {
	return tea.Tick(m.opts.Refresh, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		first := !m.loaded
		m.rows, m.loaded, m.loading, m.updated = msg, true, false, m.now()
		if m.cursor >= len(m.rows) {
			m.cursor = len(m.rows) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		if first {
			return m, m.tick()
		}
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.refresh(), m.tick())
	case doneMsg:
		if msg.err != nil {
			m.message = warnStyle.Render(fmt.Sprintf("%s failed: %v", msg.action, msg.err))
		} else {
			m.message = msg.action + " done"
		}
		return m, m.refresh()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyMsg:
		return m.key(msg.String())
	}
	return m, nil
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
	row, ok := m.current()
	switch key {
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
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)-1 {
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
			return m, m.run("up "+strings.Join(names, " "), append([]string{"up", "--detach"}, names...)...)
		}
	case "s":
		if names := m.targets(); len(names) > 0 {
			m.selected = map[string]bool{}
			return m, m.run("down "+strings.Join(names, " "), append([]string{"down"}, names...)...)
		}
	case "r":
		switch {
		case !ok:
		case !row.Repo:
			m.message = "restart works on repos; press s then u to restart a service"
		case !row.Overmind:
			m.message = row.Name + " is not running; press u to start it"
		default:
			return m, m.run("restart "+row.Name, "restart", row.Name)
		}
	case "c":
		switch {
		case !ok:
		case !row.Repo || row.RunMode != "container":
			m.message = "Claude Code runs in a repo's container"
		default:
			return m, m.run("claude "+row.Name, "claude", row.Name)
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
		return m.runAll(fmt.Sprintf("switch %s to %s", repo, config), switchEnvCommands(c.row, config)...)
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

// run runs a neptuno command with the terminal, keeping its output on
// screen when it fails.
func (m *dashboard) run(action string, args ...string) tea.Cmd {
	return m.runAll(action, args)
}

// runAll runs neptuno commands one after another, stopping at the first
// that fails.
func (m *dashboard) runAll(action string, cmds ...[]string) tea.Cmd {
	var commands []string
	for _, args := range cmds {
		command := procfile.Quote(m.opts.Neptuno)
		for _, a := range args {
			command += " " + procfile.Quote(a)
		}
		commands = append(commands, command)
	}
	script := strings.Join(commands, " && ") + ` || { status=$?; echo; echo "Press enter to return to the dashboard."; read _; exit $status; }`
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
	} else if r, ok := m.current(); ok {
		b.WriteString("\n" + m.details(r))
	}
	if m.message != "" {
		b.WriteString("\n" + m.message + "\n")
	}
	help := "↑↓ move · space select · enter open window · u up · s stop · r restart · e env · c claude · f refresh · q quit"
	if m.envs != nil {
		help = "↑↓ move · enter switch · esc cancel"
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
	return b.String()
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
	return []string{r.Name, stateLabel(r), procs, branch, commit, date, plainEnvList(r)}
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
			win = append(win, orchestrate.ClaudeWindow(r.Name))
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
	if len(r.DependsOn) > 0 {
		add("Depends on", strings.Join(r.DependsOn, ", "))
	}
	return strings.Join(lines, "\n") + "\n"
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
