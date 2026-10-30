// Package stacks holds the templates `neptuno add --stack` scaffolds a repo
// from. A stack is a folder:
//
//	stack.yml    description, port, the shared services it uses, an
//	             optional generator command run in the new container, and
//	             an optional stack it extends
//	Dockerfile   the development image
//	Procfile     the processes overmind runs
//	base.env     the repo's base environment
//	app/         files copied into a brand-new repo (optional); a .tmpl
//	             suffix is dropped, so app/go.mod.tmpl becomes go.mod
//
// Every file, and the generate command, is a Go text/template with .Name,
// .Project and .Port. Built-in stacks are embedded; a project's own
// stacks/<name>/ folders add to them and override them by name.
//
// A stack with `extends: <name>` starts from that stack: settings it leaves
// out and files it does not have come from the parent. A project stack that
// extends its own name, such as stacks/rails with `extends: rails`, extends
// the built-in it overrides.
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

// BuiltIn is the Source of stacks embedded in Neptuno.
const BuiltIn = "built-in"

// Stack is one resolved stack.
type Stack struct {
	Name        string
	Description string
	// Port is the port the dev server listens on, used for the Caddy route.
	Port int
	// Services are the shared compose services the repo depends on, when
	// they exist in compose.yml.
	Services []string
	// Generate runs in the repo's container, in its empty folder, to create
	// a new app (such as `rails new .`). Runs after app/ is copied.
	Generate string
	// Source says where the stack came from: BuiltIn or its folder.
	Source string
	// Extends names the parent stack, if any.
	Extends string

	// layers holds the stack's files, its own first, then its ancestors'.
	layers []fs.FS
}

// definition is one stack.yml as written; nil fields were left out.
type definition struct {
	Description *string   `yaml:"description"`
	Port        *int      `yaml:"port"`
	Services    *[]string `yaml:"services"`
	Generate    *string   `yaml:"generate"`
	Extends     string    `yaml:"extends"`

	name, source string
	fsys         fs.FS
}

// Data is what stack templates can use.
type Data struct {
	Name, Project string
	Port          int
}

// List returns every available stack, project stacks overriding built-ins.
func List(projectStacksDir string) ([]*Stack, error) {
	sub, err := fs.Sub(builtin, "builtin")
	if err != nil {
		return nil, err
	}
	builtins, err := loadDefinitions(sub, BuiltIn)
	if err != nil {
		return nil, err
	}
	project := map[string]*definition{}
	if projectStacksDir != "" {
		if info, err := os.Stat(projectStacksDir); err == nil && info.IsDir() {
			if project, err = loadDefinitions(os.DirFS(projectStacksDir), projectStacksDir); err != nil {
				return nil, err
			}
		}
	}

	r := resolver{builtins: builtins, project: project, done: map[*definition]*Stack{}, visiting: map[*definition]bool{}}
	var out []*Stack
	for name := range builtins {
		if _, overridden := project[name]; !overridden {
			s, err := r.resolve(builtins[name])
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
	}
	for _, def := range project {
		s, err := r.resolve(def)
		if err != nil {
			return nil, err
		}
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

func loadDefinitions(fsys fs.FS, source string) (map[string]*definition, error) {
	defs := map[string]*definition{}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		b, err := fs.ReadFile(sub, "stack.yml")
		if errors.Is(err, fs.ErrNotExist) {
			continue // not a stack
		}
		if err != nil {
			return nil, err
		}
		d := &definition{name: e.Name(), source: source, fsys: sub}
		if source != BuiltIn {
			d.source = filepath.Join(source, e.Name())
		}
		if err := yaml.Unmarshal(b, d); err != nil {
			return nil, fmt.Errorf("stack %s: stack.yml: %w", d.source, err)
		}
		defs[d.name] = d
	}
	return defs, nil
}

type resolver struct {
	builtins, project map[string]*definition
	done              map[*definition]*Stack
	visiting          map[*definition]bool
}

// parent finds the definition d extends.
func (r *resolver) parent(d *definition) (*definition, error) {
	name := d.Extends
	if d.source != BuiltIn && name == d.name {
		if p, ok := r.builtins[name]; ok {
			return p, nil
		}
		return nil, fmt.Errorf("stack %s extends %q, but there is no built-in stack by that name", d.source, name)
	}
	if p, ok := r.project[name]; ok && d.source != BuiltIn {
		return p, nil
	}
	if p, ok := r.builtins[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("stack %s extends unknown stack %q", d.source, name)
}

func (r *resolver) resolve(d *definition) (*Stack, error) {
	if s, ok := r.done[d]; ok {
		return s, nil
	}
	if r.visiting[d] {
		return nil, fmt.Errorf("stack %s: extends loops back to itself", d.source)
	}
	r.visiting[d] = true
	defer delete(r.visiting, d)

	s := &Stack{}
	if d.Extends != "" {
		p, err := r.parent(d)
		if err != nil {
			return nil, err
		}
		parent, err := r.resolve(p)
		if err != nil {
			return nil, err
		}
		*s = *parent
		s.layers = append([]fs.FS(nil), parent.layers...)
		s.Services = append([]string(nil), parent.Services...)
	}
	s.Name, s.Source, s.Extends = d.name, d.source, d.Extends
	s.layers = append([]fs.FS{d.fsys}, s.layers...)
	if d.Description != nil {
		s.Description = *d.Description
	}
	if d.Port != nil {
		s.Port = *d.Port
	}
	if d.Services != nil {
		s.Services = *d.Services
	}
	if d.Generate != nil {
		s.Generate = *d.Generate
	}
	r.done[d] = s
	return s, nil
}

// readFile returns a stack file from the nearest layer that has it.
func (s *Stack) readFile(name string) ([]byte, error) {
	for _, l := range s.layers {
		b, err := fs.ReadFile(l, name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		return b, err
	}
	return nil, fs.ErrNotExist
}

// Render renders one of the stack's top-level files. It returns
// fs.ErrNotExist when neither the stack nor its parents have it.
func (s *Stack) Render(name string, data Data) ([]byte, error) {
	b, err := s.readFile(name)
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

// Files returns the stack's template files as written, merged across its
// parents, keyed by slash-separated path. stack.yml is left out.
func (s *Stack) Files() (map[string][]byte, error) {
	out := map[string][]byte{}
	for i := len(s.layers) - 1; i >= 0; i-- {
		l := s.layers[i]
		err := fs.WalkDir(l, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || p == "stack.yml" {
				return err
			}
			b, err := fs.ReadFile(l, p)
			if err != nil {
				return err
			}
			out[p] = b
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AppFiles renders every file under app/, merged across parents, keyed by
// slash-separated path relative to app/.
func (s *Stack) AppFiles(data Data) (map[string][]byte, error) {
	files, err := s.Files()
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for p, b := range files {
		rel, ok := strings.CutPrefix(p, "app/")
		if !ok {
			continue
		}
		rendered, err := render(s.Name+"/"+p, b, data)
		if err != nil {
			return nil, err
		}
		out[strings.TrimSuffix(path.Clean(rel), ".tmpl")] = rendered
	}
	return out, nil
}

// Copy writes the stack, merged with its parents, into dir as a standalone
// stack folder. It refuses to overwrite an existing folder.
func (s *Stack) Copy(dir string) ([]string, error) {
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%s already exists", dir)
	}
	files, err := s.Files()
	if err != nil {
		return nil, err
	}
	def := struct {
		Description string   `yaml:"description,omitempty"`
		Port        int      `yaml:"port,omitempty"`
		Services    []string `yaml:"services,flow"`
		Generate    string   `yaml:"generate,omitempty"`
	}{s.Description, s.Port, s.Services, s.Generate}
	yml, err := yaml.Marshal(def)
	if err != nil {
		return nil, err
	}
	files["stack.yml"] = yml

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		target := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, files[p], 0o644); err != nil {
			return nil, err
		}
	}
	return paths, nil
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
