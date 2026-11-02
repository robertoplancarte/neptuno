package procs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/robertoplancarte/neptuno/internal/procfile"
)

// fakeDocker writes a docker stand-in that runs `docker compose exec`
// locally the way the container would: in a new session, with the -e
// variables set and a scratch folder standing in for /dev/shm.
func fakeDocker(t *testing.T) (docker, shm string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the scripts read /proc")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	dir := t.TempDir()
	shm = filepath.Join(dir, "shm")
	docker = filepath.Join(dir, "docker")
	script := `#!/bin/sh
shift 2
[ "$1" = -T ] && shift
while [ "$1" = -e ]; do export "$2"; shift 2; done
shift
script=$(printf '%s' "$3" | sed "s#/dev/shm/#` + shm + `/#g")
exec setsid -w sh -c "$script"
`
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return docker, shm
}

// records lists the files under the scratch neptuno-procs folder.
func records(shm string) []string {
	files, _ := filepath.Glob(filepath.Join(shm, "neptuno-procs", "*", "*"))
	return files
}

// alive reports whether the process group a record names still exists.
func alive(t *testing.T, record string) bool {
	t.Helper()
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.Fields(string(b))[0])
	return syscall.Kill(-pid, 0) == nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func execAsync(opts ExecOptions) (chan int, chan os.Signal) {
	sigs := make(chan os.Signal, 1)
	opts.Signals = sigs
	done := make(chan int, 1)
	go func() { done <- Exec(opts) }()
	return done, sigs
}

func TestExecForwardsInterrupt(t *testing.T) {
	docker, shm := fakeDocker(t)
	done, sigs := execAsync(ExecOptions{
		Docker: docker, Service: "api", Process: "web", Run: "r1",
		Script: procfile.ContainerScript("api", `trap 'exit 0' INT; while :; do sleep 0.1; done`, false),
	})
	waitFor(t, "the record", func() bool { return len(records(shm)) == 1 })
	rec := records(shm)[0]
	if !strings.HasPrefix(filepath.Base(rec), "web.") || filepath.Base(filepath.Dir(rec)) != "r1" {
		t.Errorf("record = %s, want r1/web.<id>", rec)
	}
	sigs <- syscall.SIGINT
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d, want 0 from the trap", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exec did not return after SIGINT")
	}
	if len(records(shm)) != 0 {
		t.Errorf("records left: %v", records(shm))
	}
}

// TestExecStopsGroupOnHangup is a Procfile line whose terminal goes away:
// the command, which ignores SIGINT, and its children are stopped.
func TestExecStopsGroupOnHangup(t *testing.T) {
	docker, shm := fakeDocker(t)
	done, sigs := execAsync(ExecOptions{
		Docker: docker, Service: "api", Process: "web", Run: "r1",
		Script: procfile.ContainerScript("api", `trap '' INT; sleep 1000 & while :; do sleep 0.1; done`, false),
	})
	waitFor(t, "the record", func() bool { return len(records(shm)) == 1 })
	b, err := os.ReadFile(records(shm)[0])
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.Fields(string(b))[0])
	sigs <- syscall.SIGHUP
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("Exec did not return after SIGHUP")
	}
	if syscall.Kill(-pid, 0) == nil {
		t.Error("the command's process group survived")
	}
}

func TestExecPassesExitStatus(t *testing.T) {
	docker, shm := fakeDocker(t)
	code := Exec(ExecOptions{
		Docker: docker, Service: "api", Process: "web", Run: "r1",
		Script: procfile.ContainerScript("api", "exit 3", false),
	})
	if code != 3 {
		t.Errorf("exit code %d, want 3", code)
	}
	waitFor(t, "the record to go", func() bool { return len(records(shm)) == 0 })
}

// TestSweep starts processes under two runs and an unrecorded one, and
// checks that the sweep stops only those of the other run.
func TestSweep(t *testing.T) {
	docker, shm := fakeDocker(t)
	start := func(run, proc, command string) *exec.Cmd {
		cmd := exec.Command(docker, "compose", "exec", "-e", RunEnv+"="+run, "-e", ProcEnv+"="+proc, "api", "sh", "-c",
			procfile.ContainerScript("api", command, false))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	start("old", "web.a1", "trap '' TERM; while :; do sleep 0.1; done")
	start("old", "worker.b2", "sleep 1000")
	start("current", "web.c3", "sleep 1000")
	waitFor(t, "the records", func() bool { return len(records(shm)) == 3 })

	// A record whose PID now belongs to another process: never touched.
	stranger := exec.Command("setsid", "sleep", "1000")
	if err := stranger.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stranger.Process.Kill(); _ = stranger.Wait() })
	if err := os.MkdirAll(filepath.Join(shm, "neptuno-procs", "older"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(shm, "neptuno-procs", "older", "reused.d4"), []byte(strconv.Itoa(stranger.Process.Pid)+" 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(shm, "neptuno-procs", "older", "init.e5"), []byte("1 1\n"), 0o644)

	script := strings.ReplaceAll(SweepScript, "/dev/shm/", shm+"/")
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), RunEnv+"=current")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ParseSweep(string(out)), ","); got != "web,worker" {
		t.Errorf("swept %q, want web,worker", got)
	}
	left := records(shm)
	if len(left) != 1 || !strings.Contains(left[0], "/current/web.c3") {
		t.Errorf("records left: %v", left)
	}
	if !alive(t, left[0]) {
		t.Error("the current run's process was stopped")
	}
	if syscall.Kill(stranger.Process.Pid, 0) != nil {
		t.Error("the process with a reused PID was stopped")
	}
}
