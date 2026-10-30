package add

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/scaffold"
)

type fakeRunner struct{ cmds []string }

func (f *fakeRunner) Run(dir, name string, args ...string) error {
	f.cmds = append(f.cmds, filepath.Base(dir)+"$ "+name+" "+strings.Join(args, " "))
	return nil
}
func (f *fakeRunner) Check(dir, name string, args ...string) bool { return true }
func (f *fakeRunner) Output(dir, name string, args ...string) (string, error) {
	return "", nil
}
func (f *fakeRunner) Attach(dir, name string, args ...string) error { return nil }
func (f *fakeRunner) LookPath(string) error                         { return nil }

// newAdder scaffolds a fresh project with `neptuno init`.
func newAdder(t *testing.T) (*Adder, *fakeRunner) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "acme")
	if _, err := scaffold.Init(scaffold.Options{Name: "acme", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{}
	return &Adder{Project: p, Runner: f, Log: &bytes.Buffer{}}, f
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddNewRepoFromStack(t *testing.T) {
	a, f := newAdder(t)
	root := a.Project.Root
	if err := a.Add(Options{Name: "web", Stack: "node"}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"repos/web/package.json", "repos/web/server.js", "repos/web/.gitignore",
		"dockerfiles/web/Dockerfile", "procfiles/web/Procfile",
		"environments/web/base.env", "environments/web/local.env",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("%s not created", path)
		}
	}
	if got := read(t, filepath.Join(root, "dockerfiles/web/Dockerfile")); !strings.Contains(got, "FROM node:22-slim") {
		t.Errorf("Dockerfile:\n%s", got)
	}

	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Services["web"].DependsOn, ","); got != "postgres,valkey,s3" {
		t.Errorf("web depends_on = %s", got)
	}
	if !p.IsRepo("web") {
		t.Error("web is not a repo after add")
	}
	if caddy := read(t, filepath.Join(root, "Caddyfile")); !strings.Contains(caddy, "http://web.localhost {\n\treverse_proxy web:3000\n}") {
		t.Errorf("Caddyfile:\n%s", caddy)
	}
	// node has no generator, so the only command is git init in the new repo.
	if want := []string{"web$ git init --quiet"}; strings.Join(f.cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands = %q, want %q", f.cmds, want)
	}
}

func TestAddRunsGeneratorInContainer(t *testing.T) {
	a, f := newAdder(t)
	if err := a.Add(Options{Name: "shop", Stack: "rails", Port: 4000}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 2 || !strings.HasPrefix(f.cmds[0], "acme$ docker compose run --rm --no-deps --build shop sh -c ") ||
		!strings.Contains(f.cmds[0], "rails new . --name=shop --database=postgresql") {
		t.Fatalf("commands = %q", f.cmds)
	}
	if f.cmds[1] != "shop$ git init --quiet" {
		t.Errorf("second command = %q", f.cmds[1])
	}
	root := a.Project.Root
	if _, err := os.Stat(filepath.Join(root, ".neptuno/env/shop.env")); err != nil {
		t.Error("env file not written before the generator ran")
	}
	if !strings.Contains(read(t, filepath.Join(root, "Caddyfile")), "reverse_proxy shop:4000") {
		t.Error("--port not used for the Caddy route")
	}

	f.cmds = nil
	if err := a.Add(Options{Name: "shop2", Stack: "rails", NoGenerate: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 1 || f.cmds[0] != "shop2$ git init --quiet" {
		t.Errorf("--no-generate commands = %q", f.cmds)
	}
}

func TestAddWithURLAddsSubmodule(t *testing.T) {
	a, f := newAdder(t)
	if err := a.Add(Options{Name: "api", URL: "git@github.com:acme/api.git"}); err != nil {
		t.Fatal(err)
	}
	want := "acme$ git submodule add git@github.com:acme/api.git repos/api"
	if len(f.cmds) != 1 || f.cmds[0] != want {
		t.Errorf("commands = %q, want [%q]", f.cmds, want)
	}
}

func TestAddIsRerunnable(t *testing.T) {
	a, _ := newAdder(t)
	root := a.Project.Root
	if err := a.Add(Options{Name: "web", Stack: "node"}); err != nil {
		t.Fatal(err)
	}
	procfile := filepath.Join(root, "procfiles/web/Procfile")
	if err := os.WriteFile(procfile, []byte("web: my edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compose := read(t, filepath.Join(root, "compose.yml"))
	caddy := read(t, filepath.Join(root, "Caddyfile"))

	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	a.Project = p
	if err := a.Add(Options{Name: "web", Stack: "node"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, procfile); got != "web: my edit\n" {
		t.Errorf("rerun overwrote the Procfile: %q", got)
	}
	if read(t, filepath.Join(root, "compose.yml")) != compose || read(t, filepath.Join(root, "Caddyfile")) != caddy {
		t.Error("rerun changed compose.yml or the Caddyfile")
	}
}

func TestAddRejectsBadNames(t *testing.T) {
	a, _ := newAdder(t)
	for _, name := range []string{"Web", "_shared", "postgres", "../x"} {
		if err := a.Add(Options{Name: name}); err == nil {
			t.Errorf("Add(%q) succeeded", name)
		}
	}
	if err := a.Add(Options{Name: "x", Stack: "cobol"}); err == nil {
		t.Error("unknown stack accepted")
	}
}

func TestAddIgnoresServicesOnlyInOverride(t *testing.T) {
	a, _ := newAdder(t)
	root := a.Project.Root
	override := "services:\n  web:\n    environment: {DEBUG: \"1\"}\n"
	if err := os.WriteFile(filepath.Join(root, "compose.override.yml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	a.Project = p
	if err := a.Add(Options{Name: "web", Stack: "node"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(root, "compose.yml")), "\n  web:\n") {
		t.Error("web not added to compose.yml when an override already mentions it")
	}
}
