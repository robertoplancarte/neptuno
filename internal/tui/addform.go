package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robertoplancarte/neptuno/internal/scaffold"
	"github.com/robertoplancarte/neptuno/internal/stacks"
)

// addTail is how many lines of `neptuno add`'s output the form shows.
const addTail = 12

// addForm is the dashboard's form for `neptuno add`: a repo's name, an
// optional git URL to clone, its stack and the stack's options.
type addForm struct {
	name, url string
	list      []*stacks.Stack
	stack     int
	// on holds the chosen options of the stack shown, reset to its defaults
	// when the stack changes.
	on       map[string]bool
	wildcard bool
	// row is the field the cursor is on: see the form* constants, then one
	// row per option, the wildcard toggle and the add button.
	row int
	err string
	// added is the repo being added once the form is sent, which then shows
	// the command's output until it succeeds.
	added string
}

// The form's first rows; the stack's options follow.
const (
	formName = iota
	formURL
	formStack
	formOptions
)

// newAddForm opens the form with the default stack chosen.
func newAddForm(list []*stacks.Stack, err error) *addForm {
	f := &addForm{list: list}
	if err != nil {
		f.err = err.Error()
	}
	for i, s := range list {
		if s.Name == stacks.Default {
			f.stack = i
		}
	}
	f.resetOptions()
	return f
}

func (f *addForm) current() *stacks.Stack {
	if f.stack < 0 || f.stack >= len(f.list) {
		return nil
	}
	return f.list[f.stack]
}

func (f *addForm) options() []*stacks.Option {
	if s := f.current(); s != nil {
		return s.Options
	}
	return nil
}

func (f *addForm) resetOptions() {
	f.on = map[string]bool{}
	for _, o := range f.options() {
		f.on[o.Name] = o.Default
	}
}

func (f *addForm) wildcardRow() int { return formOptions + len(f.options()) }
func (f *addForm) submitRow() int   { return f.wildcardRow() + 1 }

// args is the `neptuno add` command line the form stands for.
func (f *addForm) args() []string {
	args := []string{"add", strings.TrimSpace(f.name)}
	if u := strings.TrimSpace(f.url); u != "" {
		args = append(args, u)
	}
	if s := f.current(); s != nil {
		args = append(args, "--stack", s.Name)
	}
	var with, without []string
	for _, o := range f.options() {
		if f.on[o.Name] {
			with = append(with, o.Name)
		} else {
			without = append(without, o.Name)
		}
	}
	if len(with) > 0 {
		args = append(args, "--with", strings.Join(with, ","))
	}
	if len(without) > 0 {
		args = append(args, "--without", strings.Join(without, ","))
	}
	if f.wildcard {
		args = append(args, "--wildcard")
	}
	return args
}

// openAddForm shows the add-repo form, listing the stacks.
func (m *dashboard) openAddForm() {
	var list []*stacks.Stack
	var err error
	if m.opts.Stacks != nil {
		list, err = m.opts.Stacks()
	}
	m.form = newAddForm(list, err)
}

// formKey handles a key while the add-repo form is open. Text goes into the
// name and URL fields, so only arrows, tab and enter move between fields.
func (m *dashboard) formKey(msg tea.KeyMsg) tea.Cmd {
	f := m.form
	key := msg.String()
	if key == "ctrl+c" {
		return tea.Quit
	}
	if f.added != "" {
		// Sent: the output shows until it succeeds; esc hides it, and the
		// command carries on.
		if key == "esc" || key == "q" || key == "n" {
			m.form = nil
		}
		return nil
	}
	f.err = ""
	text := map[int]*string{formName: &f.name, formURL: &f.url}[f.row]
	switch key {
	case "esc":
		m.form = nil
		return nil
	case "up", "shift+tab":
		f.row = max(0, f.row-1)
		return nil
	case "down", "tab":
		f.row = min(f.submitRow(), f.row+1)
		return nil
	case "ctrl+s":
		return m.submitAdd()
	}
	switch {
	case text != nil:
		switch {
		case key == "enter":
			f.row++
		case key == "backspace" && *text != "":
			r := []rune(*text)
			*text = string(r[:len(r)-1])
		case key == "ctrl+u":
			*text = ""
		case msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace:
			*text += strings.TrimSpace(string(msg.Runes))
		}
	case f.row == formStack:
		n := len(f.list)
		switch key {
		case "left", "h":
			if n > 0 {
				f.stack = (f.stack + n - 1) % n
				f.resetOptions()
			}
		case "right", "l", " ":
			if n > 0 {
				f.stack = (f.stack + 1) % n
				f.resetOptions()
			}
		case "enter":
			f.row++
		}
	case f.row < f.wildcardRow():
		o := f.options()[f.row-formOptions]
		switch key {
		case " ", "x", "enter":
			f.on[o.Name] = !f.on[o.Name]
		case "a":
			all := false
			for _, o := range f.options() {
				all = all || !f.on[o.Name]
			}
			for _, o := range f.options() {
				f.on[o.Name] = all
			}
		}
	case f.row == f.wildcardRow():
		if key == " " || key == "x" || key == "enter" {
			f.wildcard = !f.wildcard
		}
	case key == "enter":
		return m.submitAdd()
	}
	return nil
}

// submitAdd checks the form and runs `neptuno add` in the background.
func (m *dashboard) submitAdd() tea.Cmd {
	f := m.form
	name := strings.TrimSpace(f.name)
	switch {
	case name == "":
		f.err, f.row = "Give the repo a name.", formName
		return nil
	case scaffold.ValidateName(name) != nil:
		f.err, f.row = scaffold.ValidateName(name).Error(), formName
		return nil
	case f.current() == nil:
		f.err = "No stacks to choose from."
		return nil
	}
	for _, r := range m.rows {
		if r.Name == name && !r.Repo {
			f.err, f.row = "compose.yml already has a "+name+" service that is not a repo; pick another name.", formName
			return nil
		}
	}
	cmd := m.run("add "+name, []string{name}, f.args()...)
	if cmd != nil {
		f.added = name
	}
	return cmd
}

// addDone closes the form once its repo is added and puts the cursor on the
// new repo, whose Logs tab shows the whole output.
func (m *dashboard) addDone(a *action) bool {
	f := m.form
	if f == nil || f.added == "" || m.actions[f.added] != a || a.err != nil {
		return false
	}
	m.form, m.focus, m.tab, m.scroll = nil, f.added, tabLogs, 0
	m.focusRow()
	return true
}

// focusRow moves the cursor to the repo just added, once the status lists it.
func (m *dashboard) focusRow() {
	for i, r := range m.rows {
		if r.Name == m.focus && m.focus != "" {
			m.cursor, m.focus = i, ""
			return
		}
	}
}

// formView is the add-repo form, in place of the right panel's tab.
func (m *dashboard) formView() string {
	f := m.form
	var lines []string
	if f.added != "" {
		lines = append(lines, nameStyle.Render("Adding "+f.added))
		text := strings.Split(m.actionText(f.added), "\n")
		if len(text) > addTail+1 {
			text = append(text[:1], text[len(text)-addTail:]...)
		}
		lines = append(lines, text...)
		lines = append(lines, "", labelStyle.Render("esc hides this; the command keeps running and its output stays in "+f.added+"'s Logs."))
		return strings.Join(lines, "\n")
	}
	cursor := func(row int) string {
		if f.row == row {
			return cursorStyle.Render("> ")
		}
		return "  "
	}
	field := func(row int, label, value, hint string) {
		v := value
		if f.row == row {
			v += cursorStyle.Render("▏")
		}
		if value == "" && hint != "" {
			v += labelStyle.Render(hint)
		}
		lines = append(lines, cursor(row)+labelStyle.Render(fmt.Sprintf("%-9s", label))+v)
	}
	lines = append(lines, nameStyle.Render("Add a repo"), "")
	field(formName, "Name", f.name, "folder and service name, such as api")
	field(formURL, "Git URL", f.url, "optional: clone it; blank starts a new app")
	stack := labelStyle.Render("none found")
	if s := f.current(); s != nil {
		stack = "◂ " + nameStyle.Render(s.Name) + " ▸" + labelStyle.Render(fmt.Sprintf("  %d of %d", f.stack+1, len(f.list)))
	}
	lines = append(lines, cursor(formStack)+labelStyle.Render(fmt.Sprintf("%-9s", "Stack"))+stack)
	if s := f.current(); s != nil && s.Description != "" {
		lines = append(lines, "           "+labelStyle.Render(s.Description))
	}
	lines = append(lines, "")
	if opts := f.options(); len(opts) > 0 {
		lines = append(lines, "  "+labelStyle.Render("Options"))
		width := 0
		for _, o := range opts {
			width = max(width, len(o.Name))
		}
		for i, o := range opts {
			box := "[ ]"
			if f.on[o.Name] {
				box = "[x]"
			}
			lines = append(lines, cursor(formOptions+i)+box+" "+o.Name+strings.Repeat(" ", width-len(o.Name))+"  "+labelStyle.Render(o.Description))
		}
	} else if f.current() != nil {
		lines = append(lines, "  "+labelStyle.Render("The "+f.current().Name+" stack has no options."))
	}
	box := "[ ]"
	if f.wildcard {
		box = "[x]"
	}
	lines = append(lines, "", cursor(f.wildcardRow())+box+" wildcard"+labelStyle.Render("  also route *.<repo>.localhost to it"))
	lines = append(lines, "")
	button := "[ Add repo ]"
	if f.row == f.submitRow() {
		button = selectedRow.Render(button)
	}
	lines = append(lines, cursor(f.submitRow())+button)
	if f.err != "" {
		lines = append(lines, "", errStyle.Render(f.err))
	}
	lines = append(lines, "", labelStyle.Render("Runs: neptuno "+strings.Join(f.args(), " ")))
	return strings.Join(lines, "\n")
}
