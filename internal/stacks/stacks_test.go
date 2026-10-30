package stacks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinStacksRender(t *testing.T) {
	list, err := List("")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range list {
		names[s.Name] = true
		data := Data{Name: "my-app", Project: "acme", Port: s.Port}
		if s.Port == 0 {
			t.Errorf("%s: no port", s.Name)
		}
		for _, f := range []string{"Dockerfile", "Procfile", "base.env"} {
			out, err := s.Render(f, data)
			if err != nil {
				t.Errorf("%s/%s: %v", s.Name, f, err)
				continue
			}
			if strings.Contains(string(out), "{{") {
				t.Errorf("%s/%s left a template action unrendered", s.Name, f)
			}
		}
		if _, err := s.AppFiles(data); err != nil {
			t.Errorf("%s app files: %v", s.Name, err)
		}
		if _, err := s.GenerateCommand(data); err != nil {
			t.Errorf("%s generate: %v", s.Name, err)
		}
	}
	for _, want := range []string{"blank", "go", "node", "python", "rails"} {
		if !names[want] {
			t.Errorf("built-in stack %s missing", want)
		}
	}
}

func TestAppFilesDropTmplSuffix(t *testing.T) {
	s, err := Get("", "go")
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.AppFiles(Data{Name: "svc", Port: 9000})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(files["go.mod"]); got != "module svc\n\ngo 1.24\n" {
		t.Errorf("go.mod = %q", got)
	}
	if !strings.Contains(string(files["main.go"]), `port = "9000"`) {
		t.Errorf("main.go not rendered:\n%s", files["main.go"])
	}
}

func TestProjectStackOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("node/stack.yml", "description: our node\nport: 4000\n")
	write("node/Procfile", "web: yarn dev\n")
	write("elixir/stack.yml", "description: phoenix\nport: 4000\n")
	write("notastack/README", "no stack.yml here\n")

	s, err := Get(dir, "node")
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 4000 || s.Source != filepath.Join(dir, "node") {
		t.Errorf("project stack did not override built-in: %+v", s)
	}
	if out, _ := s.Render("Procfile", Data{}); string(out) != "web: yarn dev\n" {
		t.Errorf("Procfile = %q", out)
	}
	if _, err := Get(dir, "elixir"); err != nil {
		t.Errorf("project-only stack: %v", err)
	}
	if _, err := Get(dir, "notastack"); err == nil {
		t.Error("folder without stack.yml counted as a stack")
	}
}
