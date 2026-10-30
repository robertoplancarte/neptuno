// Package stacks holds the templates `neptuno add --stack` scaffolds a repo
// from. A stack is a folder:
//
//	stack.yml    description, port, the shared services it uses, and an
//	             optional generator command run in the new container
//	Dockerfile   the development image
//	Procfile     the processes overmind runs
//	base.env     the repo's base environment
//	app/         files copied into a brand-new repo (optional); a .tmpl
//	             suffix is dropped, so app/go.mod.tmpl becomes go.mod
//
// Every file, and the generate command, is a Go text/template with .Name,
// .Project and .Port. Built-in
// stacks are embedded; a project's own stacks/<name>/ folders add to them
// and override them by name.
package stacks

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

//go:embed all:builtin
var builtin embed.FS

// Default is the stack used when none is named.
const Default = "blank"

// Stack is one loaded stack.
type Stack struct {
	Name        string
	Description string `yaml:"description"`
	// Port is the port the dev server listens on, used for the Caddy route.
	Port int `yaml:"port"`
	// Services are the shared compose services the repo depends on, when
	// they exist in compose.yml.
	Services []string `yaml:"services"`
	// Generate runs in the repo's container, in its empty folder, to create
	// a new app (such as `rails new .`). Runs after app/ is copied.
	Generate string `yaml:"generate"`
	// Source says where the stack came from: "built-in" or its folder.
	Source string `yaml:"-"`

	fsys fs.FS
}

// Data is what stack templates can use.
type Data struct {
	Name, Project string
	Port          int
}

// List returns every available stack, project stacks overriding built-ins.
func List(projectStacksDir string) ([]*Stack, error) {
	byName := map[string]*Stack{}
	sub, err := fs.Sub(builtin, "builtin")
	if err != nil {
		return nil, err
	}
	if err := loadAll(sub, "built-in", byName); err != nil {
		return nil, err
	}
	if projectStacksDir != "" {
		if info, err := os.Stat(projectStacksDir); err == nil && info.IsDir() {
			if err := loadAll(os.DirFS(projectStacksDir), projectStacksDir, byName); err != nil {
				return nil, err
			}
		}
	}
	out := make([]*Stack, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns the named stack.
func Get(projectStacksDir, name string) (*Stack, error) {
	all, err := List(projectStacksDir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range all {
		if s.Name == name {
			return s, nil
		}
		names = append(names, s.Name)
	}
	return nil, fmt.Errorf("unknown stack %q (available: %v)", name, names)
}

func loadAll(fsys fs.FS, source string, into map[string]*Stack) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(fsys, e.Name())
		if err != nil {
			return err
		}
		s := &Stack{Name: e.Name(), fsys: sub, Source: source}
		if source != "built-in" {
			s.Source = filepath.Join(source, e.Name())
		}
		b, err := fs.ReadFile(sub, "stack.yml")
		if errors.Is(err, fs.ErrNotExist) {
			continue // not a stack
		}
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(b, s); err != nil {
			return fmt.Errorf("stack %s: stack.yml: %w", e.Name(), err)
		}
		into[s.Name] = s
	}
	return nil
}

// Render renders one of the stack's top-level files. It returns
// fs.ErrNotExist when the stack does not have it.
func (s *Stack) Render(name string, data Data) ([]byte, error) {
	b, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		return nil, err
	}
	return render(s.Name+"/"+name, b, data)
}

// GenerateCommand renders the stack's generate command, or "" if it has none.
func (s *Stack) GenerateCommand(data Data) (string, error) {
	if s.Generate == "" {
		return "", nil
	}
	b, err := render(s.Name+"/stack.yml generate", []byte(s.Generate), data)
	return string(b), err
}

// AppFiles renders every file under app/, keyed by slash-separated path
// relative to app/.
func (s *Stack) AppFiles(data Data) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(s.fsys, "app", func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == "app" {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(s.fsys, p)
		if err != nil {
			return err
		}
		rendered, err := render(s.Name+"/"+p, b, data)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("app", filepath.FromSlash(p))
		out[strings.TrimSuffix(path.Clean(filepath.ToSlash(rel)), ".tmpl")] = rendered
		return nil
	})
	return out, err
}

func render(name string, src []byte, data Data) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
