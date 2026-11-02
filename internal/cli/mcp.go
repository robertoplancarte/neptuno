package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
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
			"containers. Servers you added to .mcp.json yourself are kept. Also writes a\n" +
			"CLAUDE.md describing the project if there is none. `neptuno up` does the\n" +
			"same each time it runs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			return syncMCP(cmd, o)
		},
	})
	return cmd
}

func syncMCP(cmd *cobra.Command, o *orchestrate.Orchestrator) error {
	servers, err := o.SyncMCP()
	if err != nil {
		return err
	}
	wrote, err := scaffold.WriteClaudeMD(o.Project.Root, o.Project.Name)
	if err != nil {
		return err
	}
	if wrote {
		fmt.Fprintln(cmd.ErrOrStderr(), "Wrote CLAUDE.md, which tells Claude in the project folder how to work with the repos")
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
