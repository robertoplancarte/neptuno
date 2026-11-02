package orchestrate

import (
	"fmt"
	"slices"

	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/state"
)

// ContainerMCPConfig is where Claude inside a repo's container finds the
// project's MCP servers: .neptuno/claude/neptuno-mcp.json, through the
// mount at ClaudeConfigDir.
const ContainerMCPConfig = ClaudeConfigDir + "/" + mcp.ContainerFile

// MCPServers lists the MCP servers the repos declare in neptuno.yml, with
// their URLs, and any problems finding them.
func (o *Orchestrator) MCPServers() ([]mcp.Server, []string) {
	sites, published := o.proxySites(o.composeContainers())
	return mcp.Servers(o.Project, sites, published)
}

// SyncMCP writes the declared MCP servers into the project's .mcp.json, for
// Claude on the host started in the project folder, and into
// .neptuno/claude/neptuno-mcp.json, for Claude in the repos' containers.
func (o *Orchestrator) SyncMCP() ([]mcp.Server, error) {
	p := o.Project
	servers, warnings := o.MCPServers()
	for _, w := range warnings {
		fmt.Fprintln(o.Log, "warning: "+w)
	}
	st, err := state.Load(p.Root)
	if err != nil {
		return servers, err
	}
	wrote, changed, err := mcp.WriteProject(p.Root, servers, st.MCPServers)
	if err != nil {
		return servers, err
	}
	if changed {
		fmt.Fprintf(o.Log, "Updated %s with %d MCP server(s) for Claude in the project folder\n", mcp.ProjectFile, len(wrote))
	}
	if !slices.Equal(wrote, st.MCPServers) {
		st.MCPServers = wrote
		if err := state.Save(p.Root, st); err != nil {
			return servers, err
		}
	}
	if _, err := mcp.WriteContainer(p.Path(".neptuno", "claude"), servers); err != nil {
		return servers, err
	}
	return servers, nil
}

// MCPSummary is where the project's MCP servers are set up, for the
// dashboard.
type MCPSummary struct {
	// Servers is how many the repos declare.
	Servers int
	// ProjectFileCurrent is true when .mcp.json has every one of them at
	// its current URL.
	ProjectFileCurrent bool
	// Desktop is true when Claude desktop's config runs `neptuno mcp serve`
	// for this project.
	Desktop bool
}

// MCPSummary reports how far the project's MCP servers are set up.
func (o *Orchestrator) MCPSummary() MCPSummary {
	servers, _ := o.MCPServers()
	sum := MCPSummary{Servers: len(servers)}
	sum.ProjectFileCurrent = mcp.ProjectFileCurrent(o.Project.Root, servers)
	if path, err := mcp.DesktopConfigPath(); err == nil {
		e, _ := mcp.ReadDesktop(path, mcp.DesktopName(o.Project.Name))
		sum.Desktop = e != nil
	}
	return sum
}
