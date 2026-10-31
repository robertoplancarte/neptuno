package orchestrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/state"
)

// DashboardWindow is the first window of the project's tmux session, which
// runs the dashboard.
const DashboardWindow = "neptuno"

// ServiceStatus is what the dashboard shows for one repo or compose service.
type ServiceStatus struct {
	Name string
	// Repo is true for repos under repos/, false for other compose services.
	Repo bool
	// RunMode is project.RunContainer or project.RunHost, for repos.
	RunMode string
	// Container is the compose container's state, such as running or
	// exited, or "" when it has none.
	Container string
	// Health is the container's health check result, if it has one.
	Health string
	// Ports are the container's published ports, such as 5432->5432.
	Ports []string
	// Overmind is true while the repo's overmind answers on its socket.
	Overmind bool
	// Processes are overmind's processes and their states.
	Processes []ProcessStatus
	// Window is true when the repo has a window in the project's session.
	Window bool
	// Claude is true when Claude Code is open for the repo.
	Claude bool
	// Env is the repo's active configuration, such as local.
	Env string
	// DependsOn lists the direct dependencies.
	DependsOn []string
	// Git describes the repo's checkout, or is nil when it has none.
	Git *GitStatus
}

// ProcessStatus is one line of `overmind status`.
type ProcessStatus struct {
	Name   string
	Status string
}

// GitStatus is where a repo's checkout is.
type GitStatus struct {
	Branch  string
	Commit  string
	Subject string
	Author  string
	Date    time.Time
	// Dirty is true when the work tree has uncommitted changes.
	Dirty bool
}

// Running reports whether anything of the service is up.
func (s ServiceStatus) Running() bool {
	return s.Container == "running" || s.Overmind
}

// State is a one-word summary: running, partial, exited, stopped and so on.
func (s ServiceStatus) State() string {
	if s.Overmind {
		up := 0
		for _, p := range s.Processes {
			if p.Status == "running" {
				up++
			}
		}
		if len(s.Processes) > 0 && up < len(s.Processes) {
			return "partial"
		}
		if s.RunMode == project.RunContainer && s.Container != "" && s.Container != "running" {
			return "partial"
		}
		return "running"
	}
	switch s.Container {
	case "":
		return "stopped"
	case "running":
		if s.Health == "unhealthy" {
			return "unhealthy"
		}
		if s.Health == "starting" {
			return "starting"
		}
		return "running"
	}
	return s.Container
}

// Status reports the state of every repo and compose service in the project.
// It only reads, so the dashboard can call it every few seconds.
func (o *Orchestrator) Status() []ServiceStatus {
	p := o.Project
	repos, services := p.Selectable()
	containers := o.composeContainers()
	windows := map[string]bool{}
	if o.hasSession() {
		out, _ := o.Runner.Output(p.Root, "tmux", "list-windows", "-t", "="+o.SessionName(), "-F", "#{window_name}")
		for _, w := range strings.Fields(out) {
			windows[w] = true
		}
	}
	st, _ := state.Load(p.Root)

	var out []ServiceStatus
	for _, name := range append(append([]string(nil), repos...), services...) {
		s := ServiceStatus{Name: name, Repo: p.IsRepo(name), DependsOn: p.Services[name].DependsOn}
		if c, ok := containers[name]; ok {
			s.Container, s.Health, s.Ports = c.State, c.Health, c.ports()
		}
		if s.Repo {
			s.RunMode = p.RunMode(name)
			if s.RunMode == project.RunHost {
				s.DependsOn = p.Config.Repos[name].DependsOn
			}
			s.Env = st.EnvFor(name, p.Config.DefaultEnv)
			s.Window, s.Claude = windows[name], windows[ClaudeWindow(name)]
			if tabs, _, ok := o.overmindWindows(name); ok && slices.Contains(tabs, ClaudeTab) {
				s.Claude = true
			}
			if procs, err := o.Runner.Output(p.Root, "overmind", "status", "-s", socket(name)); err == nil {
				s.Overmind, s.Processes = true, parseOvermindStatus(procs)
			}
			s.Git = o.gitStatus(p.Path("repos", name))
		}
		out = append(out, s)
	}
	return out
}

// composeContainer is one entry of `docker compose ps --format json`.
type composeContainer struct {
	Service    string `json:"Service"`
	State      string `json:"State"`
	Health     string `json:"Health"`
	Publishers []struct {
		PublishedPort int `json:"PublishedPort"`
		TargetPort    int `json:"TargetPort"`
	} `json:"Publishers"`
}

func (c composeContainer) ports() []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range c.Publishers {
		if p.PublishedPort == 0 {
			continue
		}
		s := fmt.Sprintf("%d->%d", p.PublishedPort, p.TargetPort)
		if !seen[s] { // listed once per address family
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// composeContainers maps each service to its container's state.
func (o *Orchestrator) composeContainers() map[string]composeContainer {
	out, err := o.Runner.Output(o.Project.Root, "docker", "compose", "ps", "--all", "--format", "json")
	if err != nil {
		return nil
	}
	return parseComposePS(out)
}

// parseComposePS reads `docker compose ps --format json`, which newer
// versions print as one object per line and older ones as an array.
func parseComposePS(out string) map[string]composeContainer {
	var list []composeContainer
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "[") {
		_ = json.Unmarshal([]byte(out), &list)
	} else {
		for _, line := range strings.Split(out, "\n") {
			var c composeContainer
			if json.Unmarshal([]byte(line), &c) == nil {
				list = append(list, c)
			}
		}
	}
	m := map[string]composeContainer{}
	for _, c := range list {
		// With replicas, prefer a running container.
		if prev, ok := m[c.Service]; ok && prev.State == "running" {
			continue
		}
		m[c.Service] = c
	}
	return m
}

// parseOvermindStatus reads `overmind status`, a PROCESS PID STATUS table.
func parseOvermindStatus(out string) []ProcessStatus {
	var procs []ProcessStatus
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 && len(f) > 0 && f[0] == "PROCESS" || len(f) < 2 {
			continue
		}
		procs = append(procs, ProcessStatus{Name: f[0], Status: f[len(f)-1]})
	}
	return procs
}

// gitStatus describes the checkout in dir, or returns nil when it is not one.
func (o *Orchestrator) gitStatus(dir string) *GitStatus {
	// An empty submodule folder would otherwise report the project's own
	// checkout.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil
	}
	out, err := o.Runner.Output(dir, "git", "log", "-1", "--format=%h%x1f%cI%x1f%an%x1f%s")
	if err != nil || out == "" {
		return nil
	}
	f := strings.SplitN(out, "\x1f", 4)
	if len(f) < 4 {
		return nil
	}
	g := &GitStatus{Commit: f[0], Author: f[2], Subject: f[3]}
	g.Date, _ = time.Parse(time.RFC3339, f[1])
	if branch, err := o.Runner.Output(dir, "git", "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		g.Branch = branch
	}
	if changes, err := o.Runner.Output(dir, "git", "status", "--porcelain", "--untracked-files=no"); err == nil {
		g.Dirty = changes != ""
	}
	return g
}
