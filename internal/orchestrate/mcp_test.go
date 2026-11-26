package orchestrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/state"
)

// withMCP declares an MCP server for api, gives it a Caddyfile site and
// mounts its repo at /app.
func withMCP(t *testing.T, o *Orchestrator) {
	t.Helper()
	root := o.Project.Root
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("neptuno.yml", "repos:\n  docs:\n    run: host\n  api:\n    mcp:\n      hyperdrive: /_hyperdrive/mcp\n")
	write("Caddyfile", "http://api.localhost {\n\treverse_proxy api:3000\n}\n")
	write("compose.override.yml", "services:\n  api:\n    volumes: [\"./repos/api:/app\"]\n")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	o.Project = p
}

func TestUpWritesMCPConfigs(t *testing.T) {
	t.Setenv("TMUX", "")
	o, _ := setup(t)
	withMCP(t, o)
	if err := o.Up(UpOptions{Selected: []string{"api"}, Detach: true}); err != nil {
		t.Fatal(err)
	}
	root := o.Project.Root
	if got := read(t, filepath.Join(root, ".mcp.json")); !strings.Contains(got, `"url": "http://api.localhost/_hyperdrive/mcp"`) {
		t.Errorf(".mcp.json =\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".neptuno/claude")); err == nil {
		t.Error("wrote a Claude config for the containers")
	}
	for repo, want := range map[string]string{
		"api":  "neptuno exec api -- <command>",
		"docs": "runs on this machine (`run: host`",
		"web":  "in the web container with `neptuno exec web`",
	} {
		if got := read(t, filepath.Join(root, ".claude/agents", repo+".md")); !strings.Contains(got, want) || !strings.Contains(got, "name: "+repo+"\n") {
			t.Errorf("%s's subagent misses %q:\n%s", repo, want, got)
		}
	}
	if got := read(t, filepath.Join(root, ".claude/agents/api.md")); !strings.Contains(got, "at `/app`") {
		t.Errorf("api's subagent doesn't name the mount:\n%s", got)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude/agents/api.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.SyncMCP(); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(root, ".claude/agents/api.md")); got != "mine\n" {
		t.Errorf("overwrote an edited subagent: %q", got)
	}
	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.MCPServers, []string{"api-hyperdrive"}) || !reflect.DeepEqual(st.LastUp, []string{"api"}) {
		t.Errorf("state = %+v", st)
	}
}
