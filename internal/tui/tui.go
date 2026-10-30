// Package tui is Neptuno's Bubble Tea interface. For now it is a placeholder
// screen; the dashboard, logs, attach and env screens come later.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type model struct{}

func (model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (model) View() string {
	return "Neptuno\n\nThe dashboard is not built yet. Run `neptuno --help` for commands.\n\nPress q to quit.\n"
}

// Run starts the TUI and blocks until the user quits.
func Run() error {
	_, err := tea.NewProgram(model{}).Run()
	return err
}
