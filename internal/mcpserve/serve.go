// Package mcpserve runs `neptuno mcp serve`: one local MCP server, over
// stdio, that gives Claude desktop, or any MCP client that starts local
// servers, Neptuno's own tools for the project plus the tools of every MCP
// server the project's repos declare, forwarded from their
// http://<repo>.localhost URLs.
package mcpserve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/project"
)

// maxOutput caps how much of a command's output a tool returns: the end,
// where errors and results usually are.
const maxOutput = 60 << 10

// Options configures Serve.
type Options struct {
	Project *project.Project
	// Neptuno is the neptuno binary, which runs the up, down, restart and
	// status tools.
	Neptuno string
	// Version is reported to clients.
	Version string
	// Servers lists the MCP servers the repos declare, read again on every
	// reconnect so new declarations show up.
	Servers func() []mcp.Server
	// Run runs a command in the project root and returns its combined
	// output. Nil runs it for real.
	Run func(ctx context.Context, dir string, name string, args ...string) (string, error)
	// HTTPClient reaches the repos' servers. Nil uses mcp.HTTPClient.
	HTTPClient *http.Client
	// Retry is how often servers that did not answer are tried again. Zero
	// means every 15 seconds.
	Retry time.Duration
}

// Serve runs the server on t until the client disconnects or ctx ends.
func Serve(ctx context.Context, opts Options, t sdk.Transport) error {
	s := New(opts)
	s.connectAll(ctx, 3*time.Second)
	go s.retry(ctx)
	return s.Server.Run(ctx, t)
}

// Server is the MCP server and the repos' servers it forwards.
type Server struct {
	*sdk.Server
	opts Options

	mu sync.Mutex
	// upstreams are the repos' servers by ID.
	upstreams map[string]*upstream
}

// upstream is one of the repos' MCP servers.
type upstream struct {
	server  mcp.Server
	session *sdk.ClientSession
	// tools are the names this server registered for it, and their names
	// on the upstream.
	tools map[string]string
	err   error
}

// New builds the server with Neptuno's own tools; Serve connects it to the
// repos' servers.
func New(opts Options) *Server {
	if opts.Run == nil {
		opts.Run = run
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = mcp.HTTPClient()
	}
	if opts.Retry == 0 {
		opts.Retry = 15 * time.Second
	}
	if opts.Servers == nil {
		opts.Servers = func() []mcp.Server { return nil }
	}
	p := opts.Project
	s := &Server{opts: opts, upstreams: map[string]*upstream{}}
	s.Server = sdk.NewServer(&sdk.Implementation{Name: "neptuno-" + p.Name, Title: "Neptuno: " + p.Name, Version: opts.Version}, &sdk.ServerOptions{
		Instructions: fmt.Sprintf("Tools for the %s Neptuno project at %s, a development environment of several repos run "+
			"with docker compose. Use neptuno_status first to see the repos and services and whether they run. Repos run in "+
			"their containers, so run their commands with neptuno_exec. Tools named <repo>-<server>__<tool> come from the MCP "+
			"servers the repos serve and only work while that repo runs.", p.Name, p.Root),
	})
	s.addOwnTools()
	return s
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// connectAll tries every declared server that isn't connected, at once.
func (s *Server) connectAll(ctx context.Context, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	declared := s.opts.Servers()
	var wg sync.WaitGroup
	for _, srv := range declared {
		if srv.HostURL == "" {
			continue
		}
		s.mu.Lock()
		u := s.upstreams[srv.ID()]
		if u == nil {
			u = &upstream{server: srv, tools: map[string]string{}}
			s.upstreams[srv.ID()] = u
		}
		connected := u.session != nil
		if !connected {
			u.server = srv // its URL may have changed
		}
		s.mu.Unlock()
		if connected {
			continue
		}
		wg.Add(1)
		go func(u *upstream) {
			defer wg.Done()
			s.connect(ctx, u)
		}(u)
	}
	wg.Wait()
}

func (s *Server) retry(ctx context.Context) {
	t := time.NewTicker(s.opts.Retry)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.connectAll(ctx, 5*time.Second)
		}
	}
}

// connect opens a session to u and registers its tools, replacing the ones
// it registered before.
func (s *Server) connect(ctx context.Context, u *upstream) error {
	s.mu.Lock()
	endpoint := u.server.HostURL
	s.mu.Unlock()
	client := sdk.NewClient(&sdk.Implementation{Name: "neptuno", Version: s.opts.Version}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           s.opts.HTTPClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		s.mu.Lock()
		u.err = err
		s.mu.Unlock()
		return err
	}
	var tools []*sdk.Tool
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			session.Close()
			s.mu.Lock()
			u.err = err
			s.mu.Unlock()
			return err
		}
		tools = append(tools, t)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if u.session != nil {
		u.session.Close()
	}
	var old []string
	for name := range u.tools {
		old = append(old, name)
	}
	if len(old) > 0 {
		s.RemoveTools(old...)
	}
	u.session, u.err, u.tools = session, nil, map[string]string{}
	for _, t := range tools {
		name := ToolName(u.server.ID(), t.Name)
		u.tools[name] = t.Name
		forwarded := *t
		forwarded.Name = name
		forwarded.Description = fmt.Sprintf("[%s, from %s] %s", u.server.ID(), u.server.Repo, t.Description)
		if forwarded.InputSchema == nil {
			forwarded.InputSchema = map[string]any{"type": "object"}
		}
		s.AddTool(&forwarded, s.forward(u.server.ID(), name))
	}
	return nil
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ToolName is a forwarded tool's name: <server id>__<tool>, with characters
// clients reject replaced and cut to the 64 characters they allow.
func ToolName(server, tool string) string {
	name := unsafeName.ReplaceAllString(server+"__"+tool, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// forward calls the tool on the repo's server, reconnecting once if the
// session died, such as when the repo restarted.
func (s *Server) forward(id, name string) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var args any
		if len(req.Params.Arguments) > 0 {
			args = json.RawMessage(req.Params.Arguments)
		}
		for attempt := 0; ; attempt++ {
			s.mu.Lock()
			u := s.upstreams[id]
			var session *sdk.ClientSession
			var tool string
			if u != nil {
				session, tool = u.session, u.tools[name]
			}
			s.mu.Unlock()
			if u == nil || tool == "" {
				return errorResult(fmt.Sprintf("%s is no longer declared in neptuno.yml", id)), nil
			}
			if session != nil {
				res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
				if err == nil {
					return res, nil
				}
				if attempt > 0 || ctx.Err() != nil {
					return errorResult(fmt.Sprintf("calling %s on %s: %v", tool, u.server.HostURL, err)), nil
				}
			}
			if attempt > 0 {
				return errorResult(fmt.Sprintf("%s does not answer at %s; is %s running? Start it with neptuno_up", id, u.server.HostURL, u.server.Repo)), nil
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.connect(cctx, u)
			cancel()
			if err != nil {
				return errorResult(fmt.Sprintf("%s does not answer at %s (%v); is %s running? Start it with neptuno_up", id, u.server.HostURL, err, u.server.Repo)), nil
			}
		}
	}
}

func errorResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

func textResult(text string, err error) *sdk.CallToolResult {
	text = tail(text)
	if err != nil {
		if strings.TrimSpace(text) != "" {
			text += "\n"
		}
		return errorResult(text + err.Error())
	}
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

func tail(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return "[… earlier output cut]\n" + s[len(s)-maxOutput:]
}

// errNotRepo is returned for a repo name that isn't one.
var errNotRepo = errors.New("not a repo of this project; neptuno_status lists them")

// Upstreams reports each declared server Serve forwards and whether it is
// connected, for the neptuno_mcp_servers tool.
func (s *Server) Upstreams() []UpstreamStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []UpstreamStatus
	for _, srv := range s.opts.Servers() {
		st := UpstreamStatus{Server: srv}
		if u := s.upstreams[srv.ID()]; u != nil {
			st.Connected = u.session != nil
			for name := range u.tools {
				st.Tools = append(st.Tools, name)
			}
			if u.err != nil {
				st.Error = u.err.Error()
			}
		}
		out = append(out, st)
	}
	return out
}

// UpstreamStatus is one forwarded server's state.
type UpstreamStatus struct {
	mcp.Server
	Connected bool
	Tools     []string
	Error     string
}
