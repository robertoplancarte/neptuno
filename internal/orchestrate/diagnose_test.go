package orchestrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseConflict(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want Conflict
		ok   bool
	}{
		{"=> Booting Puma\nA server is already running (pid: 51, file: /rails/tmp/pids/server.pid).\nExiting\n", Conflict{PID: 51, PIDFile: "/rails/tmp/pids/server.pid"}, true},
		{`Address already in use - bind(2) for "0.0.0.0" port 3000 (Errno::EADDRINUSE)`, Conflict{Port: 3000}, true},
		{"Error: listen EADDRINUSE: address already in use :::4000\n    at Server.setupListenHandle", Conflict{Port: 4000}, true},
		{"ERROR:    [Errno 98] error while attempting to bind on address ('0.0.0.0', 8000): address already in use", Conflict{Port: 8000}, true},
		{"listen tcp :8080: bind: address already in use", Conflict{Port: 8080}, true},
		{"OSError: [Errno 98] Address already in use", Conflict{}, true},
		{"NoMethodError: undefined method `x'", Conflict{}, false},
	} {
		got, ok := ParseConflict(tc.out)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseConflict(%q) = %+v, %v; want %+v, %v", tc.out, got, ok, tc.want, tc.ok)
		}
	}
}

// TestStatusExplainsPortConflicts records a process that exited on a taken
// port, and checks the warning for each holder the container probe can
// report.
func TestStatusExplainsPortConflicts(t *testing.T) {
	o, f := setup(t)
	root := o.Project.Root
	if err := os.MkdirAll(filepath.Join(root, ".neptuno/run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, runFile("api")), []byte("now\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordExit(root, "api", "web", `Address already in use - bind(2) for "0.0.0.0" port 3000 (Errno::EADDRINUSE)`); err != nil {
		t.Fatal(err)
	}
	s := ServiceStatus{Name: "api", Repo: true, RunMode: "container", Container: "running", Overmind: true,
		Processes: []ProcessStatus{{"web", "dead"}, {"worker", "running"}}}
	for _, tc := range []struct{ probe, want string }{
		{"pid=88\npgid=80\ncmd=puma 6.4.2 (tcp://0.0.0.0:3000) [app]\nrun=before\nproc=web\n",
			"web could not start: port 3000 is held by pid 88 (puma 6.4.2 (tcp://0.0.0.0:3000) [app]), left by an earlier run; neptuno restart api stops it"},
		{"pid=88\npgid=80\ncmd=puma\nrun=now\nproc=admin\n", "web could not start: port 3000 is held by pid 88 (puma), which this run's admin started"},
		{"pid=88\npgid=88\ncmd=bin/rails s\n", "web could not start: port 3000 is held by pid 88 (bin/rails s), which neptuno did not start (such as one run by hand with docker compose exec); stop it with: docker compose exec api kill 88"},
		{"", "web could not start: port 3000 was already in use, but it is free now; try neptuno restart api:web"},
	} {
		f.execOut, f.execs = tc.probe, nil
		got := o.exitWarnings(s)
		if !reflect.DeepEqual(got, []string{tc.want}) {
			t.Errorf("probe %q:\n got %q\nwant %q", tc.probe, got, tc.want)
		}
		if len(f.execs) != 1 || !strings.Contains(f.execs[0], "-e NEPTUNO_PORT=3000 -e NEPTUNO_PID= api sh -c") {
			t.Errorf("probe command = %q", f.execs)
		}
	}

	// A clean exit clears what the last one recorded.
	if err := RecordExit(root, "api", "web", "Goodbye\n"); err != nil {
		t.Fatal(err)
	}
	if got := o.exitWarnings(s); got != nil {
		t.Errorf("after a clean exit: %q", got)
	}

	// Rails' stale pid file, naming the container's PID 1.
	if err := RecordExit(root, "api", "web", "A server is already running (pid: 1, file: /app/tmp/pids/server.pid).\n"); err != nil {
		t.Fatal(err)
	}
	f.execOut = "pid=1\npgid=1\ncmd=/bin/sh /entrypoint.sh\n"
	want := "web could not start: a server is already running (pid 1), but pid 1 is the container's main process, so /app/tmp/pids/server.pid is left from an earlier container; remove it and run neptuno restart api:web"
	if got := o.exitWarnings(s); !reflect.DeepEqual(got, []string{want}) {
		t.Errorf("stale pid file:\n got %q\nwant %q", got, want)
	}

	// On the host there is no container to look in.
	s.RunMode, f.execs = "host", nil
	if got := o.exitWarnings(s); !reflect.DeepEqual(got, []string{"web could not start: a server is already running (pid 1)"}) || f.execs != nil {
		t.Errorf("host repo: %q, execs %q", got, f.execs)
	}
}

func TestHostWarningsAndDown(t *testing.T) {
	o, f := setup(t)
	root := o.Project.Root
	f.ps = strings.Join([]string{
		"  101 overmind start --daemonize --any-can-die -f " + root + "/.neptuno/procfiles/api/Procfile -d " + root + " -s .neptuno/run/api.sock -w acme-api -F x",
		"  102 /opt/homebrew/bin/overmind start --daemonize -d " + root + " -s .neptuno/run/api.sock -w acme-api",
		"  103 overmind start --daemonize -d " + root + " -s .neptuno/run/shop.sock -w acme-shop",
		"  104 overmind start --daemonize -d " + root + " -s .neptuno/run/shop.sock -w acme-shop",
		"  105 overmind start --daemonize -d " + root + " -s .neptuno/run/docs.sock -w acme-docs",
		"  106 overmind start --daemonize -d /elsewhere -s .neptuno/run/shop.sock -w acme-shop",
		"  107 tmux -C -L overmind-acme-shop-x new -n x -s acme-shop",
		"  108 vim overmind start -w acme-shop -d " + root,
	}, "\n")
	want := []string{
		"api has 2 overmind daemons (pids 101, 102), probably from starting it twice; stop them with: neptuno down api",
		"overmind for shop (pids 103, 104) is still running, but shop is not a repo in this project; stop it with: neptuno down shop",
	}
	if got := o.HostWarnings(); !reflect.DeepEqual(got, want) {
		t.Errorf("HostWarnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if err := o.Down([]string{"shop"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.cmds, "\n"); !strings.Contains(got, "kill -TERM 103\nkill -TERM 104") || strings.Contains(got, "kill -TERM 106") {
		t.Errorf("down shop:\n%s", got)
	}
	if err := o.Down([]string{"nope"}); err == nil {
		t.Error("down accepted a name with no service, repo or daemon")
	}

	// One api daemon answering its socket is left to overmind quit.
	f.cmds, f.ps = nil, "  101 overmind start -d "+root+" -w acme-api"
	f.overminds[".neptuno/run/api.sock"] = true
	if err := os.MkdirAll(filepath.Join(root, ".neptuno/run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".neptuno/run/api.sock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := o.Down([]string{"api"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.cmds, "\n"); strings.Contains(got, "kill") || !strings.Contains(got, "overmind quit") {
		t.Errorf("down api:\n%s", got)
	}
}
