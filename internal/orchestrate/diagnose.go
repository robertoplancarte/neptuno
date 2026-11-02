package orchestrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/procs"
	"github.com/robertoplancarte/neptuno/internal/project"
)

// Conflict is why a process exited, when it could not start because its
// port or server pid file was taken.
type Conflict struct {
	// Port is the port it could not listen on, if its output says.
	Port int `json:"port,omitempty"`
	// PID is the process a pid file names, from Rails' "A server is
	// already running", and PIDFile that file.
	PID     int    `json:"pid,omitempty"`
	PIDFile string `json:"pid_file,omitempty"`
}

var (
	serverRunning = regexp.MustCompile(`A server is already running \(pid: (\d+)(?:, file: ([^)\s]+))?\)`)
	addrInUse     = regexp.MustCompile(`(?i)address already in use|EADDRINUSE`)
	portPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`port (\d+)`),                   // puma
		regexp.MustCompile(`', (\d+)\)`),                   // uvicorn: ('0.0.0.0', 8000)
		regexp.MustCompile(`(?:tcp|EADDRINUSE).*:(\d+)\b`), // go, node
		regexp.MustCompile(`:(\d{2,5})\b`),
	}
)

// ParseConflict looks through a process's last output for Rails' "A server
// is already running" or an "address already in use" error.
func ParseConflict(output string) (Conflict, bool) {
	if m := serverRunning.FindStringSubmatch(output); m != nil {
		pid, _ := strconv.Atoi(m[1])
		return Conflict{PID: pid, PIDFile: m[2]}, true
	}
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !addrInUse.MatchString(lines[i]) {
			continue
		}
		for _, re := range portPatterns {
			if m := re.FindAllStringSubmatch(lines[i], -1); m != nil {
				port, _ := strconv.Atoi(m[len(m)-1][1])
				if port > 0 && port < 65536 {
					return Conflict{Port: port}, true
				}
			}
		}
		return Conflict{}, true
	}
	return Conflict{}, false
}

// conflictFile records why a repo's process last exited, relative to the
// project root.
func conflictFile(repo, process string) string {
	return filepath.Join(".neptuno", "run", repo+".exits", process+".json")
}

// RecordExit saves the conflict in a process's last output, which the
// pane-died hook captures when it exits, for Status to explain, or clears
// what an earlier exit saved.
func RecordExit(root, repo, process, output string) error {
	path := filepath.Join(root, conflictFile(repo, process))
	c, ok := ParseConflict(output)
	if !ok {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// exitWarnings explains each of a repo's stopped processes that exited
// because its port or pid file was taken, and by what.
func (o *Orchestrator) exitWarnings(s ServiceStatus) []string {
	var out []string
	for _, proc := range s.Processes {
		if proc.Status == "running" {
			continue
		}
		b, err := os.ReadFile(o.Project.Path(conflictFile(s.Name, proc.Name)))
		if err != nil {
			continue
		}
		var c Conflict
		if json.Unmarshal(b, &c) != nil {
			continue
		}
		out = append(out, o.explainConflict(s, proc.Name, c))
	}
	return out
}

func (o *Orchestrator) explainConflict(s ServiceStatus, process string, c Conflict) string {
	repo := s.Name
	what := "its port was already in use"
	switch {
	case c.PID > 0:
		what = fmt.Sprintf("a server is already running (pid %d)", c.PID)
	case c.Port > 0:
		what = fmt.Sprintf("port %d was already in use", c.Port)
	}
	prefix := fmt.Sprintf("%s could not start: %s", process, what)
	if s.RunMode != project.RunContainer || (c.Port == 0 && c.PID == 0) || s.Container != "running" {
		return prefix
	}
	args := []string{"compose", "exec", "-T", "-e", "NEPTUNO_PORT=" + itoa(c.Port), "-e", "NEPTUNO_PID=" + itoa(c.PID), repo, "sh", "-c", procs.ProbeScript}
	out, err := o.Runner.Output(o.Project.Root, "docker", args...)
	if err != nil {
		return prefix
	}
	h, ok := procs.ParseProbe(out)
	switch {
	case !ok:
		return fmt.Sprintf("%s, but it is free now; try neptuno restart %s:%s", prefix, repo, process)
	case h.Gone:
		file := c.PIDFile
		if file == "" {
			file = "its pid file"
		}
		return fmt.Sprintf("%s, but pid %d is not running; remove %s and run neptuno restart %s:%s", prefix, h.PID, file, repo, process)
	}
	if h.PID == 1 && c.PID == 1 {
		file := c.PIDFile
		if file == "" {
			file = "its pid file"
		}
		return fmt.Sprintf("%s, but pid 1 is the container's main process, so %s is left from an earlier container; remove it and run neptuno restart %s:%s", prefix, file, repo, process)
	}
	holder := fmt.Sprintf("pid %d", h.PID)
	if h.Cmd != "" {
		holder += " (" + h.Cmd + ")"
	}
	if c.Port > 0 {
		prefix = fmt.Sprintf("%s could not start: port %d is held by %s", process, c.Port, holder)
	} else {
		prefix = fmt.Sprintf("%s could not start: a server is already running as %s", process, holder)
	}
	switch {
	case h.Run != "" && h.Run != o.currentRun(repo):
		return fmt.Sprintf("%s, left by an earlier run; neptuno restart %s stops it", prefix, repo)
	case h.Run != "":
		return fmt.Sprintf("%s, which this run's %s started", prefix, h.Proc)
	}
	return fmt.Sprintf("%s, which neptuno did not start (such as one run by hand with docker compose exec); stop it with: docker compose exec %s kill %d", prefix, repo, h.PID)
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// hostOvermind is an overmind daemon on the host.
type hostOvermind struct {
	PID  int
	Repo string
}

// hostOverminds lists the overmind daemons neptuno started for this
// project, found by their -d <root> and -w <project>-<repo> arguments.
func (o *Orchestrator) hostOverminds() []hostOvermind {
	out, err := o.Runner.Output(o.Project.Root, "ps", "-axo", "pid=,command=")
	if err != nil {
		return nil
	}
	return parseHostOverminds(out, o.Project.Root, o.Project.Name)
}

func parseHostOverminds(ps, root, name string) []hostOvermind {
	var found []hostOvermind
	for _, line := range strings.Split(ps, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || filepath.Base(f[1]) != "overmind" || f[2] != "start" {
			continue
		}
		if !strings.Contains(line+" ", " -d "+root+" ") {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		for i := 3; i < len(f)-1; i++ {
			if f[i] == "-w" && strings.HasPrefix(f[i+1], name+"-") {
				found = append(found, hostOvermind{PID: pid, Repo: strings.TrimPrefix(f[i+1], name+"-")})
				break
			}
		}
	}
	return found
}

// HostWarnings reports the project's overmind daemons that should not be
// running: those for a repo that is no longer in the project, and extra
// ones for a repo, such as from starting it twice. It only warns, since
// they run on the host rather than in a disposable container.
func (o *Orchestrator) HostWarnings() []string {
	byRepo := map[string][]int{}
	var order []string
	for _, d := range o.hostOverminds() {
		if _, seen := byRepo[d.Repo]; !seen {
			order = append(order, d.Repo)
		}
		byRepo[d.Repo] = append(byRepo[d.Repo], d.PID)
	}
	var out []string
	for _, repo := range order {
		pids := byRepo[repo]
		list := joinInts(pids)
		switch {
		case !o.Project.IsRepo(repo):
			label := "pid"
			if len(pids) > 1 {
				label = "pids"
			}
			out = append(out, fmt.Sprintf("overmind for %s (%s %s) is still running, but %s is not a repo in this project; stop it with: neptuno down %s", repo, label, list, repo, repo))
		case len(pids) > 1:
			out = append(out, fmt.Sprintf("%s has %d overmind daemons (pids %s), probably from starting it twice; stop them with: neptuno down %s", repo, len(pids), list, repo))
		}
	}
	return out
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}

// stopHostOverminds stops the given overmind daemons with SIGTERM, which
// overmind treats like quit: those still running for a repo after `overmind
// quit`, such as one whose socket a second start replaced.
func (o *Orchestrator) stopHostOverminds(repo string, daemons []hostOvermind) {
	for _, d := range daemons {
		if d.Repo != repo {
			continue
		}
		fmt.Fprintf(o.Log, "Stopping overmind for %s (pid %d)\n", repo, d.PID)
		_ = o.Runner.Check(o.Project.Root, "kill", "-TERM", strconv.Itoa(d.PID))
	}
}

// countFor counts the daemons for repo.
func countFor(daemons []hostOvermind, repo string) int {
	n := 0
	for _, d := range daemons {
		if d.Repo == repo {
			n++
		}
	}
	return n
}
