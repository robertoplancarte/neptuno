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
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl templates/skills/*/SKILL.md.tmpl
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

// namePattern matches valid docker compose project names, which project
// names double as. Repo names follow the same rule so they work as compose
// service names and <repo>.localhost hosts.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// ErrInvalidName is returned for an invalid project or repo name.
var ErrInvalidName = errors.New("name must start with a lowercase letter or digit and contain only lowercase letters, digits, dashes and underscores")

// ValidateName reports whether name can be used as a project or repo name.
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
	// Services names the DefaultServices to set up, along with the ones
	// they require. Nil means all of them; an empty slice means none, which
	// leaves only the proxy.
	Services []string
}

// templateData is what the project templates render with.
type templateData struct {
	Name string
	// Services are the chosen shared services, and All is the proxy
	// followed by them.
	Services, All []Service
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

	names := opts.Services
	if names == nil {
		names = DefaultServiceNames()
	}
	services, _, err := ResolveServices(names)
	if err != nil {
		return nil, err
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

	data := templateData{Name: opts.Name, Services: services, All: append([]Service{Proxy}, services...)}
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

	agents, err := writeAgentFiles(dir, data)
	created = append(created, agents...)
	if err != nil {
		return created, err
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

// AgentFiles maps the files that tell coding agents how to work in the
// project to their templates: AGENTS.md, which Cursor, Codex and others
// read, CLAUDE.md, which imports it for Claude Code, and the skills in
// .claude/skills.
func AgentFiles() []struct{ Path, Template string } {
	out := []struct{ Path, Template string }{
		{"AGENTS.md", "AGENTS.md.tmpl"},
		{"CLAUDE.md", "CLAUDE.md.tmpl"},
	}
	skills, _ := fs.Glob(templates, "templates/skills/*/SKILL.md.tmpl")
	for _, t := range skills {
		name := filepath.Base(filepath.Dir(t))
		out = append(out, struct{ Path, Template string }{
			".claude/skills/" + name + "/SKILL.md", strings.TrimPrefix(t, "templates/"),
		})
	}
	return out
}

// WriteAgentFiles writes each of AgentFiles that the project doesn't have,
// leaving the ones it has alone, and returns the paths it wrote, relative
// to root.
func WriteAgentFiles(root, name string) ([]string, error) {
	data := templateData{Name: name}
	if have, err := ExistingServices(root); err == nil {
		data.Services, _, _ = ResolveServices(have)
	} else {
		data.Services = DefaultServices
	}
	data.All = append([]Service{Proxy}, data.Services...)
	return writeAgentFiles(root, data)
}

func writeAgentFiles(root string, data templateData) ([]string, error) {
	var wrote []string
	for _, f := range AgentFiles() {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return wrote, err
		}
		out, err := render(f.Template, data)
		if err != nil {
			return wrote, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return wrote, err
		}
		wrote = append(wrote, f.Path)
	}
	return wrote, nil
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
