package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ErrCancelled is returned when the user quits the picker without choosing.
var ErrCancelled = errors.New("cancelled")

// PickerOptions configures PickServices.
type PickerOptions struct {
	Title    string
	Repos    []string
	Services []string
	// Preselected names start checked.
	Preselected []string
	// Closure expands a selection with its dependencies, for the preview of
	// what else will start.
	Closure func([]string) ([]string, error)
}

type pickerItem struct {
	name   string
	header bool
}

type picker struct {
	opts     PickerOptions
	items    []pickerItem
	cursor   int
	selected map[string]bool
	done     bool
	err      string

	// descriptions are shown next to item names; allowEmpty lets enter
	// accept no selection; action names what enter does.
	descriptions map[string]string
	allowEmpty   bool
	action       string
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	headerStyle = lipgloss.NewStyle().Faint(true)
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	depsStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	helpStyle   = lipgloss.NewStyle().Faint(true)
)

func newPicker(opts PickerOptions) *picker {
	m := &picker{opts: opts, selected: map[string]bool{}}
	if len(opts.Repos) > 0 {
		m.items = append(m.items, pickerItem{name: "Repos", header: true})
		for _, r := range opts.Repos {
			m.items = append(m.items, pickerItem{name: r})
		}
	}
	if len(opts.Services) > 0 {
		m.items = append(m.items, pickerItem{name: "Services", header: true})
		for _, s := range opts.Services {
			m.items = append(m.items, pickerItem{name: s})
		}
	}
	known := map[string]bool{}
	for _, it := range m.items {
		known[it.name] = !it.header
	}
	for _, name := range opts.Preselected {
		if known[name] {
			m.selected[name] = true
		}
	}
	m.cursor = -1
	m.move(1)
	return m
}

// move steps the cursor by delta, skipping headers.
func (m *picker) move(delta int) {
	for i := m.cursor + delta; i >= 0 && i < len(m.items); i += delta {
		if !m.items[i].header {
			m.cursor = i
			return
		}
	}
}

// Selection returns the checked names in display order.
func (m *picker) Selection() []string {
	var out []string
	for _, it := range m.items {
		if !it.header && m.selected[it.name] {
			out = append(out, it.name)
		}
	}
	return out
}

func (m *picker) Init() tea.Cmd { return nil }

func (m *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	m.err = ""
	switch key.String() {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case " ", "x":
		if m.cursor >= 0 {
			name := m.items[m.cursor].name
			m.selected[name] = !m.selected[name]
		}
	case "a":
		all := len(m.Selection()) < m.count()
		for _, it := range m.items {
			if !it.header {
				m.selected[it.name] = all
			}
		}
	case "enter":
		if len(m.Selection()) == 0 && !m.allowEmpty {
			m.err = "Select at least one service with space."
			return m, nil
		}
		m.done = true
		return m, tea.Quit
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m *picker) count() int {
	n := 0
	for _, it := range m.items {
		if !it.header {
			n++
		}
	}
	return n
}

func (m *picker) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(m.opts.Title) + "\n\n")
	for i, it := range m.items {
		if it.header {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(headerStyle.Render(it.name) + "\n")
			continue
		}
		box := "[ ]"
		if m.selected[it.name] {
			box = "[x]"
		}
		line := fmt.Sprintf("%s %s", box, it.name)
		if d := m.descriptions[it.name]; d != "" {
			line += "  " + helpStyle.Render(d)
		}
		if i == m.cursor {
			b.WriteString(cursorStyle.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}

	if extra := m.dependencies(); extra != "" {
		b.WriteString("\n" + depsStyle.Render(extra) + "\n")
	}
	if m.err != "" {
		b.WriteString("\n" + errStyle.Render(m.err) + "\n")
	}
	action := m.action
	if action == "" {
		action = "start"
	}
	b.WriteString("\n" + helpStyle.Render("space select · a all · enter "+action+" · q cancel") + "\n")
	return b.String()
}

// dependencies describes the services that start because something selected
// depends on them.
func (m *picker) dependencies() string {
	sel := m.Selection()
	if len(sel) == 0 || m.opts.Closure == nil {
		return ""
	}
	all, err := m.opts.Closure(sel)
	if err != nil {
		return err.Error()
	}
	var extra []string
	for _, name := range all {
		if !m.selected[name] {
			extra = append(extra, name)
		}
	}
	if len(extra) == 0 {
		return ""
	}
	return "Also starts dependencies: " + strings.Join(extra, ", ")
}

// PickOptions shows a checklist of a stack's options, with descriptions,
// and returns the chosen names, which may be none, or ErrCancelled.
func PickOptions(title string, names []string, descriptions map[string]string, preselected []string) ([]string, error) {
	m := newPicker(PickerOptions{Title: title, Repos: names, Preselected: preselected})
	m.items[0].name = "Options"
	m.descriptions, m.allowEmpty, m.action = descriptions, true, "add"
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return nil, err
	}
	if !m.done {
		return nil, ErrCancelled
	}
	return m.Selection(), nil
}

// PickServices shows a checklist of repos and services and returns the
// chosen names, or ErrCancelled.
func PickServices(opts PickerOptions) ([]string, error) {
	m := newPicker(opts)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return nil, err
	}
	if !m.done {
		return nil, ErrCancelled
	}
	return m.Selection(), nil
}
