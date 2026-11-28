package orchestrate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/robertoplancarte/neptuno/internal/project"
)

// restartSetup makes api a Rails app behind http://api.localhost, with its
// overmind running web and worker.
func restartSetup(t *testing.T) (*Orchestrator, *fakeRunner, *strings.Builder) {
	t.Helper()
	old := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	o, f := setup(t)
	root := o.Project.Root
	for path, content := range map[string]string{
		"Caddyfile":                       "http://api.localhost {\n\treverse_proxy api:3000\n}\n",
		"repos/api/config/application.rb": "",
		"repos/api/bin/rails":             "",
		".neptuno/procfiles/api/Procfile": "web: x\nworker: y\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.overminds[socket("api")] = true
	var log strings.Builder
	o.Log = &log
	return o, f, &log
}

const (
	statusBefore = "PROCESS PID STATUS\nweb 100 running\nworker 101 running\n"
	statusAfter  = "PROCESS PID STATUS\nweb 200 running\nworker 201 running\n"
)

// probes answers the health checks with codes in turn, repeating the last,
// and records the URLs.
func probes(o *Orchestrator, codes ...int) *[]string {
	var urls []string
	o.healthProbe = func(_ context.Context, url string) (int, error) {
		urls = append(urls, url)
		code := codes[0]
		if len(codes) > 1 {
			codes = codes[1:]
		}
		if code == 0 {
			return 0, errors.New("connection refused")
		}
		return code, nil
	}
	return &urls
}

func TestRestartWaitsForNewProcessesAndHealth(t *testing.T) {
	o, f, log := restartSetup(t)
	// The old pids still show right after the restart, then web is between
	// processes, then both run again.
	f.statuses = []string{statusBefore, statusBefore, "PROCESS PID STATUS\nweb 0 running\nworker 201 running\n", statusAfter}
	urls := probes(o, 0, 502, 200)

	if err := o.Restart("api", "", RestartOptions{Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.cmds, "overmind restart -s .neptuno/run/api.sock") {
		t.Errorf("commands:\n%s", strings.Join(f.cmds, "\n"))
	}
	if len(*urls) != 3 || (*urls)[0] != "http://api.localhost/up" {
		t.Errorf("probed %q, want http://api.localhost/up three times", *urls)
	}
	if !strings.Contains(log.String(), "Waiting for api to answer at http://api.localhost/up") ||
		!strings.Contains(log.String(), "api is up: http://api.localhost/up answered 200") {
		t.Errorf("log:\n%s", log)
	}
}

func TestRestartOneProcessWaitsOnlyForIt(t *testing.T) {
	o, f, _ := restartSetup(t)
	// worker keeps its pid: only web is restarted.
	f.statuses = []string{statusBefore, "PROCESS PID STATUS\nweb 200 running\nworker 101 running\n"}
	probes(o, 200)
	if err := o.Restart("api", "web", RestartOptions{Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
}

func TestRestartTimesOut(t *testing.T) {
	o, f, _ := restartSetup(t)
	f.statuses = []string{statusBefore, statusAfter}
	probes(o, 502)
	err := o.Restart("api", "", RestartOptions{Wait: 30 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "api did not answer at http://api.localhost/up") ||
		!strings.Contains(err.Error(), "last: 502 Bad Gateway") {
		t.Errorf("err = %v", err)
	}

	// A process that never comes back.
	f.statuses = []string{statusBefore}
	err = o.Restart("api", "", RestartOptions{Wait: 30 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "web, worker did not start again") {
		t.Errorf("err = %v", err)
	}
}

func TestRestartFailsWhenAProcessDiesWhileBooting(t *testing.T) {
	o, f, _ := restartSetup(t)
	f.statuses = []string{statusBefore, statusAfter, statusAfter, "PROCESS PID STATUS\nweb 200 dead\nworker 201 running\n"}
	probes(o, 502)
	err := o.Restart("api", "", RestartOptions{Wait: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "web exited while api was starting") {
		t.Errorf("err = %v", err)
	}
}

func TestRestartHealthConfig(t *testing.T) {
	o, f, log := restartSetup(t)
	f.statuses = []string{statusBefore, statusAfter}

	o.Project.Config.Repos["api"] = project.RepoConfig{Health: "off"}
	urls := probes(o, 200)
	if err := o.Restart("api", "", RestartOptions{Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if len(*urls) != 0 {
		t.Errorf("probed %q with health: off", *urls)
	}

	o.Project.Config.Repos["api"] = project.RepoConfig{Health: "healthz"}
	f.statuses = []string{statusBefore, statusAfter}
	urls = probes(o, 404)
	if err := o.Restart("api", "", RestartOptions{Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if len(*urls) != 1 || (*urls)[0] != "http://api.localhost/healthz" {
		t.Errorf("probed %q", *urls)
	}
	if !strings.Contains(log.String(), "warning: api answers at http://api.localhost/healthz, but with 404 Not Found") {
		t.Errorf("log:\n%s", log)
	}

	// No wait: no probe.
	urls = probes(o, 200)
	if err := o.Restart("api", "", RestartOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(*urls) != 0 {
		t.Errorf("probed %q without waiting", *urls)
	}
}

func TestHealthURLDefaults(t *testing.T) {
	o, _, _ := restartSetup(t)
	if url, _ := o.healthURL("api"); url != "http://api.localhost/up" {
		t.Errorf("rails: %q", url)
	}
	if err := os.Remove(o.Project.Path("repos", "api", "bin", "rails")); err != nil {
		t.Fatal(err)
	}
	if url, _ := o.healthURL("api"); url != "http://api.localhost/" {
		t.Errorf("other: %q", url)
	}
	if url, why := o.healthURL("web"); url != "" || !strings.Contains(why, "no site") {
		t.Errorf("no site: %q, %q", url, why)
	}
}

// TestRestartReplacesStrayOverminds checks that a repo with two overmind
// daemons gets both stopped and one started in their place, which restarts
// every process.
func TestRestartReplacesStrayOverminds(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f, log := restartSetup(t)
	root := o.Project.Root
	f.ps = "  11 overmind start -d " + root + " -s .neptuno/run/api.sock -w acme-api\n" +
		"  12 overmind start -d " + root + " -s .neptuno/run/api.sock -w acme-api\n"
	f.alive = map[int]bool{11: true, 12: true}
	f.statuses = []string{statusAfter}
	probes(o, 200)

	if err := o.Restart("api", "web", RestartOptions{Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.cmds {
		got = append(got, strings.Fields(c)[0]+" "+strings.Fields(c)[1])
	}
	want := []string{"overmind quit", "kill -TERM", "kill -TERM", "overmind start"}
	for _, w := range want {
		i := slices.Index(got, w)
		if i < 0 {
			t.Fatalf("commands:\n%s\nwant %q in order", strings.Join(f.cmds, "\n"), want)
		}
		got = got[i+1:]
	}
	if slices.Contains(f.cmds, "overmind restart -s .neptuno/run/api.sock web") {
		t.Error("restarted through a socket shared by two daemons")
	}
	for _, s := range []string{"api has 2 overmind daemons", "Restarting all of api's processes, not only web", "api is up"} {
		if !strings.Contains(log.String(), s) {
			t.Errorf("log lacks %q:\n%s", s, log)
		}
	}
}

// TestRestartRecoversAnOvermindItsSocketLost checks that an overmind that
// runs but does not answer on its socket is replaced instead of reported
// as not running.
func TestRestartRecoversAnOvermindItsSocketLost(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f, log := restartSetup(t)
	delete(f.overminds, socket("api"))
	f.ps = "  11 overmind start -d " + o.Project.Root + " -s .neptuno/run/api.sock -w acme-api\n"
	f.alive = map[int]bool{11: true}
	f.statuses = []string{statusAfter}
	probes(o, 200)
	if err := o.Restart("api", "", RestartOptions{Wait: time.Second}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.cmds, "kill -TERM 11") || !strings.Contains(log.String(), "does not answer on its socket") {
		t.Errorf("commands:\n%s\nlog:\n%s", strings.Join(f.cmds, "\n"), log)
	}
}

func TestParseProcessStates(t *testing.T) {
	got := parseProcessStates("PROCESS   PID       STATUS\nweb       4242      running\ncss       0         dead\n")
	if got["web"] != (processState{4242, "running"}) || got["css"] != (processState{0, "dead"}) || len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/up" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	o := &Orchestrator{}
	// *.localhost goes to this machine.
	url := strings.Replace(srv.URL, "127.0.0.1", "api.localhost", 1)
	if code, err := o.probe(context.Background(), url+"/up"); err != nil || code != http.StatusFound {
		t.Errorf("/up: %d, %v; want the redirect itself", code, err)
	}
	if code, _ := o.probe(context.Background(), url+"/"); code != http.StatusServiceUnavailable {
		t.Errorf("/: %d", code)
	}
	srv.Close()
	if _, err := o.probe(context.Background(), url+"/up"); err == nil || strings.Contains(err.Error(), "Get ") {
		t.Errorf("closed: %v, want the bare dial error", err)
	}
}
