package orchestrate

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/robertoplancarte/neptuno/internal/mcp"
	"github.com/robertoplancarte/neptuno/internal/proxy"
)

// RestartOptions configures Restart.
type RestartOptions struct {
	// Wait is how long Restart waits for the restarted processes to run
	// again and the repo's app to answer its health check. Zero returns as
	// soon as overmind has been told to restart.
	Wait time.Duration
}

// DefaultRestartWait is how long `neptuno restart` waits for the app.
const DefaultRestartWait = 90 * time.Second

// pollInterval is how often Restart looks at overmind and the app while it
// waits.
var pollInterval = 500 * time.Millisecond

// Restart restarts a repo's processes, or one of them, through its running
// overmind. When the repo has more than one overmind daemon, or one its
// socket no longer reaches, it replaces them with one, which restarts every
// process. With opts.Wait it then waits for the processes to run again and
// for the app to answer its health check, so a test run right after it
// doesn't meet the proxy's 502s.
func (o *Orchestrator) Restart(repo, process string, opts RestartOptions) error {
	p := o.Project
	if !p.IsRepo(repo) {
		return fmt.Errorf("unknown repo %q", repo)
	}
	sock := socket(repo)
	running := o.Runner.Check(p.Root, "overmind", "status", "-s", sock)
	daemons := o.hostOverminds()
	n := countFor(daemons, repo)
	if !running && n == 0 {
		return fmt.Errorf("%s is not running; start it with `neptuno up %s`", repo, repo)
	}

	var before map[string]processState
	if n > 1 || !running {
		// openWindow says why it replaces them.
		if process != "" {
			fmt.Fprintf(o.Log, "Restarting all of %s's processes, not only %s\n", repo, process)
			process = ""
		}
		if ok, err := o.writeProcfile(repo); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%s has no Procfile processes to restart", repo)
		}
		if err := o.openWindow(repo, true); err != nil {
			return err
		}
	} else {
		if err := o.reopenClosedWindows(repo, process); err != nil {
			return err
		}
		o.sweep(repo, o.currentRun(repo))
		before = o.processStates(repo)
		args := []string{"restart", "-s", sock}
		if process != "" {
			args = append(args, process)
		}
		fmt.Fprintf(o.Log, "Restarting %s\n", restartLabel(repo, process))
		if err := o.Runner.Run(p.Root, "overmind", args...); err != nil {
			return err
		}
	}
	if opts.Wait <= 0 {
		return nil
	}
	return o.waitUp(repo, process, before, opts.Wait)
}

func restartLabel(repo, process string) string {
	if process == "" {
		return repo + "'s processes"
	}
	return repo + ":" + process
}

// warnStray says why Restart or Up replaces a repo's overmind daemons.
func (o *Orchestrator) warnStray(repo string, n int, answering bool) {
	if n > 1 {
		fmt.Fprintf(o.Log, "%s has %d overmind daemons, probably from starting it twice; replacing them with one\n", repo, n)
	} else {
		fmt.Fprintf(o.Log, "overmind for %s is running but does not answer on its socket; replacing it\n", repo)
	}
}

// stopStray stops the repo's overmind daemons that `overmind quit` on its
// socket did not reach, and waits for them to exit, so the next overmind
// doesn't start its processes while theirs still hold the ports.
func (o *Orchestrator) stopStray(repo string, daemons []hostOvermind, quitAnswered bool) {
	n := countFor(daemons, repo)
	if n == 0 || n == 1 && quitAnswered {
		return
	}
	o.stopHostOverminds(repo, daemons)
	deadline := time.Now().Add(5 * time.Second)
	for {
		alive := o.aliveOverminds(repo, daemons)
		if len(alive) == 0 {
			return
		}
		if time.Now().After(deadline) {
			for _, pid := range alive {
				fmt.Fprintf(o.Log, "overmind for %s (pid %d) did not stop; killing it\n", repo, pid)
				_ = o.Runner.Check(o.Project.Root, "kill", "-KILL", strconv.Itoa(pid))
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// aliveOverminds lists the pids of the repo's daemons that still run.
func (o *Orchestrator) aliveOverminds(repo string, daemons []hostOvermind) []int {
	var alive []int
	for _, d := range daemons {
		if d.Repo == repo && o.Runner.Check(o.Project.Root, "kill", "-0", strconv.Itoa(d.PID)) {
			alive = append(alive, d.PID)
		}
	}
	return alive
}

// processState is one process in `overmind status`.
type processState struct {
	PID    int
	Status string
}

// processStates reads the repo's overmind status, by process name. It is
// empty when overmind does not answer.
func (o *Orchestrator) processStates(repo string) map[string]processState {
	out, err := o.Runner.Output(o.Project.Root, "overmind", "status", "-s", socket(repo))
	if err != nil {
		return nil
	}
	return parseProcessStates(out)
}

// parseProcessStates reads `overmind status`, a PROCESS PID STATUS table.
func parseProcessStates(out string) map[string]processState {
	states := map[string]processState{}
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 && len(f) > 0 && f[0] == "PROCESS" || len(f) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[1])
		states[f[0]] = processState{PID: pid, Status: f[len(f)-1]}
	}
	return states
}

// waitUp waits for the restarted processes to run again, then for the
// repo's app to answer its health check. before holds the processes as
// they were before the restart, or is nil after a fresh overmind start.
func (o *Orchestrator) waitUp(repo, process string, before map[string]processState, wait time.Duration) error {
	start := time.Now()
	deadline := start.Add(wait)

	// The processes being restarted that were running: overmind stops each
	// one and starts it again with a new pid. One that had already exited
	// is not waited for; the health check tells whether the app is up.
	targets := map[string]int{}
	if before == nil {
		for name := range o.processStates(repo) {
			targets[name] = -1
		}
	} else {
		for name, st := range before {
			if (process == "" || name == process) && st.PID > 0 && st.Status == "running" {
				targets[name] = st.PID
			}
		}
	}
	for len(targets) > 0 {
		states := o.processStates(repo)
		for name, old := range targets {
			st, ok := states[name]
			if !ok {
				delete(targets, name)
				continue
			}
			if st.PID > 0 && st.PID != old && st.Status == "running" {
				delete(targets, name)
			}
		}
		if len(targets) == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not start again within %s; its window in %s shows why", strings.Join(sortedKeys(targets), ", "), wait, o.SessionName())
		}
		time.Sleep(pollInterval)
	}

	url, why := o.healthURL(repo)
	if url == "" {
		if why != "" {
			fmt.Fprintf(o.Log, "%s's processes are running; %s\n", repo, why)
		}
		return nil
	}
	fmt.Fprintf(o.Log, "Waiting for %s to answer at %s\n", repo, url)
	last := ""
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		code, err := o.probe(ctx, url)
		cancel()
		switch {
		case err != nil:
			last = err.Error()
		case code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout:
			last = fmt.Sprintf("%d %s", code, http.StatusText(code))
		default:
			took := time.Since(start).Round(100 * time.Millisecond)
			if code >= 400 {
				fmt.Fprintf(o.Log, "warning: %s answers at %s, but with %d %s (after %s)\n", repo, url, code, http.StatusText(code), took)
			} else {
				fmt.Fprintf(o.Log, "%s is up: %s answered %d after %s\n", repo, url, code, took)
			}
			return nil
		}
		// A process that exits while the app boots won't answer.
		if dead := o.deadProcesses(repo, process); len(dead) > 0 {
			return fmt.Errorf("%s exited while %s was starting; its window in %s shows why", strings.Join(dead, ", "), repo, o.SessionName())
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not answer at %s within %s (last: %s); its window in %s shows why", repo, url, wait, last, o.SessionName())
		}
		time.Sleep(pollInterval)
	}
}

// deadProcesses lists the repo's processes overmind reports dead, of those
// restarted.
func (o *Orchestrator) deadProcesses(repo, process string) []string {
	var dead []string
	for name, st := range o.processStates(repo) {
		if st.Status == "dead" && (process == "" || name == process) {
			dead = append(dead, name)
		}
	}
	slices.Sort(dead)
	return dead
}

// healthURL is where the repo's app answers its health check, through the
// reverse proxy, or "" with why when there is none to check.
func (o *Orchestrator) healthURL(repo string) (url, why string) {
	p := o.Project
	check := strings.TrimSpace(p.Config.Repos[repo].Health)
	switch strings.ToLower(check) {
	case "off", "false", "none", "no":
		return "", ""
	}
	if strings.HasPrefix(check, "http://") || strings.HasPrefix(check, "https://") {
		return check, ""
	}
	if check == "" {
		check = "/"
		if isRails(p.Path("repos", repo)) {
			check = "/up"
		}
	}
	if !strings.HasPrefix(check, "/") {
		check = "/" + check
	}
	containers := o.composeContainers()
	sites, published := o.proxySites(containers)
	var base string
	for _, l := range proxy.URLs(sites, repo, published) {
		if !l.Pattern {
			base = strings.TrimSuffix(l.URL, "/")
			break
		}
	}
	if base == "" {
		return "", "the Caddyfile has no site for it, so there is no URL to check"
	}
	if _, ok := p.Services[proxyService]; ok && containers[proxyService].State != "running" {
		return "", "the proxy is not running, so its URL can't be checked"
	}
	return base + check, ""
}

// isRails reports whether dir holds a Rails app, which answers /up once it
// has booted.
func isRails(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "config", "application.rb"))
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "bin", "rails"))
	return err == nil
}

// healthClient sends the health checks: *.localhost goes to this machine,
// redirects are not followed, and Caddy's local certificates are accepted.
var healthClient = func() *http.Client {
	c := mcp.HTTPClient()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	c.Transport.(*http.Transport).DisableKeepAlives = true
	return c
}()

// probe sends a GET to url and returns the status code. Tests replace it.
func (o *Orchestrator) probe(ctx context.Context, url string) (int, error) {
	if o.healthProbe != nil {
		return o.healthProbe(ctx, url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := healthClient.Do(req)
	if err != nil {
		// Drop the "Get <url>:" the client wraps the error in.
		var uerr *neturl.Error
		if errors.As(err, &uerr) {
			return 0, uerr.Err
		}
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return resp.StatusCode, nil
}

func sortedKeys(m map[string]int) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
