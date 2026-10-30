package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme")
	if _, err := Init(Options{Name: "acme", Dir: dir}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, folder := range Folders {
		info, err := os.Stat(filepath.Join(dir, folder))
		if err != nil || !info.IsDir() {
			t.Errorf("folder %s missing", folder)
		}
	}
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f.path)); err != nil {
			t.Errorf("file %s missing: %v", f.path, err)
		}
	}

	compose := read(t, filepath.Join(dir, "compose.yml"))
	for _, want := range []string{
		"name: acme\n",
		"image: caddy:2",
		"image: postgres:17",
		"image: valkey/valkey:8",
		"image: chrislusf/seaweedfs",
		"./Caddyfile:/etc/caddy/Caddyfile:ro",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yml missing %q", want)
		}
	}

	caddy := read(t, filepath.Join(dir, "Caddyfile"))
	if !strings.Contains(caddy, "http://s3.localhost {\n\treverse_proxy s3:8333\n}") {
		t.Errorf("Caddyfile missing s3 site block:\n%s", caddy)
	}

	gitignore := read(t, filepath.Join(dir, ".gitignore"))
	for _, want := range []string{"/.neptuno/", "*.local.env", "compose.override.yml", "neptuno.local.yml"} {
		if !strings.Contains(gitignore, want) {
			t.Errorf(".gitignore missing %q", want)
		}
	}
}

func TestInitDefaultsDirToName(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := Init(Options{Name: "acme"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := os.Stat("acme/compose.yml"); err != nil {
		t.Fatalf("compose.yml not created under ./acme: %v", err)
	}
}

func TestInitAllowsEmptyExistingDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := Init(Options{Name: "acme", Dir: dir}); err != nil {
		t.Fatalf("Init into empty dir: %v", err)
	}
}

func TestInitRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(Options{Name: "acme", Dir: dir}); err == nil {
		t.Fatal("Init into non-empty dir succeeded, want error")
	}
	if got := read(t, filepath.Join(dir, "compose.yml")); got != "mine" {
		t.Fatalf("existing compose.yml was overwritten: %q", got)
	}
}

func TestValidateName(t *testing.T) {
	for _, name := range []string{"acme", "acme-dev", "acme_dev", "1password"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", "Acme", "-acme", "_acme", "acme dev", "../acme", "acme/dev"} {
		if err := ValidateName(name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateName(%q) = %v, want ErrInvalidName", name, err)
		}
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
