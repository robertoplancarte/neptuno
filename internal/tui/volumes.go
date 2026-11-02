package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

// volumeTail is how many lines of a reset's output the volumes view shows.
const volumeTail = 8

// volumesMsg carries the project's volumes, once loaded.
type volumesMsg struct {
	list []orchestrate.Volume
	err  error
}

// volumeView lists the project's volumes and resets one. confirm is set
// while it asks whether to delete the one under the cursor.
type volumeView struct {
	cursor  int
	confirm bool
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

// volumeAction is the key of a volume's last reset in m.actions, next to
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

// volumeKey handles a key while the volumes view is open.
func (m *dashboard) volumeKey(key string) tea.Cmd {
	c := m.vols
	if key == "ctrl+c" {
		return tea.Quit
	}
	if c.confirm {
		c.confirm = false
		if key != "y" || c.cursor >= len(m.volumes) {
			m.message = "nothing deleted"
			return nil
		}
		v := m.volumes[c.cursor]
		targets := append(append([]string(nil), v.Services...), volumeAction(v.Name))
		return m.run("reset volume "+v.Name, targets, "volumes", "reset", "--yes", v.Name)
	}
	switch key {
	case "esc", "q", "v":
		m.vols = nil
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down", "j":
		if c.cursor < len(m.volumes)-1 {
			c.cursor++
		}
	case "f":
		return m.loadVolumes()
	case "enter", "d":
		switch {
		case c.cursor >= len(m.volumes):
		case m.volumes[c.cursor].External:
			m.message = m.volumes[c.cursor].Name + " is external: compose.yml doesn't own it, so neptuno won't delete it"
		default:
			c.confirm = true
		}
	}
	return nil
}

// volumeView lists the volumes, or asks to confirm a reset.
func (m *dashboard) volumeView() string {
	c := m.vols
	var b strings.Builder
	b.WriteString(nameStyle.Render("Volumes") + labelStyle.Render("  named volumes in compose.yml") + "\n\n")
	switch {
	case m.volumesErr != nil:
		b.WriteString(errStyle.Render(m.volumesErr.Error()) + "\n")
		return b.String()
	case !m.volumesLoaded:
		b.WriteString(labelStyle.Render("Loading…") + "\n")
		return b.String()
	case len(m.volumes) == 0:
		b.WriteString("compose.yml declares no named volumes.\n")
		return b.String()
	}
	rows := make([][]string, len(m.volumes))
	for i, v := range m.volumes {
		users := strings.Join(v.Services, ", ")
		if users == "" {
			users = "unused"
		}
		state := "not created"
		if v.Exists {
			state = "created"
		}
		if v.External {
			state = "external"
		}
		if a := m.actions[volumeAction(v.Name)]; a != nil && a.running {
			state = "resetting…"
		}
		rows[i] = []string{v.Name, users, state}
	}
	widths := columnWidths(append([][]string{{"VOLUME", "USED BY", "STATE"}}, rows...))
	b.WriteString("  " + headerStyle.Render(formatRow([]string{"VOLUME", "USED BY", "STATE"}, widths)) + "\n")
	for i, cells := range rows {
		line := formatRow(cells, widths)
		if i == c.cursor {
			b.WriteString(cursorStyle.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	if c.cursor >= len(m.volumes) {
		return b.String()
	}
	v := m.volumes[c.cursor]
	b.WriteString("\n")
	if c.confirm {
		b.WriteString(warnStyle.Render("Delete "+v.Docker+"? Its data is gone for good.") + "\n")
		b.WriteString(labelStyle.Render("This will:") + "\n" + m.resetPlan(v) + "\n\n")
		b.WriteString(nameStyle.Render("Press y to delete it") + labelStyle.Render(", any other key to cancel.") + "\n")
		return b.String()
	}
	b.WriteString(labelStyle.Render("Enter resets "+v.Name+", deleting its data:") + "\n" + m.resetPlan(v) + "\n")
	if a := m.actions[volumeAction(v.Name)]; a != nil {
		lines := strings.Split(m.actionText(volumeAction(v.Name)), "\n")
		head, tail := lines[0], lines[1:]
		if len(tail) > volumeTail {
			tail = tail[len(tail)-volumeTail:]
		}
		b.WriteString("\n" + strings.Replace(head, "neptuno ", "", 1) + "\n" + strings.Join(tail, "\n") + "\n")
	}
	return b.String()
}

// resetPlan lists the steps of resetting v, one per line.
func (m *dashboard) resetPlan(v orchestrate.Volume) string {
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
	return strings.Join(steps, "\n")
}
