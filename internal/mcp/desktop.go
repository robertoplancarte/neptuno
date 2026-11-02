package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// DesktopEntry is how Claude desktop starts a local MCP server.
type DesktopEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// DesktopConfigPath is Claude desktop's config file:
// ~/Library/Application Support/Claude on macOS, %APPDATA%\Claude on
// Windows and ~/.config/Claude elsewhere. NEPTUNO_CLAUDE_DESKTOP_CONFIG
// overrides it.
func DesktopConfigPath() (string, error) {
	if p := os.Getenv("NEPTUNO_CLAUDE_DESKTOP_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Claude", "claude_desktop_config.json"), nil
}

// DesktopName is the project's server name in Claude desktop's config.
func DesktopName(project string) string {
	return "neptuno-" + project
}

// ReadDesktop returns the server called name in Claude desktop's config at
// path, or nil when there is none.
func ReadDesktop(path, name string) (*DesktopEntry, error) {
	doc, list, _, err := readDesktop(path)
	if err != nil || doc == nil {
		return nil, err
	}
	raw, ok := list[name]
	if !ok {
		return nil, nil
	}
	var e DesktopEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", path, name, err)
	}
	return &e, nil
}

// WriteDesktop adds, replaces or, with a nil entry, removes the server
// called name in Claude desktop's config at path, keeping everything else
// in it. It reports whether the file changed.
func WriteDesktop(path, name string, e *DesktopEntry) (bool, error) {
	doc, list, old, err := readDesktop(path)
	if err != nil {
		return false, err
	}
	if doc == nil {
		if e == nil {
			return false, nil
		}
		doc = map[string]json.RawMessage{}
	}
	if e == nil {
		if _, ok := list[name]; !ok {
			return false, nil
		}
		delete(list, name)
	} else {
		if cur, ok := list[name]; ok {
			var c DesktopEntry
			if json.Unmarshal(cur, &c) == nil && reflect.DeepEqual(&c, e) {
				return false, nil
			}
		}
		b, err := json.Marshal(e)
		if err != nil {
			return false, err
		}
		list[name] = b
	}
	b, err := json.Marshal(list)
	if err != nil {
		return false, err
	}
	doc["mcpServers"] = b
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return writeJSON(path, doc, old)
}

// readDesktop reads Claude desktop's config, returning a nil doc when the
// file is missing.
func readDesktop(path string) (doc, list map[string]json.RawMessage, old []byte, err error) {
	old, err = os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, map[string]json.RawMessage{}, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	doc = map[string]json.RawMessage{}
	list = map[string]json.RawMessage{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &doc); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if raw, ok := doc["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: mcpServers: %w", path, err)
		}
	}
	return doc, list, old, nil
}
