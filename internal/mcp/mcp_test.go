package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/proxy"
)

const caddyfile = `
http://locke.localhost, http://*.locke.localhost {
	reverse_proxy locke:3000
}

http://docs.localhost {
	reverse_proxy host.docker.internal:8000
}

http://plain.localhost {
	reverse_proxy plain
}
`

func testProject() *project.Project {
	return &project.Project{
		Repos: []string{"docs", "locke", "plain", "nosite"},
		Services: map[string]project.Service{
			"locke": {Name: "locke"}, "plain": {Name: "plain"}, "nosite": {Name: "nosite"},
		},
		Config: project.Config{Repos: map[string]project.RepoConfig{
			"locke":  {MCP: map[string]string{"hyperdrive": "/_hyperdrive/mcp", "locke": "mcp"}},
			"docs":   {Run: project.RunHost, MCP: map[string]string{"search": "/mcp"}},
			"plain":  {MCP: map[string]string{"tools": "/mcp"}},
			"nosite": {MCP: map[string]string{"x": "/mcp"}},
			"ghost":  {MCP: map[string]string{"x": "/mcp"}},
			"api":    {Run: project.RunHost},
		}},
	}
}

func TestServers(t *testing.T) {
	got, warnings := Servers(testProject(), proxy.Parse(caddyfile), map[int]int{80: 8080})
	want := []Server{
		{Repo: "docs", Name: "search", Path: "/mcp", HostURL: "http://docs.localhost:8080/mcp", ContainerURL: "http://host.docker.internal:8000/mcp"},
		{Repo: "locke", Name: "locke", Path: "/mcp", HostURL: "http://locke.localhost:8080/mcp", ContainerURL: "http://locke:3000/mcp"},
		{Repo: "locke", Name: "hyperdrive", Path: "/_hyperdrive/mcp", HostURL: "http://locke.localhost:8080/_hyperdrive/mcp", ContainerURL: "http://locke:3000/_hyperdrive/mcp"},
		{Repo: "nosite", Name: "x", Path: "/mcp"},
		{Repo: "plain", Name: "tools", Path: "/mcp", HostURL: "http://plain.localhost:8080/mcp", ContainerURL: "http://plain:80/mcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Servers =\n%+v\nwant\n%+v", got, want)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "ghost") || !strings.Contains(warnings[1], "no site for nosite") {
		t.Errorf("warnings = %q", warnings)
	}
	if ids := []string{got[1].ID(), got[2].ID()}; !reflect.DeepEqual(ids, []string{"locke", "locke-hyperdrive"}) {
		t.Errorf("IDs = %v", ids)
	}
}

func TestWriteProjectKeepsOtherServers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ProjectFile)
	if err := os.WriteFile(path, []byte(`{"mcpServers": {"mine": {"command": "my-mcp"}, "locke-old": {"type": "http", "url": "http://x"}}, "other": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	servers := []Server{
		{Repo: "locke", Name: "hyperdrive", HostURL: "http://locke.localhost/_hyperdrive/mcp"},
		{Repo: "nosite", Name: "x"},
	}
	wrote, changed, err := WriteProject(root, servers, []string{"locke-old"})
	if err != nil || !changed {
		t.Fatalf("WriteProject = %v, %v", changed, err)
	}
	if !reflect.DeepEqual(wrote, []string{"locke-hyperdrive"}) {
		t.Errorf("wrote %v", wrote)
	}
	var doc struct {
		MCPServers map[string]map[string]string `json:"mcpServers"`
		Other      int                          `json:"other"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"mine":             {"command": "my-mcp"},
		"locke-hyperdrive": {"type": "http", "url": "http://locke.localhost/_hyperdrive/mcp"},
	}
	if !reflect.DeepEqual(doc.MCPServers, want) || doc.Other != 1 {
		t.Errorf(".mcp.json =\n%s", b)
	}

	if _, changed, _ := WriteProject(root, servers, wrote); changed {
		t.Error("rewrote an up-to-date .mcp.json")
	}
}

func TestWriteProjectWithNothingDeclared(t *testing.T) {
	root := t.TempDir()
	if _, changed, err := WriteProject(root, nil, nil); err != nil || changed {
		t.Fatalf("WriteProject = %v, %v", changed, err)
	}
	if _, err := os.Stat(filepath.Join(root, ProjectFile)); err == nil {
		t.Error("created .mcp.json with no servers")
	}
}

func TestWriteContainer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude")
	servers := []Server{{Repo: "locke", Name: "hyperdrive", ContainerURL: "http://locke:3000/_hyperdrive/mcp"}}
	if changed, err := WriteContainer(dir, servers); err != nil || !changed {
		t.Fatalf("WriteContainer = %v, %v", changed, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ContainerFile))
	if !strings.Contains(string(b), `"locke-hyperdrive": {
      "type": "http",
      "url": "http://locke:3000/_hyperdrive/mcp"`) {
		t.Errorf("%s =\n%s", ContainerFile, b)
	}
	if changed, _ := WriteContainer(dir, servers); changed {
		t.Error("rewrote an up-to-date config")
	}
}
