// Package stacks holds the templates `neptuno add --stack` scaffolds a repo
// from. A stack is a folder:
//
//	stack.yml    description, port, the shared services it uses, an
//	             optional generator command run in the new container,
//	             extra compose volumes, and an optional stack it extends
//	Dockerfile   the development image
//	entrypoint.sh  setup run at container start, such as bundle install
//	             (optional); Procfile processes wait for it
//	Procfile     the processes overmind runs
//	base.env     the repo's base environment
//	app/         files copied into a brand-new repo (optional); a .tmpl
//	             suffix is dropped, so app/go.mod.tmpl becomes go.mod
//
// stack.yml can also declare options, such as Tailwind for rails, that
// `neptuno add` offers as a checklist. Each selected option adds generator
// flags, commands run after the generator, Procfile lines, base.env lines and
// shared services.
//
// Every file, the generate command and every option field is a Go
// text/template with .Name, .Project, .Port and .Options, which maps each
// option's name to whether it was selected. Built-in stacks are embedded; a project's own
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
	// Volumes are extra compose volumes for the repo's service, such as
	// "{{.Name}}_bundle:/usr/local/bundle" to keep installed gems across
	// container rebuilds. Named volumes are declared in compose.yml too.
	Volumes []string
	// Source says where the stack came from: BuiltIn or its folder.
	Source string
	// Extends names the parent stack, if any.
	Extends string
	// Options are the choices `neptuno add` offers, sorted by name.
	Options []*Option

	// layers holds the stack's files, its own first, then its ancestors'.
	layers []fs.FS
}

// Option is one choice a stack offers, such as Tailwind for rails.
type Option struct {
	Name        string `yaml:"-"`
	Description string `yaml:"description,omitempty"`
	// Default options start selected.
	Default bool `yaml:"default,omitempty"`
	// GenerateFlags are appended to the stack's generate command.
	GenerateFlags string `yaml:"generate_flags,omitempty"`
	// AfterGenerate runs in the same container right after the generator.
	AfterGenerate string `yaml:"after_generate,omitempty"`
	// Procfile and Env are lines appended to the Procfile and base.env.
	Procfile []string `yaml:"procfile,omitempty"`
	Env      []string `yaml:"env,omitempty"`
	// Services are shared compose services the repo then depends on.
	Services []string `yaml:"services,omitempty,flow"`
}

// definition is one stack.yml as written; nil fields were left out.
type definition struct {
	Description *string            `yaml:"description"`
	Port        *int               `yaml:"port"`
	Services    *[]string          `yaml:"services"`
	Generate    *string            `yaml:"generate"`
	Volumes     *[]string          `yaml:"volumes"`
	Extends     string             `yaml:"extends"`
	Options     map[string]*Option `yaml:"options"`

	name, source string
	fsys         fs.FS
}

// Data is what stack templates can use.
type Data struct {
	Name, Project string
	Port          int
	// Options maps every option of the stack to whether it is selected.
	Options map[string]bool
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
		s.Volumes = append([]string(nil), parent.Volumes...)
		s.Options = append([]*Option(nil), parent.Options...)
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
	if d.Volumes != nil {
		s.Volumes = *d.Volumes
	}
	// An option replaces the parent's option of the same name.
	for name, o := range d.Options {
		if o == nil {
			o = &Option{}
		}
		o.Name = name
		replaced := false
		for i, po := range s.Options {
			if po.Name == name {
				s.Options[i], replaced = o, true
			}
		}
		if !replaced {
			s.Options = append(s.Options, o)
		}
	}
	sort.Slice(s.Options, func(i, j int) bool { return s.Options[i].Name < s.Options[j].Name })
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

// Select returns the data for the stack with the default options, plus
// with, minus without. Unknown option names are an error.
func (s *Stack) Select(data Data, with, without []string) (Data, error) {
	data.Options = map[string]bool{}
	for _, o := range s.Options {
		data.Options[o.Name] = o.Default
	}
	for _, list := range []struct {
		names []string
		on    bool
	}{{with, true}, {without, false}} {
		for _, name := range list.names {
			if _, ok := data.Options[name]; !ok {
				return data, fmt.Errorf("the %s stack has no option %q (options: %s)", s.Name, name, strings.Join(s.OptionNames(), ", "))
			}
			data.Options[name] = list.on
		}
	}
	return data, nil
}

// OptionNames lists the stack's options.
func (s *Stack) OptionNames() []string {
	var names []string
	for _, o := range s.Options {
		names = append(names, o.Name)
	}
	return names
}

// selected returns the options data selects, in order.
func (s *Stack) selected(data Data) []*Option {
	var out []*Option
	for _, o := range s.Options {
		if data.Options[o.Name] {
			out = append(out, o)
		}
	}
	return out
}

// Render renders one of the stack's top-level files, with the Procfile and
// env lines of the selected options appended to Procfile and base.env. It
// returns fs.ErrNotExist when neither the stack nor its parents have it.
func (s *Stack) Render(name string, data Data) ([]byte, error) {
	data = withOptions(s, data)
	b, err := s.readFile(name)
	if err != nil {
		return nil, err
	}
	out, err := render(s.Name+"/"+name, b, data)
	if err != nil {
		return nil, err
	}
	for _, o := range s.selected(data) {
		lines := map[string][]string{"Procfile": o.Procfile, "base.env": o.Env}[name]
		if len(lines) == 0 {
			continue
		}
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		if name == "base.env" {
			out = append(out, fmt.Sprintf("# From the %s option\n", o.Name)...)
		}
		for _, l := range lines {
			r, err := render(s.Name+"/stack.yml options."+o.Name, []byte(l), data)
			if err != nil {
				return nil, err
			}
			out = append(append(out, r...), '\n')
		}
	}
	return out, nil
}

// GenerateCommand renders the stack's generate command, with the selected
// options' generator flags and after_generate commands, or "" if the stack
// has no generator.
func (s *Stack) GenerateCommand(data Data) (string, error) {
	if s.Generate == "" {
		return "", nil
	}
	data = withOptions(s, data)
	command := strings.TrimSpace(s.Generate)
	var after []string
	for _, o := range s.selected(data) {
		if o.GenerateFlags != "" {
			command += " " + strings.TrimSpace(o.GenerateFlags)
		}
		if o.AfterGenerate != "" {
			after = append(after, "("+strings.TrimSpace(o.AfterGenerate)+")")
		}
	}
	if len(after) > 0 {
		command = "(" + command + ") && " + strings.Join(after, " && ")
	}
	b, err := render(s.Name+"/stack.yml generate", []byte(command), data)
	return string(b), err
}

// ServicesFor returns the stack's shared services plus those of the
// selected options.
func (s *Stack) ServicesFor(data Data) []string {
	data = withOptions(s, data)
	out := append([]string(nil), s.Services...)
	seen := map[string]bool{}
	for _, svc := range out {
		seen[svc] = true
	}
	for _, o := range s.selected(data) {
		for _, svc := range o.Services {
			if !seen[svc] {
				seen[svc] = true
				out = append(out, svc)
			}
		}
	}
	return out
}

// VolumesFor renders the stack's volumes.
func (s *Stack) VolumesFor(data Data) ([]string, error) {
	data = withOptions(s, data)
	var out []string
	for _, v := range s.Volumes {
		b, err := render(s.Name+"/stack.yml volumes", []byte(v), data)
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	return out, nil
}

// withOptions fills in the default selection when data has none, so
// templates can always read .Options.<name>.
func withOptions(s *Stack, data Data) Data {
	if data.Options == nil {
		data, _ = s.Select(data, nil, nil)
	}
	return data
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
	data = withOptions(s, data)
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
	options := map[string]*Option{}
	for _, o := range s.Options {
		options[o.Name] = o
	}
	def := struct {
		Description string             `yaml:"description,omitempty"`
		Port        int                `yaml:"port,omitempty"`
		Services    []string           `yaml:"services,flow"`
		Generate    string             `yaml:"generate,omitempty"`
		Volumes     []string           `yaml:"volumes,omitempty"`
		Options     map[string]*Option `yaml:"options,omitempty"`
	}{s.Description, s.Port, s.Services, s.Generate, s.Volumes, options}
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
