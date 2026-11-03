package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func press(m *picker, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func TestPicker(t *testing.T) {
	m := newPicker(PickerOptions{
		Title:       "Start?",
		Repos:       []string{"api", "web"},
		Services:    []string{"postgres", "valkey"},
		Preselected: []string{"web", "gone"},
		Closure: func(sel []string) ([]string, error) {
			return append([]string{"postgres"}, sel...), nil
		},
	})

	// Cursor starts on api, skipping the header.
	press(m, " ")
	if got := m.Selection(); !reflect.DeepEqual(got, []string{"api", "web"}) {
		t.Fatalf("Selection = %v", got)
	}
	if !strings.Contains(m.View(), "Also starts dependencies: postgres") {
		t.Errorf("View does not preview dependencies:\n%s", m.View())
	}

	// Moving down past web crosses the Services header onto postgres.
	press(m, "down", "down", " ")
	if got := m.Selection(); !reflect.DeepEqual(got, []string{"api", "web", "postgres"}) {
		t.Fatalf("Selection = %v", got)
	}

	press(m, "a")
	if got := m.Selection(); len(got) != 4 {
		t.Fatalf("a did not select all: %v", got)
	}
	press(m, "a")
	if got := m.Selection(); len(got) != 0 {
		t.Fatalf("second a did not clear: %v", got)
	}

	press(m, "enter")
	if m.done {
		t.Fatal("enter with nothing selected finished the picker")
	}
	press(m, " ", "enter")
	if !m.done {
		t.Fatal("enter did not finish the picker")
	}
}

func TestChecklist(t *testing.T) {
	m := newChecklist(ChecklistOptions{
		Title:        "Services",
		Header:       "Services",
		Names:        []string{"postgres", "valkey", "svix"},
		Descriptions: map[string]string{"svix": "webhooks"},
		Preselected:  []string{"postgres"},
		Action:       "create",
		Closure: func(sel []string) ([]string, error) {
			for _, s := range sel {
				if s == "svix" {
					return append(sel, "valkey"), nil
				}
			}
			return sel, nil
		},
		DepsLabel: "Also adds: ",
	})
	press(m, "down", "down", " ")
	view := m.View()
	for _, want := range []string{"Services", "webhooks", "Also adds: valkey", "enter create"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	if got := m.Selection(); !reflect.DeepEqual(got, []string{"postgres", "svix"}) {
		t.Errorf("selection = %v", got)
	}
	press(m, "a", "a", "enter")
	if !m.done || len(m.Selection()) != 0 {
		t.Errorf("an empty checklist should be accepted: done=%v selection=%v", m.done, m.Selection())
	}
}
