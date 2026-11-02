// Package mcp works out where the MCP servers that a project's repos declare
// in neptuno.yml answer, and writes them into Claude Code's config files: the
// project's .mcp.json for Claude on the host, and a config for Claude inside
// the repos' containers.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/proxy"
)

// ProjectFile is the project-scoped config Claude Code reads when it starts
// in the project's root folder.
const ProjectFile = ".mcp.json"

// ContainerFile is the config for Claude inside the repos' containers,
// under .neptuno/claude, which compose mounts at /root/.claude.
const ContainerFile = "neptuno-mcp.json"

// Server is one MCP server a repo declares.
type Server struct {
	Repo string
	Name string
	// Path is where the server answers, such as /_hyperdrive/mcp.
	Path string
	// HostURL reaches it from the host through the reverse proxy, such as
	// http://locke.localhost/_hyperdrive/mcp. It is "" when the Caddyfile
	// has no site for the repo.
	HostURL string
	// ContainerURL reaches it from other containers on the compose network,
	// such as http://locke:3000/_hyperdrive/mcp, where *.localhost names
	// point back at the container itself.
	ContainerURL string
}

// ID is the server's name in Claude's config: <repo>-<name>, or just the
// repo when the server is named after it.
func (s Server) ID() string {
	if s.Name == s.Repo {
		return s.Repo
	}
	return s.Repo + "-" + s.Name
}

// Servers lists every MCP server declared in neptuno.yml, sorted by ID, with
// the URLs it answers on. published maps the proxy container's ports to the
// host ports they are published on, as for proxy.URLs. Problems, such as a
// repo without a Caddyfile site, come back as warnings and leave that
// server's URLs empty.
func Servers(p *project.Project, sites []proxy.Site, published map[int]int) ([]Server, []string) {
	var out []Server
	var warnings []string
	for name, cfg := range p.Config.Repos {
		if len(cfg.MCP) == 0 {
			continue
		}
		if _, ok := p.Services[name]; !ok && !p.IsRepo(name) {
			warnings = append(warnings, fmt.Sprintf("neptuno.yml declares MCP servers for %s, which is not a repo or a compose service", name))
			continue
		}
		host := hostBase(sites, name, published)
		container := containerBase(sites, name)
		if host == "" && container == "" {
			warnings = append(warnings, fmt.Sprintf("the Caddyfile has no site for %s, so its MCP servers have no URL; add one with `neptuno add %s`", name, name))
		}
		for server, path := range cfg.MCP {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			s := Server{Repo: name, Name: server, Path: path}
			if host != "" {
				s.HostURL = host + path
			}
			if container != "" {
				s.ContainerURL = container + path
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	sort.Strings(warnings)
	return out, warnings
}

// ForRepo picks out one repo's servers.
func ForRepo(servers []Server, repo string) []Server {
	var out []Server
	for _, s := range servers {
		if s.Repo == repo {
			out = append(out, s)
		}
	}
	return out
}

// hostBase is the first URL the repo answers on through the proxy that can
// be opened as is, without a trailing slash.
func hostBase(sites []proxy.Site, repo string, published map[int]int) string {
	for _, l := range proxy.URLs(sites, repo, published) {
		if !l.Pattern {
			return strings.TrimSuffix(l.URL, "/")
		}
	}
	return ""
}

// containerBase is what the proxy dials for the repo, such as
// http://locke:3000, which other containers can dial too: an upstream named
// after the repo first, else the first upstream of the repo's
// <repo>.localhost site, such as host.docker.internal:8000 for a repo run
// on the host.
func containerBase(sites []proxy.Site, repo string) string {
	var fallback string
	for _, s := range sites {
		for i, u := range s.Upstreams {
			if u == repo && i < len(s.Dials) {
				return "http://" + withPort(s.Dials[i])
			}
		}
		if fallback != "" || len(s.Dials) == 0 {
			continue
		}
		for _, a := range s.Addresses {
			if siteHost(a) == repo+".localhost" {
				fallback = "http://" + withPort(s.Dials[0])
			}
		}
	}
	return fallback
}

func withPort(dial string) string {
	if _, _, err := net.SplitHostPort(dial); err == nil {
		return dial
	}
	return dial + ":80"
}

func siteHost(addr string) string {
	if _, rest, ok := strings.Cut(addr, "://"); ok {
		addr = rest
	}
	addr, _, _ = strings.Cut(addr, "/")
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// entry is one server in Claude Code's mcpServers.
type entry struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// WriteProject updates the project's .mcp.json with the servers' host URLs.
// It replaces the servers it wrote before, listed in managed, and keeps
// every other server and key in the file. It returns the IDs it wrote, for
// the next call's managed, and whether the file changed. With no servers
// and no file it writes nothing.
func WriteProject(root string, servers []Server, managed []string) ([]string, bool, error) {
	path := filepath.Join(root, ProjectFile)
	doc := map[string]json.RawMessage{}
	old, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		old = nil
	case err != nil:
		return managed, false, err
	default:
		if err := json.Unmarshal(old, &doc); err != nil {
			return managed, false, fmt.Errorf("%s: %w", ProjectFile, err)
		}
	}
	list := map[string]json.RawMessage{}
	if raw, ok := doc["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &list); err != nil {
			return managed, false, fmt.Errorf("%s: mcpServers: %w", ProjectFile, err)
		}
	}
	for _, id := range managed {
		delete(list, id)
	}
	var wrote []string
	for _, s := range servers {
		if s.HostURL == "" {
			continue
		}
		b, _ := json.Marshal(entry{Type: "http", URL: s.HostURL})
		list[s.ID()] = b
		wrote = append(wrote, s.ID())
	}
	if old == nil && len(wrote) == 0 {
		return nil, false, nil
	}
	b, err := json.Marshal(list)
	if err != nil {
		return managed, false, err
	}
	doc["mcpServers"] = b
	changed, err := writeJSON(path, doc, old)
	return wrote, changed, err
}

// WriteContainer writes the config for Claude inside the containers, with
// the servers' container URLs, to dir/neptuno-mcp.json. Neptuno owns the
// file, so it is rewritten whole.
func WriteContainer(dir string, servers []Server) (bool, error) {
	list := map[string]entry{}
	for _, s := range servers {
		if s.ContainerURL != "" {
			list[s.ID()] = entry{Type: "http", URL: s.ContainerURL}
		}
	}
	path := filepath.Join(dir, ContainerFile)
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	return writeJSON(path, map[string]any{"mcpServers": list}, old)
}

// writeJSON writes v indented, unless the file already holds the same.
func writeJSON(path string, v any, old []byte) (bool, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return false, err
	}
	if old != nil && bytes.Equal(normalize(old), normalize(buf.Bytes())) {
		return false, nil
	}
	return true, os.WriteFile(path, buf.Bytes(), 0o644)
}

// normalize re-indents JSON so files that differ only in layout compare
// equal.
func normalize(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, _ := json.Marshal(v)
	return out
}

// ProjectFileCurrent reports whether the project's .mcp.json has every
// server with a host URL, at that URL.
func ProjectFileCurrent(root string, servers []Server) bool {
	b, err := os.ReadFile(filepath.Join(root, ProjectFile))
	if err != nil {
		return false
	}
	var doc struct {
		MCPServers map[string]entry `json:"mcpServers"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return false
	}
	for _, s := range servers {
		if s.HostURL != "" && doc.MCPServers[s.ID()].URL != s.HostURL {
			return false
		}
	}
	return true
}
