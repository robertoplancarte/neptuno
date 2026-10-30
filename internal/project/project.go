// Package project loads a Neptuno project from disk: its root folder,
// neptuno.yml, the services in compose.yml and the repos under repos/.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// ErrNotFound is returned when no Neptuno project encloses a directory.
var ErrNotFound = errors.New("not inside a Neptuno project (no neptuno.yml or compose.yml with a repos/ folder found)")

// Run modes for a repo.
const (
	RunContainer = "container"
	RunHost      = "host"
)

// RepoConfig is the per-repo section of neptuno.yml.
type RepoConfig struct {
	// Run is "container" (default) or "host".
	Run string `yaml:"run"`
	// DependsOn lists services a host-mode repo needs. Container repos
	// declare dependencies with depends_on in compose.yml instead.
	DependsOn []string `yaml:"depends_on"`
}

// Hooks are scripts run at lifecycle points, relative to the project root.
type Hooks struct {
	BeforeUp   string `yaml:"before_up"`
	AfterUp    string `yaml:"after_up"`
	BeforeDown string `yaml:"before_down"`
}

// Config is neptuno.yml merged with neptuno.local.yml.
type Config struct {
	DefaultEnv string                `yaml:"default_env"`
	Repos      map[string]RepoConfig `yaml:"repos"`
	Hooks      Hooks                 `yaml:"hooks"`
}

// Service is a compose service as far as Neptuno cares about it.
type Service struct {
	Name      string
	DependsOn []string
}

// Project is a loaded Neptuno project.
type Project struct {
	Root   string
	Name   string
	Config Config
	// Services holds every compose service by name.
	Services map[string]Service
	// Repos lists the folder names under repos/, sorted.
	Repos []string
}

// Find walks up from dir to the nearest project root.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if exists(filepath.Join(dir, "neptuno.yml")) ||
			(exists(filepath.Join(dir, "compose.yml")) && isDir(filepath.Join(dir, "repos"))) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

// Load finds the project enclosing dir and reads its configuration.
func Load(dir string) (*Project, error) {
	root, err := Find(dir)
	if err != nil {
		return nil, err
	}
	p := &Project{Root: root, Name: filepath.Base(root)}

	for _, name := range []string{"neptuno.yml", "neptuno.local.yml"} {
		if err := mergeYAML(filepath.Join(root, name), &p.Config); err != nil {
			return nil, err
		}
	}
	if p.Config.DefaultEnv == "" {
		p.Config.DefaultEnv = "local"
	}

	if err := p.loadCompose(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(filepath.Join(root, "repos"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() && e.Name()[0] != '.' {
			p.Repos = append(p.Repos, e.Name())
		}
	}
	sort.Strings(p.Repos)
	return p, nil
}

func mergeYAML(path string, into any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, into); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// composeFile is the subset of compose.yml that Neptuno reads.
type composeFile struct {
	Name     string `yaml:"name"`
	Services map[string]struct {
		DependsOn dependsOn `yaml:"depends_on"`
	} `yaml:"services"`
}

// dependsOn accepts both the list and the map form of depends_on.
type dependsOn []string

func (d *dependsOn) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return err
		}
		*d = list
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			*d = append(*d, n.Content[i].Value)
		}
	default:
		return fmt.Errorf("line %d: depends_on must be a list or a map", n.Line)
	}
	return nil
}

func (p *Project) loadCompose() error {
	p.Services = map[string]Service{}
	var cf composeFile
	for _, name := range []string{"compose.yml", "compose.override.yml"} {
		var part composeFile
		if err := mergeYAML(filepath.Join(p.Root, name), &part); err != nil {
			return err
		}
		if part.Name != "" {
			cf.Name = part.Name
		}
		for svc, def := range part.Services {
			s := p.Services[svc]
			s.Name = svc
			s.DependsOn = appendUnique(s.DependsOn, def.DependsOn...)
			p.Services[svc] = s
		}
	}
	if cf.Name != "" {
		p.Name = cf.Name
	}
	return nil
}

// IsRepo reports whether name is a repo known to Neptuno.
func (p *Project) IsRepo(name string) bool {
	for _, r := range p.Repos {
		if r == name {
			return true
		}
	}
	return false
}

// RunMode returns how a repo runs: in its container or on the host.
func (p *Project) RunMode(repo string) string {
	if p.Config.Repos[repo].Run == RunHost {
		return RunHost
	}
	return RunContainer
}

// Selectable lists everything `neptuno up` can start: repos first, then the
// remaining compose services, each group sorted.
func (p *Project) Selectable() (repos, services []string) {
	for _, r := range p.Repos {
		if _, inCompose := p.Services[r]; inCompose || p.RunMode(r) == RunHost {
			repos = append(repos, r)
		}
	}
	for name := range p.Services {
		if !p.IsRepo(name) {
			services = append(services, name)
		}
	}
	sort.Strings(services)
	return repos, services
}

// dependencies returns the direct dependencies of a service or repo.
func (p *Project) dependencies(name string) []string {
	deps := p.Services[name].DependsOn
	if p.RunMode(name) == RunHost {
		deps = appendUnique(append([]string(nil), deps...), p.Config.Repos[name].DependsOn...)
	}
	return deps
}

// Closure returns the selected names plus everything they transitively
// depend on, in a stable order where dependencies come before dependents.
// Unknown names are an error.
func (p *Project) Closure(selected []string) ([]string, error) {
	var order []string
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var visit func(name string, from string) error
	visit = func(name, from string) error {
		if _, ok := p.Services[name]; !ok && !(p.IsRepo(name) && p.RunMode(name) == RunHost) {
			if from == "" {
				return fmt.Errorf("unknown service %q", name)
			}
			return fmt.Errorf("%s depends on unknown service %q", from, name)
		}
		switch state[name] {
		case 1:
			return fmt.Errorf("dependency cycle through %q", name)
		case 2:
			return nil
		}
		state[name] = 1
		deps := append([]string(nil), p.dependencies(name)...)
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d, name); err != nil {
				return err
			}
		}
		state[name] = 2
		order = append(order, name)
		return nil
	}
	for _, s := range selected {
		if err := visit(s, ""); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Path joins elements onto the project root.
func (p *Project) Path(elem ...string) string {
	return filepath.Join(append([]string{p.Root}, elem...)...)
}

func appendUnique(list []string, items ...string) []string {
	for _, it := range items {
		dup := false
		for _, l := range list {
			if l == it {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, it)
		}
	}
	return list
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
