package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestWriteDesktop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Claude", "claude_desktop_config.json")
	e := &DesktopEntry{Command: "/bin/neptuno", Args: []string{"mcp", "serve", "--project", "/p"}, Env: map[string]string{"PATH": "/usr/bin"}}

	if changed, err := WriteDesktop(path, "neptuno-acme", nil); err != nil || changed {
		t.Fatalf("removing from a missing file = %v, %v", changed, err)
	}
	if changed, err := WriteDesktop(path, "neptuno-acme", e); err != nil || !changed {
		t.Fatalf("WriteDesktop = %v, %v", changed, err)
	}
	if changed, _ := WriteDesktop(path, "neptuno-acme", e); changed {
		t.Error("rewrote an entry that was already there")
	}
	got, err := ReadDesktop(path, "neptuno-acme")
	if err != nil || !reflect.DeepEqual(got, e) {
		t.Fatalf("ReadDesktop = %+v, %v", got, err)
	}

	// Other servers and settings stay.
	var doc map[string]any
	b, _ := os.ReadFile(path)
	_ = json.Unmarshal(b, &doc)
	doc["globalShortcut"] = "Ctrl+Space"
	doc["mcpServers"].(map[string]any)["other"] = map[string]any{"command": "other-mcp"}
	b, _ = json.Marshal(doc)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := WriteDesktop(path, "neptuno-acme", nil); err != nil || !changed {
		t.Fatalf("removing = %v, %v", changed, err)
	}
	b, _ = os.ReadFile(path)
	doc = nil
	_ = json.Unmarshal(b, &doc)
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["neptuno-acme"]; ok || servers["other"] == nil || doc["globalShortcut"] != "Ctrl+Space" {
		t.Errorf("config after removing =\n%s", b)
	}
}

func TestProbe(t *testing.T) {
	codes := map[string]int{"/mcp": http.StatusOK, "/missing": http.StatusNotFound, "/gone": http.StatusBadGateway, "/auth": http.StatusUnauthorized}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(codes[r.URL.Path])
	}))
	defer ts.Close()
	servers := []Server{
		{Repo: "a", Name: "mcp", HostURL: ts.URL + "/mcp"},
		{Repo: "a", Name: "missing", HostURL: ts.URL + "/missing"},
		{Repo: "a", Name: "gone", HostURL: ts.URL + "/gone"},
		{Repo: "a", Name: "auth", HostURL: ts.URL + "/auth"},
		{Repo: "a", Name: "closed", HostURL: "http://127.0.0.1:1/mcp"},
		{Repo: "a", Name: "nourl"},
	}
	got := ProbeAll(servers, 2*time.Second)
	want := map[string]Health{"a-mcp": Up, "a-missing": NotFound, "a-gone": Down, "a-auth": Up, "a-closed": Down}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProbeAll = %v, want %v", got, want)
	}
}

func TestProjectFileCurrent(t *testing.T) {
	root := t.TempDir()
	servers := []Server{{Repo: "locke", Name: "hyperdrive", HostURL: "http://locke.localhost/mcp"}}
	if ProjectFileCurrent(root, servers) {
		t.Error("current without a .mcp.json")
	}
	if _, _, err := WriteProject(root, servers, nil); err != nil {
		t.Fatal(err)
	}
	if !ProjectFileCurrent(root, servers) {
		t.Error("not current right after writing it")
	}
	servers[0].HostURL = "http://locke.localhost:8080/mcp"
	if ProjectFileCurrent(root, servers) {
		t.Error("current although the URL changed")
	}
}
