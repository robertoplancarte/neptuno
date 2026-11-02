package tui

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robertoplancarte/neptuno/internal/procfile"
)

// maxActionLines caps how much of an action's output the dashboard keeps.
const maxActionLines = 1000

// action is a neptuno command the dashboard runs in the background, such as
// up, down or restart, and the output it printed so far. The Logs tab of
// each service it acts on shows it.
type action struct {
	id      int
	name    string
	lines   []string
	running bool
	err     error
}

// actionMsg carries an action's new output, and its end once done is set.
type actionMsg struct {
	id    int
	lines []string
	done  bool
	err   error
	next  <-chan actionMsg
}

// run runs a neptuno command in the background for targets.
func (m *dashboard) run(name string, targets []string, args ...string) tea.Cmd {
	return m.runAll(name, targets, args)
}

// runAll runs neptuno commands one after another in the background, stopping
// at the first that fails, and streams their output into the Logs tab of
// each of targets. The dashboard stays up meanwhile.
func (m *dashboard) runAll(name string, targets []string, cmds ...[]string) tea.Cmd {
	for _, t := range targets {
		if a := m.actions[t]; a != nil && a.running {
			m.message = warnStyle.Render(t + " is busy with " + a.name + "; wait for it to finish")
			return nil
		}
	}
	var commands []string
	for _, args := range cmds {
		command := procfile.Quote(m.opts.Neptuno)
		for _, a := range args {
			command += " " + procfile.Quote(a)
		}
		commands = append(commands, command)
	}
	m.nextAction++
	a := &action{id: m.nextAction, name: name, running: true}
	for _, t := range targets {
		m.actions[t] = a
	}
	m.message = name + "…"
	// Follow the output when it is about the row under the cursor.
	if r, ok := m.current(); ok {
		for _, t := range targets {
			if t == r.Name {
				m.tab = tabLogs
			}
		}
	}
	id, dir, script := a.id, m.opts.Root, strings.Join(commands, " && ")
	return func() tea.Msg { return <-startAction(id, dir, script) }
}

// startAction runs script with sh in dir and sends its output as it comes,
// then its end. The output goes through a file rather than a pipe, so a
// process it leaves running, such as overmind, doesn't hold it open, and
// quitting the dashboard doesn't cut the command off.
func startAction(id int, dir, script string) <-chan actionMsg {
	ch := make(chan actionMsg, 16)
	go func() {
		defer close(ch)
		end := func(lines []string, err error) { ch <- actionMsg{id: id, lines: lines, done: true, err: err} }
		out, err := os.CreateTemp("", "neptuno-action-*.log")
		if err != nil {
			end(nil, err)
			return
		}
		defer os.Remove(out.Name())
		in, err := os.Open(out.Name())
		if err != nil {
			out.Close()
			end(nil, err)
			return
		}
		defer in.Close()

		cmd := exec.Command("sh", "-c", script)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, out, out
		err = cmd.Start()
		out.Close()
		if err != nil {
			end(nil, err)
			return
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		var partial string
		read := func(final bool) []string {
			b, _ := io.ReadAll(in)
			lines, rest := splitLines(partial + string(b))
			partial = rest
			if final && partial != "" {
				lines, partial = append(lines, strings.TrimRight(partial, "\r")), ""
			}
			return lines
		}
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case err := <-done:
				end(read(true), err)
				return
			case <-tick.C:
				if lines := read(false); len(lines) > 0 {
					ch <- actionMsg{id: id, lines: lines, next: ch}
				}
			}
		}
	}()
	return ch
}

// splitLines splits complete lines off s, at newlines or carriage returns,
// returning them and what is left after the last one.
func splitLines(s string) (lines []string, rest string) {
	for {
		i := strings.IndexAny(s, "\r\n")
		if i < 0 || (s[i] == '\r' && i == len(s)-1) {
			// A trailing \r may be half of a \r\n.
			return lines, s
		}
		line, crlf := s[:i], strings.HasPrefix(s[i:], "\r\n")
		if s[i] == '\n' || crlf || line != "" {
			lines = append(lines, line)
		}
		if crlf {
			i++
		}
		s = s[i+1:]
	}
}

// handleAction records an action's output, and when it ends reports how it
// went and refreshes.
func (m *dashboard) handleAction(msg actionMsg) tea.Cmd {
	var a *action
	for _, x := range m.actions {
		if x.id == msg.id {
			a = x
			break
		}
	}
	if a != nil {
		a.lines = append(a.lines, msg.lines...)
		if n := len(a.lines); n > maxActionLines {
			a.lines = a.lines[n-maxActionLines:]
		}
	}
	if !msg.done {
		next := msg.next
		return func() tea.Msg {
			if x, ok := <-next; ok {
				return x
			}
			return nil
		}
	}
	name := "action"
	if a != nil {
		a.running, a.err, name = false, msg.err, a.name
	}
	if msg.err != nil {
		m.message = warnStyle.Render(name + " failed: " + msg.err.Error() + " (see Logs)")
	} else {
		m.message = name + " done"
	}
	return tea.Batch(m.refresh(), m.loadVolumes())
}

// actionText is a service's last action and its output, for the Logs tab.
func (m *dashboard) actionText(name string) string {
	a := m.actions[name]
	if a == nil {
		return ""
	}
	status := labelStyle.Render("running…")
	switch {
	case a.running:
	case a.err != nil:
		status = errStyle.Render("failed: " + a.err.Error())
	default:
		status = okStyle.Render("done")
	}
	lines := []string{labelStyle.Render("── neptuno "+a.name+" ── ") + status}
	lines = append(lines, a.lines...)
	return strings.Join(lines, "\n")
}

// logsPanel is the Logs tab: the service's container output and its last
// action's. A running or failed action goes last, where the tab follows, and
// one that succeeded goes first, before the output it led to.
func (m *dashboard) logsPanel(name, logs string) string {
	a := m.actions[name]
	if a == nil {
		return logs
	}
	text := m.actionText(name)
	if a.running || a.err != nil {
		return strings.TrimRight(logs, "\n") + "\n\n" + text
	}
	return text + "\n\n" + logs
}
