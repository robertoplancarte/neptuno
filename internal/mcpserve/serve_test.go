package mcpserve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/project"
)

type echoArgs struct {
	Text string `json:"text"`
}

// upstreamServer is a repo's MCP server with one echo tool.
func upstreamServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := sdk.NewServer(&sdk.Implementation{Name: "hyperdrive"}, nil)
	sdk.AddTool(srv, &sdk.Tool{Name: "echo", Description: "Echoes text."},
		func(_ context.Context, _ *sdk.CallToolRequest, in echoArgs) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

type recorder struct {
	mu   sync.Mutex
	cmds []string
}

func (r *recorder) run(_ context.Context, dir, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, dir+": "+name+" "+strings.Join(args, " "))
	return "ok\n", nil
}

func connect(t *testing.T, opts Options) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serverT, clientT := sdk.NewInMemoryTransports()
	go func() { _ = Serve(ctx, opts, serverT) }()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *sdk.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func testProject(root string) *project.Project {
	return &project.Project{
		Root: root, Name: "acme", Repos: []string{"docs", "locke"},
		Services: map[string]project.Service{"locke": {Name: "locke"}},
		Config:   project.Config{Repos: map[string]project.RepoConfig{"docs": {Run: project.RunHost}}},
	}
}

func TestForwardsTheReposTools(t *testing.T) {
	up := upstreamServer(t)
	servers := []mcp.Server{
		{Repo: "locke", Name: "hyperdrive", HostURL: up.URL},
		{Repo: "ghost", Name: "x", HostURL: "http://127.0.0.1:1/mcp"},
	}
	rec := &recorder{}
	cs := connect(t, Options{Project: testProject("/p"), Neptuno: "/bin/neptuno", Run: rec.run,
		Servers: func() []mcp.Server { return servers }})

	names := toolNames(t, cs)
	for _, want := range []string{"neptuno_status", "neptuno_exec", "neptuno_ask_repo_claude", "locke-hyperdrive__echo"} {
		if !slices.Contains(names, want) {
			t.Errorf("no %s in %v", want, names)
		}
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "locke-hyperdrive__echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil || res.IsError || text(res) != "echo: hi" {
		t.Fatalf("echo = %+v, %v", res, err)
	}

	res, err = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "neptuno_mcp_servers"})
	if err != nil {
		t.Fatal(err)
	}
	got := text(res)
	if !strings.Contains(got, "connected, forwarding: locke-hyperdrive__echo") || !strings.Contains(got, "ghost-x (repo ghost)\n") || !strings.Contains(got, "not reachable") {
		t.Errorf("neptuno_mcp_servers =\n%s", got)
	}
}

func TestReconnectsWhenTheRepoComesUp(t *testing.T) {
	up := upstreamServer(t)
	var mu sync.Mutex
	url := "http://127.0.0.1:1/mcp" // not running yet
	servers := func() []mcp.Server {
		mu.Lock()
		defer mu.Unlock()
		return []mcp.Server{{Repo: "locke", Name: "hyperdrive", HostURL: url}}
	}
	cs := connect(t, Options{Project: testProject("/p"), Run: (&recorder{}).run, Servers: servers, Retry: 50 * time.Millisecond})
	if slices.Contains(toolNames(t, cs), "locke-hyperdrive__echo") {
		t.Fatal("forwarded tools of a server that is down")
	}
	mu.Lock()
	url = up.URL
	mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(toolNames(t, cs), "locke-hyperdrive__echo") {
		if time.Now().After(deadline) {
			t.Fatal("never picked up the server once it answered")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestOwnTools(t *testing.T) {
	rec := &recorder{}
	cs := connect(t, Options{Project: testProject("/p"), Neptuno: "/bin/neptuno", Run: rec.run})
	call := func(name string, args map[string]any) *sdk.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	call("neptuno_up", map[string]any{"services": []string{"locke"}})
	call("neptuno_exec", map[string]any{"repo": "locke", "command": "bin/rails test"})
	call("neptuno_exec", map[string]any{"repo": "docs", "command": "npm test"})
	call("neptuno_ask_repo_claude", map[string]any{"repo": "locke", "task": "fix the build"})
	call("neptuno_logs", map[string]any{"service": "locke"})
	if res := call("neptuno_exec", map[string]any{"repo": "nope", "command": "ls"}); !res.IsError {
		t.Error("ran a command in an unknown repo")
	}
	want := []string{
		"/p: /bin/neptuno up --detach locke",
		"/p: docker compose exec -T locke sh -c bin/rails test",
		"/p/repos/docs: sh -c npm test",
		"/p: docker compose exec -T locke claude -p fix the build --mcp-config /root/.claude/neptuno-mcp.json",
		"/p: docker compose logs --no-color --tail 100 locke",
	}
	if !slices.Equal(rec.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(rec.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestToolName(t *testing.T) {
	if got := ToolName("locke-hyperdrive", "search.docs"); got != "locke-hyperdrive__search_docs" {
		t.Errorf("ToolName = %q", got)
	}
	if got := ToolName(strings.Repeat("a", 60), "tool"); len(got) != 64 {
		t.Errorf("ToolName is %d long", len(got))
	}
}
