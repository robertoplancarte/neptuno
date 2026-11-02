package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// menuAction is one entry of the Actions tab: a key the dashboard already
// answers to, what it does to the item under the cursor, and, when it can't
// do it now, why.
type menuAction struct {
	key, label string
	// note says why the action can't run now; empty means it can.
	note string
}

// availableActions lists what the keys do for the item under the cursor, in
// the order the Actions tab shows them.
func (m *dashboard) availableActions() []menuAction {
	var list []menuAction
	add := func(key, label, note string) { list = append(list, menuAction{key, label, note}) }

	if v, ok := m.currentVolume(); ok {
		note := ""
		if v.External {
			note = "external: compose.yml doesn't own it"
		}
		add("enter", "Clear "+v.Name+", deleting its data", note)
		add("f", "Refresh and look inside it again", "")
		add("n", "Add a repo", "")
		return list
	}
	r, ok := m.current()
	if !ok {
		add("n", "Add a repo", "")
		add("f", "Refresh", "")
		return list
	}
	targets := strings.Join(m.targets(), ", ")
	if n := len(m.targets()); n > 1 {
		targets = fmt.Sprintf("%d selected (%s)", n, targets)
	}
	if r.Repo {
		note := ""
		if !r.Window {
			note = "no window yet; start it with u"
		}
		add("enter", "Open "+r.Name+"'s window", note)
	}
	add("u", "Start "+targets, "")
	add("s", "Stop "+targets, "")
	if r.Repo {
		note := ""
		if !r.Overmind {
			note = "not running; start it with u"
		}
		add("r", "Restart "+r.Name+"'s processes", note)
		add("e", "Switch "+r.Name+"'s env (now "+r.Env+")", "")
		note = ""
		if r.RunMode != "container" {
			note = "runs on the host; Claude Code runs in a repo's container"
		}
		add("c", "Run Claude Code in "+r.Name+"'s container", note)
		if len(r.MCP) > 0 {
			add("m", "Copy or add "+r.Name+"'s MCP servers", "")
		}
	}
	sel := "Select " + r.Name + " for up and stop"
	if m.selected[r.Name] {
		sel = "Unselect " + r.Name
	}
	add("space", sel, "")
	all := "Select every repo and service"
	if len(m.selected) == len(m.rows) {
		all = "Unselect all"
	}
	add("a", all, "")
	add("n", "Add a repo", "")
	add("f", "Refresh", "")
	return list
}

// actionsKey handles a key while the Actions list has the cursor.
func (m *dashboard) actionsKey(key string) tea.Cmd {
	list := m.availableActions()
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "esc", "q":
		m.actionsFocus = false
	case "up", "k":
		m.actionCursor = max(0, m.actionCursor-1)
	case "down", "j":
		m.actionCursor = min(len(list)-1, m.actionCursor+1)
	case "enter":
		m.actionsFocus = false
		if m.actionCursor >= len(list) {
			return nil
		}
		k := list[m.actionCursor].key
		if k == "space" {
			k = " "
		}
		_, cmd := m.dispatch(k)
		return cmd
	default:
		// Any other key does what it always does.
		m.actionsFocus = false
		_, cmd := m.dispatch(key)
		return cmd
	}
	return nil
}

// actionsView is the Actions tab.
func (m *dashboard) actionsView() string {
	list := m.availableActions()
	m.actionCursor = min(m.actionCursor, max(0, len(list)-1))
	subject := "the project"
	if v, ok := m.currentVolume(); ok {
		subject = v.Name
	} else if r, ok := m.current(); ok {
		subject = r.Name
	}
	lines := []string{nameStyle.Render("Actions for " + subject), ""}
	width := 0
	for _, a := range list {
		width = max(width, len(a.key))
	}
	for i, a := range list {
		mark := "  "
		if m.actionsFocus && i == m.actionCursor {
			mark = cursorStyle.Render("> ")
		}
		k := keyStyle.Render(a.key) + strings.Repeat(" ", width-len(a.key))
		label := a.label
		if a.note != "" {
			label = labelStyle.Render(label + " · " + a.note)
		}
		if m.actionsFocus && i == m.actionCursor {
			label = selectedRow.Render(a.label)
			if a.note != "" {
				label += labelStyle.Render(" · " + a.note)
			}
		}
		lines = append(lines, mark+k+"  "+label)
	}
	lines = append(lines, "")
	if m.actionsFocus {
		lines = append(lines, labelStyle.Render("↑↓ choose · enter run · esc back to the lists"))
	} else {
		lines = append(lines, labelStyle.Render("Press enter to choose from this list, or press an action's key from anywhere."))
	}
	return strings.Join(lines, "\n")
}
