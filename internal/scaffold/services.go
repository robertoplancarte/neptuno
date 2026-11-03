package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/robertoplancarte/neptuno/internal/composefile"
)

// Service is one of the shared services `neptuno init` can set up.
type Service struct {
	// Name is the compose service name.
	Name string
	// Title and Description are shown in the init menu.
	Title       string
	Description string
	// Address is how repos' containers reach it, such as postgres:5432.
	Address string
	// Hosts are how this machine reaches it, such as localhost:5432.
	Hosts []string
	// Requires lists services it can't run without.
	Requires []string
	// Compose is the service's compose.yml entry, indented two spaces per
	// level, starting with its "name:" line and optionally preceded by
	// comments.
	Compose string
	// Volumes are the named volumes it needs declared.
	Volumes []string
	// Site is its Caddyfile block, if it has one, without a final newline.
	Site string
	// Env holds the lines every repo gets in environments/_shared/base.env.
	Env string
}

// Proxy is the Caddy reverse proxy, which every project gets: it is what
// serves http://<repo>.localhost.
var Proxy = Service{
	Name:    "proxy",
	Title:   "Caddy",
	Address: "proxy:80",
	Hosts:   []string{"localhost:80", "http://<repo>.localhost"},
	Compose: `proxy:
  image: caddy:2
  ports: ["80:80"]
  volumes:
    - ./Caddyfile:/etc/caddy/Caddyfile:ro
    - caddy:/data
`,
	Volumes: []string{"caddy"},
}

// DefaultServices are the shared services the init menu offers, in order.
var DefaultServices = []Service{
	{
		Name:        "postgres",
		Title:       "PostgreSQL",
		Description: "PostgreSQL 17 database",
		Address:     "postgres:5432",
		Hosts:       []string{"localhost:5432"},
		Compose: `postgres:
  image: postgres:17
  environment:
    POSTGRES_PASSWORD: postgres
  ports: ["5432:5432"]
  volumes: ["postgres:/var/lib/postgresql/data"]
`,
		Volumes: []string{"postgres"},
		Env: `DATABASE_HOST=postgres
DATABASE_PORT=5432
DATABASE_USER=postgres
DATABASE_PASSWORD=postgres
`,
	},
	{
		Name:        "valkey",
		Title:       "Valkey",
		Description: "Valkey 8, a Redis-compatible cache and queue",
		Address:     "valkey:6379",
		Hosts:       []string{"localhost:6379"},
		Compose: `valkey:
  image: valkey/valkey:8
  ports: ["6379:6379"]
  volumes: ["valkey:/data"]
`,
		Volumes: []string{"valkey"},
		Env:     "REDIS_URL=redis://valkey:6379\n",
	},
	{
		Name:        "s3",
		Title:       "SeaweedFS S3",
		Description: "SeaweedFS, S3-compatible object storage",
		Address:     "s3:8333",
		Hosts:       []string{"localhost:8333", "http://s3.localhost"},
		Compose: `s3:
  image: chrislusf/seaweedfs
  command: server -s3 -dir=/data
  ports: ["8333:8333"]
  volumes: ["s3:/data"]
`,
		Volumes: []string{"s3"},
		Site:    "http://s3.localhost {\n\treverse_proxy s3:8333\n}",
		Env:     "S3_ENDPOINT=http://s3:8333\n",
	},
}

// DefaultServiceNames returns the names of DefaultServices, in order.
func DefaultServiceNames() []string {
	names := make([]string, len(DefaultServices))
	for i, s := range DefaultServices {
		names[i] = s.Name
	}
	return names
}

// ErrUnknownService is returned for a name that isn't in DefaultServices.
var ErrUnknownService = errors.New("unknown service")

// ResolveServices checks names against DefaultServices and adds the
// services they require. It returns the services in menu order, and the
// names that were added only because something chosen needs them.
func ResolveServices(names []string) (chosen []Service, required []string, err error) {
	want := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if _, ok := lookupService(n); !ok {
			return nil, nil, fmt.Errorf("%q: %w (choose from %s)", n, ErrUnknownService, strings.Join(DefaultServiceNames(), ", "))
		}
		want[n] = true
	}
	asked := map[string]bool{}
	for n := range want {
		asked[n] = true
	}
	// Requirements are one level deep today; loop until nothing changes so
	// deeper chains keep working.
	for changed := true; changed; {
		changed = false
		for n := range want {
			s, _ := lookupService(n)
			for _, r := range s.Requires {
				if !want[r] {
					want[r], changed = true, true
				}
			}
		}
	}
	for _, s := range DefaultServices {
		if want[s.Name] {
			chosen = append(chosen, s)
			if !asked[s.Name] {
				required = append(required, s.Name)
			}
		}
	}
	return chosen, required, nil
}

func lookupService(name string) (Service, bool) {
	for _, s := range DefaultServices {
		if s.Name == name {
			return s, true
		}
	}
	return Service{}, false
}

// composeLines renders a service's compose entry with indent as one level.
func (s Service) composeLines(indent string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s.Compose, "\n"), "\n") {
		trimmed := strings.TrimLeft(l, " ")
		levels := (len(l) - len(trimmed)) / 2
		out = append(out, strings.Repeat(indent, levels+1)+trimmed)
	}
	return out
}

// ComposeBlock is the service's compose entry, indented under services:.
func (s Service) ComposeBlock() string {
	return strings.Join(s.composeLines("  "), "\n") + "\n"
}

// ExistingServices returns the names of the DefaultServices that the
// project's compose.yml already defines.
func ExistingServices(root string) ([]string, error) {
	src, err := os.ReadFile(filepath.Join(root, "compose.yml"))
	if err != nil {
		return nil, err
	}
	var cf struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(src, &cf); err != nil {
		return nil, fmt.Errorf("compose.yml: %w", err)
	}
	var out []string
	for _, s := range DefaultServices {
		if _, ok := cf.Services[s.Name]; ok {
			out = append(out, s.Name)
		}
	}
	return out, nil
}

// AddServices adds services to an existing project at root: their entries
// and volumes in compose.yml, their sites in the Caddyfile and their
// variables in environments/_shared/base.env. Services compose.yml already
// defines are left alone, as is everything else in those files. It returns
// what it did, one line per change.
func AddServices(root string, names []string) ([]string, error) {
	chosen, _, err := ResolveServices(names)
	if err != nil {
		return nil, err
	}
	have, err := ExistingServices(root)
	if err != nil {
		return nil, err
	}

	composePath := filepath.Join(root, "compose.yml")
	compose, err := os.ReadFile(composePath)
	if err != nil {
		return nil, err
	}
	var todo []Service
	var log []string
	for _, s := range chosen {
		if slices.Contains(have, s.Name) {
			log = append(log, "kept    compose.yml service "+s.Name)
			continue
		}
		if compose, err = composefile.InsertService(compose, s.Name, s.composeLines); err != nil {
			return nil, err
		}
		if compose, err = composefile.DeclareVolumes(compose, s.Volumes); err != nil {
			return nil, err
		}
		todo = append(todo, s)
		log = append(log, "added   compose.yml service "+s.Name)
	}
	if len(todo) == 0 {
		return log, nil
	}
	if err := os.WriteFile(composePath, compose, 0o644); err != nil {
		return nil, err
	}

	if added, err := appendCaddySites(filepath.Join(root, "Caddyfile"), todo); err != nil {
		return log, err
	} else {
		log = append(log, added...)
	}
	added, err := appendEnv(filepath.Join(root, "environments", "_shared", "base.env"), todo)
	log = append(log, added...)
	return log, err
}

// appendCaddySites adds the services' sites to the Caddyfile unless it has
// them, or does nothing when the project has no Caddyfile.
func appendCaddySites(path string, services []Service) ([]string, error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var log []string
	text := string(src)
	for _, s := range services {
		if s.Site == "" {
			continue
		}
		host, _, _ := strings.Cut(s.Site, " ")
		if strings.Contains(text, host) {
			continue
		}
		text = strings.TrimRight(text, "\n")
		if text != "" {
			text += "\n\n"
		}
		text += s.Site + "\n"
		log = append(log, "added   Caddyfile site "+host)
	}
	if len(log) == 0 {
		return nil, nil
	}
	return log, os.WriteFile(path, []byte(text), 0o644)
}

// appendEnv adds each service's variables to the shared base.env, skipping
// variables the file already sets.
func appendEnv(path string, services []Service) ([]string, error) {
	src, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	set := map[string]bool{}
	for _, l := range strings.Split(string(src), "\n") {
		if k, _, ok := strings.Cut(strings.TrimSpace(l), "="); ok && !strings.HasPrefix(k, "#") {
			set[strings.TrimPrefix(k, "export ")] = true
		}
	}
	text := string(src)
	var log []string
	for _, s := range services {
		var block []string
		for _, l := range strings.Split(strings.TrimRight(s.Env, "\n"), "\n") {
			if k, _, ok := strings.Cut(l, "="); ok && set[k] {
				continue
			}
			block = append(block, l)
		}
		if !slices.ContainsFunc(block, func(l string) bool { return strings.Contains(l, "=") }) {
			continue
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += strings.Join(block, "\n") + "\n"
		log = append(log, "added   environments/_shared/base.env variables for "+s.Name)
	}
	if len(log) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return log, os.WriteFile(path, []byte(text), 0o644)
}
