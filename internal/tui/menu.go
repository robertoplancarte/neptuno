package tui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

// mcpMenu lists what can be done with a repo's MCP servers.
type mcpMenu struct {
	row    orchestrate.ServiceStatus
	items  []menuItem
	cursor int
}

type menuItem struct {
	label string
	// copy is text to put on the clipboard, or register a server to add to
	// Claude Code for every folder.
	copy     string
	register *mcp.Server
}

func newMCPMenu(r orchestrate.ServiceStatus) *mcpMenu {
	m := &mcpMenu{row: r}
	for _, s := range r.MCP {
		if s.HostURL != "" {
			m.items = append(m.items,
				menuItem{label: "Copy " + s.ID() + "'s URL for this machine", copy: s.HostURL},
				menuItem{label: "Copy the `claude mcp add` command for it", copy: claudeMCPAdd(s)},
				menuItem{label: "Add it to Claude Code in every folder (--scope user)", register: &s},
			)
		}
		if s.ContainerURL != "" {
			m.items = append(m.items, menuItem{label: "Copy " + s.ID() + "'s URL for containers", copy: s.ContainerURL})
		}
	}
	return m
}

// claudeMCPAdd is the command that adds s to Claude Code for every folder.
func claudeMCPAdd(s mcp.Server) string {
	return strings.Join(claudeMCPAddArgs(s), " ")
}

func claudeMCPAddArgs(s mcp.Server) []string {
	return []string{"claude", "mcp", "add", "--transport", "http", "--scope", "user", s.ID(), s.HostURL}
}

// menuKey handles a key while the MCP menu is open.
func (m *dashboard) menuKey(key string) tea.Cmd {
	c := m.menu
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "esc", "q", "m":
		m.menu = nil
	case "up", "k":
		if c.cursor > 0 {
			c.cursor--
		}
	case "down", "j":
		if c.cursor < len(c.items)-1 {
			c.cursor++
		}
	case "enter":
		m.menu = nil
		if c.cursor >= len(c.items) {
			return nil
		}
		it := c.items[c.cursor]
		if it.register != nil {
			return m.registerMCP(*it.register)
		}
		if err := m.copy(it.copy); err != nil {
			m.message = warnStyle.Render("could not copy: " + err.Error())
		} else {
			m.message = "copied " + it.copy
		}
	}
	return nil
}

// menuView is the MCP menu, in place of the details.
func (m *dashboard) menuView() string {
	c := m.menu
	var b strings.Builder
	b.WriteString(nameStyle.Render(c.row.Name+"'s MCP servers") + "\n")
	if len(c.items) == 0 {
		b.WriteString(labelStyle.Render("None of them has a URL yet: give "+c.row.Name+" a site in the Caddyfile.") + "\n")
	}
	for i, it := range c.items {
		if i == c.cursor {
			b.WriteString(cursorStyle.Render("> "+it.label) + "\n")
		} else {
			b.WriteString("  " + it.label + "\n")
		}
	}
	b.WriteString("\n" + labelStyle.Render("Claude in the project folder already gets them from .mcp.json, and `neptuno claude` in the containers.") + "\n")
	return b.String()
}

// registerMCP runs `claude mcp add --scope user` in the background.
func (m *dashboard) registerMCP(s mcp.Server) tea.Cmd {
	run := m.opts.Exec
	if run == nil {
		root := m.opts.Root
		run = func(name string, args ...string) (string, error) {
			cmd := exec.Command(name, args...)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
	}
	args := claudeMCPAddArgs(s)
	action := "add " + s.ID() + " to Claude Code"
	m.message = action + "…"
	return func() tea.Msg {
		out, err := run(args[0], args[1:]...)
		if err != nil {
			if msg := strings.TrimSpace(out); msg != "" {
				err = fmt.Errorf("%w: %s", err, lastLine(msg))
			}
		}
		return doneMsg{action, err}
	}
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}

// copy puts text on the clipboard.
func (m *dashboard) copy(text string) error {
	if m.opts.Copy != nil {
		return m.opts.Copy(text)
	}
	return copyToClipboard(text)
}

// copyToClipboard uses tmux's buffer inside tmux, which also sets the outer
// terminal's clipboard when tmux can, then the system's clipboard tools, and
// last an OSC 52 escape, which many terminals honor, also over SSH.
func copyToClipboard(text string) error {
	try := func(name string, args ...string) bool {
		if _, err := exec.LookPath(name); err != nil {
			return false
		}
		cmd := exec.Command(name, args...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run() == nil
	}
	if os.Getenv("TMUX") != "" && (try("tmux", "load-buffer", "-w", "-") || try("tmux", "load-buffer", "-")) {
		return nil
	}
	if try("pbcopy") || os.Getenv("WAYLAND_DISPLAY") != "" && try("wl-copy") ||
		os.Getenv("DISPLAY") != "" && try("xclip", "-selection", "clipboard") {
		return nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return errors.New("no clipboard tool found")
	}
	defer tty.Close()
	_, err = fmt.Fprintf(tty, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	return err
}
