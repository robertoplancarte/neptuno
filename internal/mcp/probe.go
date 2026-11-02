package mcp

import (
	"context"
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

// Probe sends an MCP initialize request to url and sorts the answer: any
// reply but a 404 or the proxy's 502, 503 or 504 counts as up, since servers
// answer a client they don't know in many ways.
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
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotFound:
		return NotFound
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return Down
	}
	return Up
}

// ProbeAll probes the host URLs of servers at once, waiting at most timeout,
// and returns each server's health by ID. Servers without a host URL are
// left out.
func ProbeAll(servers []Server, timeout time.Duration) map[string]Health {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client := HTTPClient()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
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
			h := Probe(ctx, client, s.HostURL)
			mu.Lock()
			out[s.ID()] = h
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return out
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
