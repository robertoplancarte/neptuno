package project

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const compose = `name: acme
services:
  proxy:
    image: caddy:2
  postgres:
    image: postgres:17
  valkey:
    image: valkey/valkey:8
  api:
    build: dockerfiles/api
    depends_on: [postgres, valkey]
  web:
    build: dockerfiles/web
    depends_on:
      api:
        condition: service_started
      proxy:
        condition: service_started
`

// newProject writes a project with the given files into a temp dir.
func newProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoad(t *testing.T) {
	root := newProject(t, map[string]string{
		"compose.yml":           compose,
		"neptuno.yml":           "repos:\n  worker:\n    run: host\n    depends_on: [valkey]\n",
		"neptuno.local.yml":     "default_env: staging\n",
		"repos/api/.keep":       "",
		"repos/web/.keep":       "",
		"repos/worker/.keep":    "",
		"repos/orphan/.keep":    "",
		"repos/.hidden/.keep":   "",
		"environments/.gitkeep": "",
	})

	p, err := Load(filepath.Join(root, "repos", "api"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != root {
		t.Errorf("Root = %s, want %s", p.Root, root)
	}
	if p.Name != "acme" {
		t.Errorf("Name = %s, want acme", p.Name)
	}
	if p.Config.DefaultEnv != "staging" {
		t.Errorf("DefaultEnv = %s, want staging from neptuno.local.yml", p.Config.DefaultEnv)
	}
	if want := []string{"api", "orphan", "web", "worker"}; !reflect.DeepEqual(p.Repos, want) {
		t.Errorf("Repos = %v, want %v", p.Repos, want)
	}
	if got := p.Services["web"].DependsOn; !reflect.DeepEqual(got, []string{"api", "proxy"}) {
		t.Errorf("web depends_on = %v", got)
	}

	repos, services := p.Selectable()
	if want := []string{"api", "web", "worker"}; !reflect.DeepEqual(repos, want) {
		t.Errorf("selectable repos = %v, want %v (orphan has no compose service)", repos, want)
	}
	if want := []string{"postgres", "proxy", "valkey"}; !reflect.DeepEqual(services, want) {
		t.Errorf("selectable services = %v, want %v", services, want)
	}
}

func TestClosure(t *testing.T) {
	root := newProject(t, map[string]string{
		"compose.yml":        compose,
		"neptuno.yml":        "repos:\n  worker:\n    run: host\n    depends_on: [valkey]\n",
		"repos/api/.keep":    "",
		"repos/web/.keep":    "",
		"repos/worker/.keep": "",
	})
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		selected, want []string
	}{
		{[]string{"web"}, []string{"postgres", "valkey", "api", "proxy", "web"}},
		{[]string{"api", "web"}, []string{"postgres", "valkey", "api", "proxy", "web"}},
		{[]string{"worker"}, []string{"valkey", "worker"}},
		{[]string{"proxy"}, []string{"proxy"}},
	}
	for _, tt := range tests {
		got, err := p.Closure(tt.selected)
		if err != nil {
			t.Errorf("Closure(%v): %v", tt.selected, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Closure(%v) = %v, want %v", tt.selected, got, tt.want)
		}
	}

	if _, err := p.Closure([]string{"nope"}); err == nil {
		t.Error("Closure of unknown service succeeded")
	}
}

func TestClosureCycle(t *testing.T) {
	root := newProject(t, map[string]string{
		"compose.yml": "services:\n  a:\n    depends_on: [b]\n  b:\n    depends_on: [a]\n",
		"neptuno.yml": "",
	})
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Closure([]string{"a"}); err == nil {
		t.Error("Closure with a cycle succeeded")
	}
}

func TestFindOutsideProject(t *testing.T) {
	if _, err := Find(t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Find = %v, want ErrNotFound", err)
	}
}

func TestServiceAt(t *testing.T) {
	root := newProject(t, map[string]string{
		"compose.yml":                   compose,
		"repos/api/src/.keep":           "",
		"dockerfiles/api/.keep":         "",
		"environments/_shared/base.env": "",
		"environments/web/base.env":     "",
		"procfiles/worker/Procfile":     "",
	})
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"":                     "",
		"repos":                "",
		"repos/api":            "api",
		"repos/api/src":        "api",
		"dockerfiles/api":      "api",
		"environments/web":     "web",
		"environments/_shared": "",
		"procfiles/worker":     "worker",
	}
	for dir, want := range tests {
		if got := p.ServiceAt(filepath.Join(root, dir)); got != want {
			t.Errorf("ServiceAt(%q) = %q, want %q", dir, got, want)
		}
	}
	if got := p.ServiceAt(t.TempDir()); got != "" {
		t.Errorf("ServiceAt outside the project = %q", got)
	}
}
