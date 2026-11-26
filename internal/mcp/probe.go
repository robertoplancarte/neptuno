package mcp

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Health is what a quick request to an MCP server's URL found.
type Health string

const (
	// Up means something answers at the URL.
	Up Health = "up"
	// NotFound means the app answers but has nothing at the path.
	NotFound Health = "not found"
	// Down means nothing answers: the proxy has no upstream or the app is
	// not listening.
	Down Health = "down"
)

// initialize is the first request an MCP client sends, which a streamable
// HTTP server answers without a session.
const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"neptuno","version":"probe"}}}`

// sessionHeader carries the session a streamable HTTP server opened for an
// initialize, which the client ends with a DELETE.
const sessionHeader = "Mcp-Session-Id"

// maxDrain is how much of an answer a probe reads before closing it. A body
// read to the end lets the connection be reused; one longer than this is
// closed with its connection instead.
const maxDrain = 64 << 10

// Probe sends an MCP initialize request to url and sorts the answer: any
// reply but a 404 or the proxy's 502, 503 or 504 counts as up, since servers
// answer a client they don't know in many ways. A server that opens a
// session for it gets a DELETE that ends the session again, so probing
// leaves nothing behind on the server.
//
// A GET or OPTIONS would be lighter, but an app that routes only POST to
// its server, as a Rails route often does, answers those with a 404, which
// would read as nothing at the path.
func Probe(ctx context.Context, client *http.Client, url string) Health {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(initialize))
	if err != nil {
		return Down
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return Down
	}
	discard(resp)
	if id := resp.Header.Get(sessionHeader); id != "" {
		endSession(ctx, client, url, id)
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return NotFound
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return Down
	}
	return Up
}

// endSession ends the session id that an initialize to url opened. Servers
// that don't allow it answer 405, which is fine too.
func endSession(ctx context.Context, client *http.Client, url, id string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return
	}
	req.Header.Set(sessionHeader, id)
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	if resp, err := client.Do(req); err == nil {
		discard(resp)
	}
}

// discard reads what is left of a short answer and closes it, so its
// keep-alive connection goes back to the client's pool.
func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain))
	resp.Body.Close()
}

// probeClient is the one client every probe shares, so their keep-alive
// connections are reused instead of left open in a transport per probe.
var probeClient = func() *http.Client {
	c := HTTPClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t := c.Transport.(*http.Transport)
	// Connections the dashboard won't use again for a while are closed
	// rather than held open on the proxy.
	t.IdleConnTimeout = 10 * time.Second
	return c
}()

// ProbeAll probes the host URLs of servers at once, waiting at most timeout,
// and returns each server's health by ID. Servers without a host URL are
// left out.
func ProbeAll(servers []Server, timeout time.Duration) map[string]Health {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out := map[string]Health{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, s := range servers {
		if s.HostURL == "" {
			continue
		}
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			h := Probe(ctx, probeClient, s.HostURL)
			mu.Lock()
			out[s.ID()] = h
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return out
}

// Prober probes servers no more often than MaxAge, for a view that redraws
// often, such as the dashboard: it remembers what each server's last probe
// found, and probes it again once that is MaxAge old, when the state of the
// service that runs it changes, or after Forget.
type Prober struct {
	// MaxAge is how long a probe's answer holds.
	MaxAge time.Duration
	// Timeout bounds each round of probes.
	Timeout time.Duration
	// Now tells the time. Nil uses time.Now.
	Now func() time.Time

	probe func([]Server, time.Duration) map[string]Health

	mu    sync.Mutex
	cache map[string]probed
}

type probed struct {
	health Health
	at     time.Time
	// url and state are what the server's URL and its service's state
	// were when probed.
	url, state string
}

// NewProber returns a Prober whose answers hold for maxAge.
func NewProber(maxAge, timeout time.Duration) *Prober {
	return &Prober{MaxAge: maxAge, Timeout: timeout, probe: ProbeAll, cache: map[string]probed{}}
}

// Check returns each server's health by ID, probing only the servers whose
// last answer is too old or whose service's state, from states by server
// ID, has changed since. Servers without a host URL are left out.
func (p *Prober) Check(servers []Server, states map[string]string) map[string]Health {
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	p.mu.Lock()
	out := map[string]Health{}
	var stale []Server
	for _, s := range servers {
		if s.HostURL == "" {
			continue
		}
		c, ok := p.cache[s.ID()]
		if ok && c.url == s.HostURL && c.state == states[s.ID()] && now.Sub(c.at) < p.MaxAge {
			out[s.ID()] = c.health
			continue
		}
		stale = append(stale, s)
	}
	p.mu.Unlock()
	if len(stale) == 0 {
		return out
	}
	fresh := p.probe(stale, p.Timeout)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range stale {
		h, ok := fresh[s.ID()]
		if !ok {
			continue
		}
		out[s.ID()] = h
		p.cache[s.ID()] = probed{health: h, at: now, url: s.HostURL, state: states[s.ID()]}
	}
	return out
}

// Forget drops every remembered answer, so the next Check probes every
// server, as when the user asks for a refresh.
func (p *Prober) Forget() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache = map[string]probed{}
}

// HTTPClient reaches the project's servers from the host: it skips any
// HTTP proxy, and dials every *.localhost name on 127.0.0.1, which not every
// system's resolver does.
func HTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{Transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, port, err := net.SplitHostPort(addr); err == nil && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
				addr = net.JoinHostPort("127.0.0.1", port)
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}}
}
