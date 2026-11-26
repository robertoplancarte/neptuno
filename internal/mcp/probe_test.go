package mcp

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestProbeAllReusesConnections probes a server many times and checks the
// probes share a few keep-alive connections instead of leaving one open per
// probe.
func TestProbeAllReusesConnections(t *testing.T) {
	var conns, posts atomic.Int32
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
	}))
	ts.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	ts.Start()
	defer ts.Close()

	servers := []Server{{Repo: "a", Name: "one", HostURL: ts.URL + "/mcp"}}
	const rounds = 50
	for i := 0; i < rounds; i++ {
		if got := ProbeAll(servers, 2*time.Second); got["a-one"] != Up {
			t.Fatalf("round %d: ProbeAll = %v", i, got)
		}
	}
	if n := posts.Load(); n != rounds {
		t.Errorf("server saw %d initialize requests, want %d", n, rounds)
	}
	if n := conns.Load(); n > 2 {
		t.Errorf("%d probes opened %d connections, want at most 2", rounds, n)
	}
}

// TestProbeEndsSession checks a probe ends the session its initialize
// opened.
func TestProbeEndsSession(t *testing.T) {
	var mu sync.Mutex
	var deleted []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set(sessionHeader, "s-123")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n"))
		case http.MethodDelete:
			mu.Lock()
			deleted = append(deleted, r.Header.Get(sessionHeader))
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer ts.Close()
	got := ProbeAll([]Server{{Repo: "a", Name: "a", HostURL: ts.URL}}, 2*time.Second)
	if got["a"] != Up {
		t.Errorf("ProbeAll = %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deleted) != 1 || deleted[0] != "s-123" {
		t.Errorf("DELETEs = %q, want one for s-123", deleted)
	}
}

func TestProber(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var probed []string
	p := NewProber(30*time.Second, time.Second)
	p.Now = func() time.Time { return now }
	p.probe = func(servers []Server, _ time.Duration) map[string]Health {
		out := map[string]Health{}
		for _, s := range servers {
			probed = append(probed, s.ID())
			out[s.ID()] = Up
		}
		return out
	}
	servers := []Server{{Repo: "a", Name: "a", HostURL: "http://a.localhost/mcp"}, {Repo: "b", Name: "b", HostURL: "http://b.localhost/mcp"}, {Repo: "c", Name: "c"}}
	states := map[string]string{"a": "running", "b": "running"}
	check := func(step string, want ...string) {
		t.Helper()
		probed = nil
		got := p.Check(servers, states)
		if len(got) != 2 || got["a"] != Up || got["b"] != Up {
			t.Errorf("%s: Check = %v", step, got)
		}
		if len(probed) != len(want) {
			t.Errorf("%s: probed %v, want %v", step, probed, want)
			return
		}
		seen := map[string]bool{}
		for _, id := range probed {
			seen[id] = true
		}
		for _, id := range want {
			if !seen[id] {
				t.Errorf("%s: probed %v, want %v", step, probed, want)
			}
		}
	}

	check("first", "a", "b")
	for i := 0; i < 14; i++ {
		now = now.Add(2 * time.Second)
		check("within max age")
	}
	states["b"] = "partial running web=dead"
	check("b's state changed", "b")
	now = now.Add(2 * time.Second)
	check("max age passed for a", "a")
	p.Forget()
	check("after Forget", "a", "b")
}
