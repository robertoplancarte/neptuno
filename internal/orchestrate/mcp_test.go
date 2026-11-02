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
// mounts the Claude config dir.
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
	write("compose.override.yml", "services:\n  api:\n    volumes: [\"./.neptuno/claude:/root/.claude\"]\n")
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
	if got := read(t, filepath.Join(root, ".neptuno/claude/neptuno-mcp.json")); !strings.Contains(got, `"url": "http://api:3000/_hyperdrive/mcp"`) {
		t.Errorf("neptuno-mcp.json =\n%s", got)
	}
	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.MCPServers, []string{"api-hyperdrive"}) || !reflect.DeepEqual(st.LastUp, []string{"api"}) {
		t.Errorf("state = %+v", st)
	}
}

func TestClaudeGetsTheContainerMCPConfig(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	withMCP(t, o)
	f.containers["api"] = []string{"abc123"}
	f.overminds[socket("api")] = true
	if err := o.Claude(ClaudeOptions{Repo: "api", Args: []string{"--continue"}, Detach: true}); err != nil {
		t.Fatal(err)
	}
	want := "docker compose exec api claude --continue --mcp-config /root/.claude/neptuno-mcp.json;"
	found := false
	for _, c := range f.cmds {
		found = found || strings.Contains(c, want)
	}
	if !found {
		t.Errorf("no %q in:\n%s", want, strings.Join(f.cmds, "\n"))
	}
}

func TestWithMCPConfig(t *testing.T) {
	cases := map[string][]string{
		"":                  {"--mcp-config", ContainerMCPConfig},
		"-p fix it":         {"-p", "fix it", "--mcp-config", ContainerMCPConfig},
		"--continue -- --x": {"--continue", "--mcp-config", ContainerMCPConfig, "--", "--x"},
	}
	for in, want := range cases {
		var args []string
		switch in {
		case "-p fix it":
			args = []string{"-p", "fix it"}
		case "--continue -- --x":
			args = []string{"--continue", "--", "--x"}
		}
		if got := withMCPConfig(args); !reflect.DeepEqual(got, want) {
			t.Errorf("withMCPConfig(%q) = %q, want %q", in, got, want)
		}
	}
}
