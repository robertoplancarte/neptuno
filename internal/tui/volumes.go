package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

// volumeTail is how many lines of a clear's output the volume panel shows.
const volumeTail = 8

// The left-hand lists, in the order tab cycles through them.
const (
	sectionRepos = iota
	sectionServices
	sectionVolumes
)

// volumesMsg carries the project's volumes, once loaded.
type volumesMsg struct {
	list []orchestrate.Volume
	err  error
}

// loadVolumes lists the volumes off the UI goroutine.
func (m *dashboard) loadVolumes() tea.Cmd {
	list := m.opts.Volumes
	if list == nil {
		return nil
	}
	return func() tea.Msg {
		vols, err := list()
		return volumesMsg{vols, err}
	}
}

// total counts the rows the cursor moves over: the repos and services, then
// the volumes.
func (m *dashboard) total() int { return len(m.rows) + len(m.volumes) }

// currentVolume is the volume under the cursor, if it is on one.
func (m *dashboard) currentVolume() (orchestrate.Volume, bool) {
	i := m.cursor - len(m.rows)
	if i < 0 || i >= len(m.volumes) {
		return orchestrate.Volume{}, false
	}
	return m.volumes[i], true
}

// section says which left-hand list row i is in.
func (m *dashboard) section(i int) int {
	switch {
	case i >= len(m.rows):
		return sectionVolumes
	case m.rows[i].Repo:
		return sectionRepos
	}
	return sectionServices
}

// volumeAction is the key of a volume's last clear in m.actions, next to
// the services it stops.
func volumeAction(name string) string { return "volume:" + name }

// volumesFor lists the volumes a service mounts.
func (m *dashboard) volumesFor(service string) []string {
	var names []string
	for _, v := range m.volumes {
		if slices.Contains(v.Services, service) {
			names = append(names, v.Name)
		}
	}
	return names
}

// volumeKey handles the keys that act on the volume under the cursor. It
// reports whether it handled key.
func (m *dashboard) volumeKey(v orchestrate.Volume, key string) (tea.Cmd, bool) {
	if m.confirm != "" {
		name := m.confirm
		m.confirm = ""
		if key == "ctrl+c" {
			return tea.Quit, true
		}
		if key != "y" || name != v.Name {
			m.message = "nothing deleted"
			return nil, true
		}
		targets := append(append([]string(nil), v.Services...), volumeAction(v.Name))
		return m.run("clear volume "+v.Name, targets, "volumes", "reset", "--yes", v.Name), true
	}
	switch key {
	case "enter", "d":
		if v.External {
			m.message = v.Name + " is external: compose.yml doesn't own it, so neptuno won't delete it"
		} else {
			m.confirm = v.Name
		}
		return nil, true
	case "f":
		// Look inside it again too.
		m.panel = panelMsg{}
		return tea.Batch(m.refresh(), m.fetchPanel()), true
	case "u", "s", "r", "e", "c", "m", " ", "x":
		m.message = "that key works on repos and services; enter clears a volume"
		return nil, true
	}
	return nil, false
}

// volumeTitle is the right panel's title for a volume: its panel and the
// Actions tab, which [ and ] switch between.
func (m *dashboard) volumeTitle(v orchestrate.Volume) string {
	if m.tab == tabActions && m.confirm == "" {
		return labelStyle.Render("Volume "+v.Name+" │ ") + tabActive.Render("Actions")
	}
	return tabActive.Render("Volume") + labelStyle.Render(" "+v.Name+" │ Actions")
}

// volumePanel describes the volume under the cursor: what it holds, who
// mounts it, docker's view of it and what is inside, and how to clear it.
// Its description wraps at width.
func (m *dashboard) volumePanel(v orchestrate.Volume, width int) string {
	var lines []string
	add := func(label, value string) {
		lines = append(lines, labelStyle.Render(fmt.Sprintf("%-11s", label))+value)
	}
	lines = append(lines, nameStyle.Render(v.Name), ansi.Wordwrap(describeVolume(v), max(20, width), ""), "")
	add("Docker", v.Docker)
	state := "not created yet: docker makes it when a service that mounts it starts"
	switch {
	case v.External:
		state = "external: created outside compose.yml, so neptuno leaves it alone"
	case v.Exists:
		state = okStyle.Render("created")
	}
	add("State", state)
	if len(v.Mounts) == 0 {
		add("Used by", labelStyle.Render("nothing in compose.yml mounts it"))
	}
	for i, mt := range v.Mounts {
		label := ""
		if i == 0 {
			label = "Used by"
		}
		svc := mt.Service
		for _, r := range m.rows {
			if r.Name == mt.Service {
				svc += " " + stateStyle(r).Render(stateLabel(r))
			}
		}
		add(label, svc+labelStyle.Render(" at ")+mt.Target)
	}
	switch {
	case !v.Exists:
	case m.panel.key != m.panelKey():
		add("Size", labelStyle.Render("measuring…"))
	default:
		lines = append(lines, m.panel.text)
	}

	lines = append(lines, "")
	if a := m.actions[volumeAction(v.Name)]; a != nil {
		text := strings.Split(m.actionText(volumeAction(v.Name)), "\n")
		head, tail := text[0], text[1:]
		if len(tail) > volumeTail {
			tail = tail[len(tail)-volumeTail:]
		}
		lines = append(lines, strings.Replace(head, "neptuno ", "", 1))
		lines = append(lines, tail...)
		lines = append(lines, "")
	}
	switch {
	case v.External:
	case m.confirm == v.Name:
		lines = append(lines, warnStyle.Render("Delete "+v.Docker+"? Its data is gone for good."), labelStyle.Render("This will:"))
		lines = append(lines, m.clearPlan(v)...)
		lines = append(lines, "", nameStyle.Render("Press y to delete it")+labelStyle.Render(", any other key to cancel."))
	default:
		lines = append(lines, keyStyle.Render("enter")+" clears it, deleting its data:")
		lines = append(lines, m.clearPlan(v)...)
	}
	return strings.Join(lines, "\n")
}

// volumeDetailText is the part of the volume panel that has to look inside
// the volume, which runs off the UI goroutine.
func volumeDetailText(v orchestrate.Volume, inspect func(orchestrate.Volume) (orchestrate.VolumeDetail, error)) string {
	if inspect == nil {
		return ""
	}
	d, err := inspect(v)
	var lines []string
	add := func(label, value string) {
		lines = append(lines, labelStyle.Render(fmt.Sprintf("%-11s", label))+value)
	}
	if d.Driver != "" {
		add("Driver", d.Driver)
	}
	if !d.Created.IsZero() {
		add("Created", d.Created.Local().Format("Mon Jan 2 2006 15:04"))
	}
	if d.Mountpoint != "" {
		add("Host path", d.Mountpoint+labelStyle.Render(" (inside Docker's VM on a Mac)"))
	}
	if err != nil {
		add("Inside", errStyle.Render(err.Error()))
		return strings.Join(lines, "\n")
	}
	size := d.Size
	if size == "" {
		size = "unknown"
	}
	add("Size", nameStyle.Render(size))
	if len(d.Entries) == 0 {
		add("Inside", labelStyle.Render("empty"))
	}
	for i, e := range d.Entries {
		label := ""
		if i == 0 {
			label = "Inside"
		}
		add(label, e)
	}
	if d.More > 0 {
		add("", labelStyle.Render(fmt.Sprintf("and %d more", d.More)))
	}
	if d.Via != "" {
		add("", labelStyle.Render("looked "+d.Via))
	}
	return strings.Join(lines, "\n")
}

// describeVolume says in a sentence what a volume holds, from where it is
// mounted.
func describeVolume(v orchestrate.Volume) string {
	for _, mt := range v.Mounts {
		svc, t := strings.ToLower(mt.Service), mt.Target
		switch {
		case strings.Contains(t, "postgresql") || strings.Contains(svc, "postgres"):
			return "PostgreSQL's data directory: every database on the " + mt.Service + " server. Clearing it starts postgres over with no databases; apps recreate theirs on their next start (db:prepare)."
		case strings.Contains(t, "/var/lib/mysql"):
			return "MySQL's data directory: every database on the " + mt.Service + " server."
		case t == "/usr/local/bundle" || strings.HasSuffix(t, "/bundle"):
			return "Installed Ruby gems for " + mt.Service + ". Clearing it makes the next start reinstall them from Gemfile.lock."
		case strings.HasSuffix(t, "node_modules"):
			return "Installed npm packages for " + mt.Service + ". Clearing it makes the next start reinstall them."
		case strings.Contains(svc, "valkey") || strings.Contains(svc, "redis"):
			return mt.Service + "'s saved keys, which it reloads on start. Clearing it starts it with no keys."
		case strings.Contains(svc, "s3") || strings.Contains(svc, "seaweed") || strings.Contains(svc, "minio"):
			return "The files in " + mt.Service + "'s S3 buckets, and the buckets themselves."
		case strings.Contains(svc, "caddy") || strings.Contains(svc, "proxy"):
			return "The proxy's certificates and state."
		}
	}
	if len(v.Mounts) > 0 {
		return "Data " + v.Mounts[0].Service + " keeps at " + v.Mounts[0].Target + "."
	}
	return "A volume compose.yml declares that no service mounts."
}

// clearPlan lists the steps of clearing v.
func (m *dashboard) clearPlan(v orchestrate.Volume) []string {
	var running []string
	for _, r := range m.rows {
		if slices.Contains(v.Services, r.Name) && r.Container == "running" {
			running = append(running, r.Name)
		}
	}
	var steps []string
	if len(v.Services) > 0 {
		their := "their containers"
		if len(v.Services) == 1 {
			their = "its container"
		}
		steps = append(steps, "stop "+strings.Join(v.Services, ", ")+" and remove "+their)
	}
	steps = append(steps, "delete "+v.Docker)
	switch {
	case len(v.Services) == 0:
	case len(running) == 0:
		steps = append(steps, "nothing to start again: it is recreated empty when "+strings.Join(v.Services, " or ")+" next starts")
	default:
		steps = append(steps, "start "+strings.Join(running, ", ")+" again, which recreates it empty")
	}
	for i, s := range steps {
		steps[i] = fmt.Sprintf("  %d. %s", i+1, s)
	}
	return steps
}

// volumeCells is a volume's columns in the left panel.
func (m *dashboard) volumeCells(v orchestrate.Volume) []string {
	users := strings.Join(v.Services, ", ")
	if users == "" {
		users = "unused"
	}
	state := "○ not created"
	switch {
	case m.actions[volumeAction(v.Name)] != nil && m.actions[volumeAction(v.Name)].running:
		state = "◌ clearing"
	case v.External:
		state = "● external"
	case v.Exists:
		state = "● created"
	}
	return []string{v.Name, state, users}
}

// volumeStyleFor colors a volume's state.
func volumeStyleFor(state string) func(...string) string {
	switch {
	case strings.Contains(state, "created") && !strings.Contains(state, "not"):
		return okStyle.Render
	case strings.Contains(state, "clearing"):
		return warnStyle.Render
	}
	return headerStyle.Render
}
