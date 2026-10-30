// Package state reads and writes .neptuno/state.json, the git-ignored record
// of choices that should survive restarts.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// State is the content of .neptuno/state.json.
type State struct {
	// Env is the project-wide active configuration. Empty means the
	// default_env from neptuno.yml.
	Env string `json:"env,omitempty"`
	// RepoEnv overrides Env for single repos.
	RepoEnv map[string]string `json:"repo_env,omitempty"`
	// LastUp is the selection from the last `neptuno up`, used to preselect
	// the menu.
	LastUp []string `json:"last_up,omitempty"`
}

// Path returns the state file path under a project root.
func Path(root string) string {
	return filepath.Join(root, ".neptuno", "state.json")
}

// Load reads the state file, returning an empty State when it is missing.
func Load(root string) (State, error) {
	var s State
	b, err := os.ReadFile(Path(root))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Save writes the state file.
func Save(root string, s State) error {
	path := Path(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// EnvFor returns the active configuration for repo.
func (s State) EnvFor(repo, defaultEnv string) string {
	if e := s.RepoEnv[repo]; e != "" {
		return e
	}
	if s.Env != "" {
		return s.Env
	}
	return defaultEnv
}
