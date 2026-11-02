package mcpserve

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/project"
)

// ContainerMCPConfig is the MCP config for Claude inside the containers, as
// orchestrate.ContainerMCPConfig.
const ContainerMCPConfig = "/root/.claude/" + mcp.ContainerFile

type (
	noInput      struct{}
	servicesArgs struct {
		Services []string `json:"services" jsonschema:"repos and compose services, as neptuno_status names them"`
	}
	restartArgs struct {
		Target string `json:"target" jsonschema:"a repo, or repo:process for one of its Procfile processes"`
	}
	execArgs struct {
		Repo    string `json:"repo" jsonschema:"the repo to run the command in"`
		Command string `json:"command" jsonschema:"a shell command, run with sh -c in the repo's container (or in repos/<repo> for a repo that runs on the host)"`
	}
	logsArgs struct {
		Service string `json:"service" jsonschema:"a repo or compose service"`
		Lines   int    `json:"lines,omitempty" jsonschema:"how many of the last lines to return; 100 when left out"`
	}
	askArgs struct {
		Repo string `json:"repo" jsonschema:"the repo whose Claude does the task"`
		Task string `json:"task" jsonschema:"what to do, written as a prompt for Claude Code"`
	}
)

func (s *Server) addOwnTools() {
	p := s.opts.Project
	readOnly := &sdk.ToolAnnotations{ReadOnlyHint: true}

	sdk.AddTool(s.Server, &sdk.Tool{
		Name:        "neptuno_status",
		Description: "Lists every repo and compose service of the project with its state, its overmind processes, and each repo's branch, commit and env.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, any, error) {
		out, err := s.neptuno(ctx, "status")
		return textResult(ansi.Strip(out), err), nil, nil
	})

	sdk.AddTool(s.Server, &sdk.Tool{
		Name:        "neptuno_up",
		Description: "Starts repos and services with everything they depend on, as `neptuno up --detach` does, and waits until their containers are up.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in servicesArgs) (*sdk.CallToolResult, any, error) {
		if len(in.Services) == 0 {
			return errorResult("name at least one repo or service; neptuno_status lists them"), nil, nil
		}
		out, err := s.neptuno(ctx, append([]string{"up", "--detach"}, in.Services...)...)
		return textResult(out, err), nil, nil
	})

	sdk.AddTool(s.Server, &sdk.Tool{
		Name:        "neptuno_down",
		Description: "Stops repos and services, as `neptuno down` does.",
		Annotations: &sdk.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in servicesArgs) (*sdk.CallToolResult, any, error) {
		if len(in.Services) == 0 {
			return errorResult("name at least one repo or service; neptuno_status lists them"), nil, nil
		}
		out, err := s.neptuno(ctx, append([]string{"down"}, in.Services...)...)
		return textResult(out, err), nil, nil
	})

	sdk.AddTool(s.Server, &sdk.Tool{
		Name:        "neptuno_restart",
		Description: "Restarts a running repo's Procfile processes, or one of them with repo:process.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in restartArgs) (*sdk.CallToolResult, any, error) {
		out, err := s.neptuno(ctx, "restart", in.Target)
		return textResult(out, err), nil, nil
	})

	sdk.AddTool(s.Server, &sdk.Tool{
		Name: "neptuno_exec",
		Description: "Runs a shell command where the repo runs, next to its code, tools and services: in its container with " +
			"`docker compose exec`, or in repos/<repo> for a repo that runs on the host. Returns the output and exit status. " +
			"Use it for tests, generators, database consoles and anything else the repo's own tooling does.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in execArgs) (*sdk.CallToolResult, any, error) {
		if !p.IsRepo(in.Repo) {
			return errorResult(fmt.Sprintf("%q is %v", in.Repo, errNotRepo)), nil, nil
		}
		out, err := s.inRepo(ctx, in.Repo, "sh", "-c", in.Command)
		return textResult(out, err), nil, nil
	})

	sdk.AddTool(s.Server, &sdk.Tool{
		Name:        "neptuno_logs",
		Description: "Returns the end of a repo's or service's container output (`docker compose logs`).",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in logsArgs) (*sdk.CallToolResult, any, error) {
		if in.Lines <= 0 {
			in.Lines = 100
		}
		if p.IsRepo(in.Service) && p.RunMode(in.Service) == project.RunHost {
			return errorResult(in.Service + " runs on the host, so its output is in its tmux window, not in docker"), nil, nil
		}
		out, err := s.opts.Run(ctx, p.Root, "docker", "compose", "logs", "--no-color", "--tail", strconv.Itoa(in.Lines), in.Service)
		return textResult(out, err), nil, nil
	})

	sdk.AddTool(s.Server, &sdk.Tool{
		Name: "neptuno_ask_repo_claude",
		Description: "Hands a task to Claude Code running where the repo runs (its container, or repos/<repo> on the host), " +
			"with the repo's code, tools and the project's MCP servers at hand, and returns its answer. Use it for work that " +
			"needs many steps inside one repo. It can take minutes.",
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in askArgs) (*sdk.CallToolResult, any, error) {
		if !p.IsRepo(in.Repo) {
			return errorResult(fmt.Sprintf("%q is %v", in.Repo, errNotRepo)), nil, nil
		}
		args := []string{"claude", "-p", in.Task}
		if p.RunMode(in.Repo) != project.RunHost {
			args = append(args, "--mcp-config", ContainerMCPConfig)
		}
		out, err := s.inRepo(ctx, in.Repo, args...)
		return textResult(out, err), nil, nil
	})

	sdk.AddTool(s.Server, &sdk.Tool{
		Name:        "neptuno_mcp_servers",
		Description: "Lists the MCP servers the repos declare, their URLs, whether this server reaches them, and the tools it forwards from each.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ noInput) (*sdk.CallToolResult, any, error) {
		s.connectAll(ctx, 3*time.Second)
		return textResult(describe(s.Upstreams()), nil), nil, nil
	})
}

func boolPtr(b bool) *bool { return &b }

// neptuno runs the neptuno binary in the project root.
func (s *Server) neptuno(ctx context.Context, args ...string) (string, error) {
	return s.opts.Run(ctx, s.opts.Project.Root, s.opts.Neptuno, args...)
}

// inRepo runs a command where the repo runs.
func (s *Server) inRepo(ctx context.Context, repo string, args ...string) (string, error) {
	p := s.opts.Project
	if p.RunMode(repo) == project.RunHost {
		return s.opts.Run(ctx, p.Path("repos", repo), args[0], args[1:]...)
	}
	return s.opts.Run(ctx, p.Root, "docker", append([]string{"compose", "exec", "-T", repo}, args...)...)
}

func describe(list []UpstreamStatus) string {
	if len(list) == 0 {
		return "No MCP servers declared. A repo declares one in neptuno.yml under repos.<repo>.mcp, such as\n  hyperdrive: /_hyperdrive/mcp"
	}
	var b strings.Builder
	for _, u := range list {
		fmt.Fprintf(&b, "%s (repo %s)\n  from this machine: %s\n  from containers:   %s\n", u.ID(), u.Repo, orNone(u.HostURL), orNone(u.ContainerURL))
		switch {
		case u.Connected:
			sort.Strings(u.Tools)
			fmt.Fprintf(&b, "  connected, forwarding: %s\n", orNone(strings.Join(u.Tools, ", ")))
		case u.Error != "":
			fmt.Fprintf(&b, "  not reachable: %s\n", u.Error)
		default:
			b.WriteString("  not reachable\n")
		}
	}
	return b.String()
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
