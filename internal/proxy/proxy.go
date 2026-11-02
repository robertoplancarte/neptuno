// Package proxy reads the project's Caddyfile to find where each service is
// reachable through the reverse proxy.
package proxy

import (
	"net"
	"strconv"
	"strings"
)

// Site is one site block of a Caddyfile.
type Site struct {
	// Addresses are the block's site addresses, such as
	// http://web.localhost or http://*.web.localhost.
	Addresses []string
	// Upstreams are the hosts its reverse_proxy directives dial, such as
	// web for reverse_proxy web:3000.
	Upstreams []string
	// Dials are the same upstreams with their ports, as the proxy dials
	// them on the compose network, such as web:3000.
	Dials []string
}

// Parse reads the site blocks of a Caddyfile. It understands the shape
// `neptuno init` and `neptuno add` write, plus reverse_proxy inside nested
// blocks such as handle; it skips the global options block and snippets.
func Parse(src string) []Site {
	var sites []Site
	var cur *Site
	depth := 0
	for _, line := range strings.Split(src, "\n") {
		f := fields(line)
		if len(f) == 0 {
			continue
		}
		opens := f[len(f)-1] == "{"
		if depth == 0 && opens {
			addrs := splitAddresses(f[:len(f)-1])
			if len(addrs) > 0 && !strings.HasPrefix(addrs[0], "(") {
				sites = append(sites, Site{Addresses: addrs})
				cur = &sites[len(sites)-1]
			} else {
				cur = nil // global options or a snippet
			}
		} else if cur != nil && f[0] == "reverse_proxy" {
			hosts, dials := upstreams(f[1:])
			cur.Upstreams = append(cur.Upstreams, hosts...)
			cur.Dials = append(cur.Dials, dials...)
		}
		for _, t := range f {
			switch t {
			case "{":
				depth++
			case "}":
				depth = max(0, depth-1)
			}
		}
		if depth == 0 {
			cur = nil
		}
	}
	return sites
}

// fields splits a line into tokens, dropping a trailing comment and
// separating braces stuck to other tokens, such as `}` or `site{`.
func fields(line string) []string {
	if i := commentStart(line); i >= 0 {
		line = line[:i]
	}
	var out []string
	for _, t := range strings.Fields(line) {
		if t != "{" && strings.HasSuffix(t, "{") {
			out = append(out, strings.TrimSuffix(t, "{"), "{")
			continue
		}
		out = append(out, t)
	}
	return out
}

// commentStart is where a # comment begins: at the line's start or after
// whitespace, so a # inside a URL fragment is kept.
func commentStart(line string) int {
	for i, r := range line {
		if r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return i
		}
	}
	return -1
}

func splitAddresses(tokens []string) []string {
	var out []string
	for _, t := range tokens {
		for _, a := range strings.Split(t, ",") {
			if a = strings.TrimSpace(a); a != "" {
				out = append(out, a)
			}
		}
	}
	return out
}

// upstreams reads reverse_proxy's arguments, skipping a matcher such as
// /api/* or @name and stopping at its options block. It returns each
// upstream's host and its host:port.
func upstreams(args []string) (hosts, dials []string) {
	for i, a := range args {
		if a == "{" {
			break
		}
		if i == 0 && (strings.HasPrefix(a, "/") || strings.HasPrefix(a, "@") || a == "*") {
			continue
		}
		if _, rest, ok := strings.Cut(a, "://"); ok {
			a = rest
		}
		host := a
		if h, _, err := net.SplitHostPort(a); err == nil {
			host = h
		}
		if host != "" {
			hosts, dials = append(hosts, host), append(dials, a)
		}
	}
	return hosts, dials
}

// Link is one place a service answers.
type Link struct {
	// URL is what to open, such as http://web.localhost.
	URL string
	// Pattern is true for a wildcard address, such as http://*.web.localhost,
	// which names many hosts and cannot be opened as is.
	Pattern bool
}

// URLs lists where service is reachable: every address of a site that
// proxies to it, or that is <service>.localhost, such as a host-run repo
// proxied through host.docker.internal. published maps the proxy
// container's ports to the host ports they are published on, such as
// 80 to 8080; a port it leaves out is taken to be published as is.
func URLs(sites []Site, service string, published map[int]int) []Link {
	var out []Link
	seen := map[string]bool{}
	for _, s := range sites {
		match := false
		for _, u := range s.Upstreams {
			match = match || u == service
		}
		for _, a := range s.Addresses {
			match = match || hostOf(a) == service+".localhost"
		}
		if !match {
			continue
		}
		for _, a := range s.Addresses {
			l := link(a, published)
			if l.URL != "" && !seen[l.URL] {
				seen[l.URL] = true
				out = append(out, l)
			}
		}
	}
	return out
}

func hostOf(addr string) string {
	if _, rest, ok := strings.Cut(addr, "://"); ok {
		addr = rest
	}
	addr, _, _ = strings.Cut(addr, "/")
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// link turns a site address into a URL the way Caddy serves it: http://
// addresses on port 80, others on HTTPS at 443, a bare :port on localhost.
func link(addr string, published map[int]int) Link {
	scheme, rest, ok := strings.Cut(addr, "://")
	if !ok {
		scheme, rest = "", addr
	}
	hostport, path, _ := strings.Cut(rest, "/")
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		host, portStr = hostport, ""
	}
	port := 0
	if portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return Link{}
		}
		port = p
	}
	if scheme == "" {
		// Caddy only serves HTTPS when it knows the host name.
		scheme = "https"
		if port == 80 || host == "" && port != 443 {
			scheme = "http"
		}
	}
	if host == "" {
		host = "localhost"
	}
	if port == 0 {
		port = 80
		if scheme == "https" {
			port = 443
		}
	}
	if p, ok := published[port]; ok {
		port = p
	}
	u := scheme + "://" + host
	if !(scheme == "http" && port == 80 || scheme == "https" && port == 443) {
		u += ":" + strconv.Itoa(port)
	}
	if path != "" {
		u += "/" + strings.TrimSuffix(path, "*")
	}
	return Link{URL: u, Pattern: strings.Contains(host, "*")}
}
