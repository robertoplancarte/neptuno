// Package scaffold creates a new unified development environment on disk.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"text/template"
)

//go:embed templates/*.tmpl
var templates embed.FS

// Folders are the four convention folders. Each holds one subfolder per repo.
var Folders = []string{"environments", "dockerfiles", "procfiles", "repos"}

// files maps a path inside the new project to the template that renders it.
var files = []struct{ path, template string }{
	{"compose.yml", "compose.yml.tmpl"},
	{"Caddyfile", "Caddyfile.tmpl"},
	{"neptuno.yml", "neptuno.yml.tmpl"},
	{".gitignore", "gitignore.tmpl"},
	{"README.md", "README.md.tmpl"},
	{"environments/_shared/base.env", "shared_base.env.tmpl"},
	{"environments/_shared/local.env", "shared_local.env.tmpl"},
}

// namePattern matches valid docker compose project names, which the project
// name doubles as.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ErrInvalidName is returned when a project name is not a valid compose
// project name.
var ErrInvalidName = errors.New("project name must start with a lowercase letter or digit and contain only lowercase letters, digits, dashes and underscores")

// ValidateName reports whether name can be used as a project name.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrInvalidName)
	}
	return nil
}

// Options configures Init.
type Options struct {
	// Name is the project name, used for the directory and the compose
	// project name.
	Name string
	// Dir is the directory to create. Defaults to Name.
	Dir string
}

// Init creates the project directory with the four convention folders,
// neptuno.yml, compose.yml, Caddyfile and supporting files. It refuses to
// write into a directory that already exists and is not empty, and returns
// the paths it created, relative to the project directory.
func Init(opts Options) ([]string, error) {
	if err := ValidateName(opts.Name); err != nil {
		return nil, err
	}
	dir := opts.Dir
	if dir == "" {
		dir = opts.Name
	}

	if err := ensureEmptyDir(dir); err != nil {
		return nil, err
	}

	var created []string
	for _, folder := range Folders {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
			return created, err
		}
		created = append(created, folder+"/")
	}

	data := struct{ Name string }{opts.Name}
	for _, f := range files {
		out, err := render(f.template, data)
		if err != nil {
			return created, err
		}
		target := filepath.Join(dir, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return created, err
		}
		if err := os.WriteFile(target, out, 0o644); err != nil {
			return created, err
		}
		created = append(created, f.path)
	}

	// Keep empty convention folders in git.
	for _, folder := range []string{"dockerfiles", "procfiles", "repos"} {
		keep := filepath.Join(dir, folder, ".gitkeep")
		if err := os.WriteFile(keep, nil, 0o644); err != nil {
			return created, err
		}
	}

	return created, nil
}

func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return os.MkdirAll(dir, 0o755)
	case err != nil:
		return err
	case len(entries) > 0:
		return fmt.Errorf("%s already exists and is not empty", dir)
	}
	return nil
}

func render(name string, data any) ([]byte, error) {
	tmpl, err := template.ParseFS(templates, "templates/"+name)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return buf.Bytes(), nil
}
