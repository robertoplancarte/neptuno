package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/mcpserve"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/scaffold"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "List the MCP servers the repos serve",
		Long: "Lists the MCP servers the repos declare in neptuno.yml, with the URL each\n" +
			"answers on from this machine, through the reverse proxy, and from inside\n" +
			"the repos' containers. Declare one under the repo, with the path it\n" +
			"answers on:\n\n" +
			"  repos:\n" +
			"    locke:\n" +
			"      mcp:\n" +
			"        hyperdrive: /_hyperdrive/mcp\n\n" +
			"`neptuno up` writes them to the project's .mcp.json, for Claude started in\n" +
			"the project folder, and `neptuno claude` hands them to Claude in a repo's\n" +
			"container. `neptuno mcp sync` writes them without starting anything.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			servers, warnings := o.MCPServers()
			for _, w := range warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w)
			}
			printServers(cmd.OutOrStdout(), servers)
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "sync",
		Short: "Write the MCP servers to Claude's config files",
		Long: "Writes the MCP servers the repos declare in neptuno.yml to the project's\n" +
			".mcp.json, with their http://<repo>.localhost URLs, and to\n" +
			".neptuno/claude/" + mcp.ContainerFile + ", with the URLs that work inside the\n" +
			"containers. Servers you added to .mcp.json yourself are kept. `neptuno up`\n" +
			"does the same each time it runs.\n\n" +
			"Also writes the project's AGENTS.md, CLAUDE.md and .claude/skills, which\n" +
			"tell coding agents how to work in the project, where any is missing. Files\n" +
			"you have already are left alone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			return syncMCP(cmd, o)
		},
	})
	cmd.AddCommand(newMCPServeCommand(), newMCPDesktopCommand())
	return cmd
}

func syncMCP(cmd *cobra.Command, o *orchestrate.Orchestrator) error {
	servers, err := o.SyncMCP()
	if err != nil {
		return err
	}
	wrote, err := scaffold.WriteAgentFiles(o.Project.Root, o.Project.Name)
	for _, path := range wrote {
		fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s\n", path)
	}
	if err != nil {
		return err
	}
	if len(wrote) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "They tell Claude, Cursor and other agents in the project folder how to work with the repos.")
	}
	printServers(cmd.OutOrStdout(), servers)
	return nil
}

func printServers(out io.Writer, servers []mcp.Server) {
	if len(servers) == 0 {
		fmt.Fprintln(out, "No MCP servers declared. Add one to neptuno.yml under repos.<repo>.mcp, such as\n  hyperdrive: /_hyperdrive/mcp")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SERVER\tFROM THIS MACHINE\tFROM CONTAINERS")
	for _, s := range servers {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.ID(), orDash(s.HostURL), orDash(s.ContainerURL))
	}
	w.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func newMCPServeCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the project's MCP server over stdio, for Claude desktop",
		Long: "Runs one local MCP server over stdin and stdout, as Claude desktop and other\n" +
			"MCP clients start them. It has Neptuno's tools for the project (status, up,\n" +
			"down, restart, logs, running a command in a repo, and handing a task to the\n" +
			"Claude in a repo's container), and forwards the tools of every MCP server\n" +
			"the repos declare, named <repo>-<server>__<tool>. A server whose repo isn't\n" +
			"running is tried again every 15 seconds and on each call.\n\n" +
			"`neptuno mcp desktop` adds it to Claude desktop's config.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = wd
			}
			p, err := project.Load(dir)
			if err != nil {
				return err
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			// stdout carries the protocol, so everything else goes to
			// stderr, which Claude desktop keeps in its MCP logs.
			logs := cmd.ErrOrStderr()
			servers := func() []mcp.Server {
				cur, err := project.Load(p.Root)
				if err != nil {
					fmt.Fprintln(logs, "warning:", err)
					cur = p
				}
				o := &orchestrate.Orchestrator{Project: cur, Runner: orchestrate.ExecRunner{Out: logs, Err: logs}, Log: logs, Neptuno: self}
				list, warnings := o.MCPServers()
				for _, w := range warnings {
					fmt.Fprintln(logs, "warning:", w)
				}
				return list
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			fmt.Fprintf(logs, "neptuno: serving MCP for %s at %s\n", p.Name, p.Root)
			return mcpserve.Serve(ctx, mcpserve.Options{Project: p, Neptuno: self, Version: Version, Servers: servers}, &sdk.StdioTransport{})
		},
	}
	cmd.Flags().StringVar(&dir, "project", "", "the project folder (default: the one you are in)")
	return cmd
}

func newMCPDesktopCommand() *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "desktop",
		Short: "Add the project's MCP server to Claude desktop",
		Long: "Adds `neptuno mcp serve` for this project to Claude desktop's config, as\n" +
			"neptuno-<project>, so Claude desktop chat gets Neptuno's tools and the repos'\n" +
			"MCP servers. Claude desktop can't reach http://*.localhost servers itself:\n" +
			"its own connectors are reached from Anthropic's servers, so they need a\n" +
			"public URL. Your PATH is saved with it, since apps started from the dock\n" +
			"don't see the one your shell sets up, and docker would be missing.\n" +
			"Restart Claude desktop afterwards. --remove takes it out again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			path, err := mcp.DesktopConfigPath()
			if err != nil {
				return err
			}
			name := mcp.DesktopName(o.Project.Name)
			var entry *mcp.DesktopEntry
			if !remove {
				entry = desktopEntry(o)
			}
			changed, err := mcp.WriteDesktop(path, name, entry)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch {
			case !changed && remove:
				fmt.Fprintf(out, "Claude desktop has no %s server in %s\n", name, path)
			case !changed:
				fmt.Fprintf(out, "Claude desktop already runs %s from %s\n", name, path)
			case remove:
				fmt.Fprintf(out, "Removed %s from %s. Restart Claude desktop to drop it.\n", name, path)
			default:
				fmt.Fprintf(out, "Added %s to %s. Restart Claude desktop to load it.\n", name, path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "take the project's server out of Claude desktop's config")
	return cmd
}

// desktopEntry is how Claude desktop starts the project's server.
func desktopEntry(o *orchestrate.Orchestrator) *mcp.DesktopEntry {
	self := o.Neptuno
	if abs, err := filepath.Abs(self); err == nil {
		self = abs
	}
	e := &mcp.DesktopEntry{Command: self, Args: []string{"mcp", "serve", "--project", o.Project.Root}}
	if path := os.Getenv("PATH"); path != "" {
		e.Env = map[string]string{"PATH": path}
	}
	return e
}
