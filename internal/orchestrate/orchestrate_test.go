package orchestrate

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/state"
)

// fakeRunner records commands and pretends tmux sessions and overmind
// daemons exist once created.
type fakeRunner struct {
	cmds     []string
	sessions bool
	windows  map[string]bool
	// overminds holds the sockets of running overmind daemons.
	overminds map[string]bool
	// containers maps a service to the IDs `docker compose ps` returns on
	// successive calls.
	containers map[string][]string
	// overmindWindows is what list-windows prints for overmind's session.
	overmindWindows string
}

func (f *fakeRunner) Run(dir, name string, args ...string) error {
	f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
	if name == "tmux" && args[0] == "kill-window" {
		target := args[len(args)-1]
		delete(f.windows, target[strings.LastIndex(target, ":")+1:])
	}
	if name == "tmux" && (args[0] == "new-session" || args[0] == "new-window") {
		f.sessions = true
		for i, a := range args {
			if a == "-n" {
				f.windows[args[i+1]] = true
			}
		}
	}
	return nil
}

func (f *fakeRunner) Check(dir, name string, args ...string) bool {
	if name == "overmind" {
		sock := args[len(args)-1]
		switch args[0] {
		case "status":
			return f.overminds[sock]
		case "quit":
			f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
			ok := f.overminds[sock]
			delete(f.overminds, sock)
			return ok
		}
	}
	if name != "tmux" {
		return true
	}
	if args[0] == "switch-client" {
		f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
		return true
	}
	switch args[0] {
	case "has-session":
		return f.sessions
	case "list-panes":
		target := args[len(args)-1]
		return f.windows[target[strings.LastIndex(target, ":")+1:]]
	}
	return false
}

func (f *fakeRunner) Output(dir, name string, args ...string) (string, error) {
	if name == "overmind" && args[0] == "start" {
		f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
		for i, a := range args {
			if a == "-s" {
				f.overminds[args[i+1]] = true
			}
		}
		return "", nil
	}
	if name == "tmux" && slices.Contains(args, "list-windows") {
		return f.overmindWindows, nil
	}
	svc := args[len(args)-1]
	ids := f.containers[svc]
	if len(ids) == 0 {
		return "", nil
	}
	f.containers[svc] = ids[1:]
	return ids[0], nil
}

func (f *fakeRunner) Attach(dir, name string, args ...string) error {
	f.cmds = append(f.cmds, "attach: "+name+" "+strings.Join(args, " "))
	return nil
}

func (f *fakeRunner) LookPath(string) error { return nil }

func setup(t *testing.T) (*Orchestrator, *fakeRunner) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"compose.yml": `name: acme
services:
  postgres: {image: postgres:17}
  valkey: {image: valkey/valkey:8}
  api:
    build: dockerfiles/api
    env_file: .neptuno/env/api.env
    depends_on: [postgres]
  web:
    build: dockerfiles/web
    depends_on: [api, valkey]
`,
		"neptuno.yml":                   "repos:\n  docs:\n    run: host\n",
		"environments/_shared/base.env": "DATABASE_HOST=postgres\n",
		"environments/api/base.env":     "PORT=3000\n",
		"environments/api/staging.env":  "API_URL=https://staging.example\n",
		"procfiles/api/Procfile":        "web: bin/rails s -b 0.0.0.0\nworker: bin/jobs\n",
		"procfiles/docs/Procfile":       "docs: npm run dev\n",
		"repos/api/README":              "",
		"repos/web/README":              "",
		"repos/docs/README":             "",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{windows: map[string]bool{}, overminds: map[string]bool{}, containers: map[string][]string{}}
	return &Orchestrator{Project: p, Runner: f, Log: io.Discard, Neptuno: "/bin/neptuno"}, f
}

func TestUp(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	if err := state.Save(o.Project.Root, state.State{RepoEnv: map[string]string{"api": "staging"}}); err != nil {
		t.Fatal(err)
	}

	if err := o.Up(UpOptions{Selected: []string{"web", "docs"}, Set: map[string]string{"DEBUG": "1"}}); err != nil {
		t.Fatal(err)
	}

	root := o.Project.Root
	var got []string
	for _, c := range f.cmds {
		got = append(got, strings.ReplaceAll(c, root, "ROOT"))
	}
	want := []string{
		"docker compose up --detach postgres api valkey web",
		"tmux new-session -d -s neptuno-acme -n neptuno -c ROOT /bin/neptuno",
		"overmind start --daemonize --any-can-die -f ROOT/.neptuno/procfiles/api/Procfile -d ROOT -s .neptuno/run/api.sock -w acme-api -F .neptuno/run/api.tmux.conf",
		"tmux new-window -d -t =neptuno-acme: -n api -c ROOT " + o.connectCommand("api"),
		"overmind start --daemonize --any-can-die -f ROOT/.neptuno/procfiles/docs/Procfile -d ROOT -s .neptuno/run/docs.sock -w acme-docs -F .neptuno/run/docs.tmux.conf",
		"tmux new-window -d -t =neptuno-acme: -n docs -c ROOT " + o.connectCommand("docs"),
		"attach: tmux attach-session -t =neptuno-acme",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	procfile := read(t, filepath.Join(root, ".neptuno/procfiles/api/Procfile"))
	if !strings.Contains(procfile, "web: exec docker compose exec api sh -c 'bin/rails s -b 0.0.0.0'\n") {
		t.Errorf("api Procfile not wrapped:\n%s", procfile)
	}
	if err := os.MkdirAll(filepath.Join(root, "dockerfiles/api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dockerfiles/api/entrypoint.sh"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.writeProcfile("api"); err != nil {
		t.Fatal(err)
	}
	if procfile := read(t, filepath.Join(root, ".neptuno/procfiles/api/Procfile")); !strings.Contains(procfile, "/dev/shm/neptuno-ready") {
		t.Errorf("api has an entrypoint.sh but its processes do not wait for it:\n%s", procfile)
	}
	if docs := read(t, filepath.Join(root, ".neptuno/procfiles/docs/Procfile")); !strings.Contains(docs, "docs: exec /bin/neptuno host-exec docs 'npm run dev'") {
		t.Errorf("docs Procfile not wrapped for host mode:\n%s", docs)
	}
	if _, err := os.Stat(filepath.Join(root, ".neptuno/procfiles/web/Procfile")); err == nil {
		t.Error("web has no Procfile but one was generated")
	}

	apiEnv := read(t, filepath.Join(root, ".neptuno/env/api.env"))
	for _, want := range []string{"DATABASE_HOST=postgres", "PORT=3000", "API_URL=https://staging.example", "DEBUG=1"} {
		if !strings.Contains(apiEnv, want+"\n") {
			t.Errorf("api.env missing %s:\n%s", want, apiEnv)
		}
	}
	// Repos outside the selection still get env files so compose loads.
	if _, err := os.Stat(filepath.Join(root, ".neptuno/env/web.env")); err != nil {
		t.Error("web.env not written")
	}

	st, err := state.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.LastUp, []string{"web", "docs"}) || st.RepoEnv["api"] != "staging" {
		t.Errorf("state = %+v", st)
	}
}

func TestUpSkipsRunningWindowsAndSwitchesInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-0/default,1,0")
	o, f := setup(t)
	f.sessions, f.windows["api"], f.overminds[socket("api")] = true, true, true

	if err := o.Up(UpOptions{Selected: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker compose up --detach postgres api",
		"tmux new-window -d -b -t =neptuno-acme:^ -n neptuno -c " + o.Project.Root + " /bin/neptuno",
		"tmux switch-client -t =neptuno-acme",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestUpRestartsOvermindWhenContainerIsRecreated(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	f.sessions, f.windows["api"], f.overminds[socket("api")] = true, true, true
	f.containers["api"] = []string{"old", "new"}

	if err := o.Up(UpOptions{Selected: []string{"api"}, Detach: true}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.cmds {
		got = append(got, strings.Fields(c)[0]+" "+strings.Fields(c)[1])
	}
	want := []string{"docker compose", "tmux new-window", "overmind quit", "tmux kill-window", "overmind start", "tmux new-window"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n%s\nwant prefixes %v", strings.Join(f.cmds, "\n"), want)
	}
}

func TestConnectCommandIsValidShell(t *testing.T) {
	o, _ := setup(t)
	if out, err := exec.Command("sh", "-n", "-c", o.connectCommand("api")).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v\n%s", err, out)
	}
}

func TestUpReconnectsToRunningOvermind(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	f.overminds[socket("api")] = true // its window was closed

	if err := o.Up(UpOptions{Selected: []string{"api"}, Detach: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 3 || !strings.HasPrefix(f.cmds[1], "tmux new-session -d -s neptuno-acme -n neptuno ") ||
		!strings.HasPrefix(f.cmds[2], "tmux new-window -d -t =neptuno-acme: -n api ") ||
		!strings.Contains(f.cmds[2], "overmind connect") {
		t.Errorf("commands:\n%s\nwant compose up and a window that connects, without restarting overmind", strings.Join(f.cmds, "\n"))
	}
}

func TestUpServicesOnlySkipsTmux(t *testing.T) {
	o, f := setup(t)
	if err := o.Up(UpOptions{Selected: []string{"postgres"}}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"docker compose up --detach postgres"}; !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands = %v, want %v", f.cmds, want)
	}
}

func TestDown(t *testing.T) {
	o, f := setup(t)
	f.sessions, f.windows["api"] = true, true

	if err := o.Down([]string{"api"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux kill-window -t =neptuno-acme:api",
		"docker compose stop api",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}

	f.cmds = nil
	if err := o.Down(nil); err != nil {
		t.Fatal(err)
	}
	if last := f.cmds[len(f.cmds)-1]; last != "docker compose down" {
		t.Errorf("Down() ended with %q, want docker compose down", last)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClaudeInRunningContainer(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	f.containers["api"] = []string{"abc123"}
	f.overminds[socket("api")] = true // started by an older neptuno: no tmux socket file
	var log strings.Builder
	o.Log = &log

	if err := o.Claude(ClaudeOptions{Repo: "api", Args: []string{"--continue"}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.cmds {
		got = append(got, strings.ReplaceAll(c, o.Project.Root, "ROOT"))
	}
	want := []string{
		"tmux new-session -d -s neptuno-acme -n neptuno -c ROOT /bin/neptuno",
		"tmux new-window -d -t =neptuno-acme: -n claude-api -c ROOT docker compose exec api claude --continue; " +
			`status=$?; if [ $status -ne 0 ]; then echo; echo "claude exited with status $status. Press enter to close this window."; read _; fi`,
		"tmux select-window -t =neptuno-acme:claude-api",
		"attach: tmux attach-session -t =neptuno-acme",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(log.String(), "does not mount /root/.claude") {
		t.Errorf("no warning about the missing config mount:\n%s", log.String())
	}
	if _, err := os.Stat(filepath.Join(o.Project.Root, ".neptuno/claude")); err != nil {
		t.Error(".neptuno/claude not created")
	}
}

func TestClaudeStartsStoppedContainerAndReusesWindow(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	compose := read(t, filepath.Join(o.Project.Root, "compose.yml"))
	compose = strings.Replace(compose, "    env_file: .neptuno/env/api.env\n",
		"    env_file: .neptuno/env/api.env\n    volumes: [\"./repos/api:/app\", \"./.neptuno/claude:/root/.claude\"]\n", 1)
	if err := os.WriteFile(filepath.Join(o.Project.Root, "compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	o.Log = &log

	if err := o.Claude(ClaudeOptions{Repo: "api", Detach: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 5 || !strings.HasPrefix(f.cmds[0], "docker compose up --detach postgres api") ||
		!strings.Contains(f.cmds[1], "new-session -d -s neptuno-acme -n neptuno ") ||
		!strings.HasPrefix(f.cmds[2], "overmind start ") ||
		!strings.Contains(f.cmds[3], "-n api ") || !strings.Contains(f.cmds[4], "-n claude-api ") {
		t.Errorf("commands:\n%s", strings.Join(f.cmds, "\n"))
	}
	if strings.Contains(log.String(), "does not mount") {
		t.Errorf("warned although the mount exists:\n%s", log.String())
	}

	f.cmds = nil
	f.containers["api"] = []string{"abc123"}
	if err := o.Claude(ClaudeOptions{Repo: "api", Detach: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 0 {
		t.Errorf("second call ran %q, want nothing", f.cmds)
	}
}

func TestClaudeRejectsHostAndUnknownRepos(t *testing.T) {
	o, _ := setup(t)
	if err := o.Claude(ClaudeOptions{Repo: "docs"}); err == nil || !strings.Contains(err.Error(), "runs on the host") {
		t.Errorf("host repo: %v", err)
	}
	if err := o.Claude(ClaudeOptions{Repo: "nope"}); err == nil {
		t.Error("unknown repo accepted")
	}
}

func TestUpWritesTmuxConfigWithPaneDiedHook(t *testing.T) {
	t.Setenv("TMUX", "")
	o, _ := setup(t)
	if err := o.Up(UpOptions{Selected: []string{"api"}, Detach: true}); err != nil {
		t.Fatal(err)
	}
	conf := read(t, filepath.Join(o.Project.Root, ".neptuno/run/api.tmux.conf"))
	if !strings.Contains(conf, "source-file -q ~/.tmux.conf") ||
		!strings.Contains(conf, "set-environment -g NEPTUNO_TMUX ") ||
		!strings.Contains(conf, `run-shell "echo '#{socket_path}' > `) ||
		!strings.Contains(conf, `set-hook -g pane-died "run-shell \"/bin/neptuno pane-died api '#{socket_path}' '#{pane_id}'`) {
		t.Errorf("tmux config:\n%s", conf)
	}
}

func TestDeadPaneCommand(t *testing.T) {
	cmd := DeadPaneCommand("shop", "web", "130")
	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		t.Fatal(err)
	}
	if want := "\nweb exited with status 130. Run it again with: neptuno restart shop:web\n\n"; string(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestRestart(t *testing.T) {
	o, f := setup(t)
	if err := o.Restart("api", "web"); err == nil {
		t.Error("restarted api although its overmind is not running")
	}
	f.overminds[socket("api")] = true
	if err := o.Restart("api", "web"); err != nil {
		t.Fatal(err)
	}
	if last := f.cmds[len(f.cmds)-1]; last != "overmind restart -s .neptuno/run/api.sock web" {
		t.Errorf("ran %q", last)
	}
}

func TestRestartReopensClosedWindows(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	if err := o.Up(UpOptions{Selected: []string{"api"}, Detach: true}); err != nil {
		t.Fatal(err)
	}
	// overmind's tmux server records its socket on start.
	if err := os.WriteFile(o.Project.Path(overmindTmuxFile("api")), []byte("/tmp/om\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.overmindWindows = "web\n" // the worker window was closed
	f.cmds = nil
	if err := o.Restart("api", ""); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"tmux -S /tmp/om new-window -d -n worker sleep 60",
		"overmind restart -s .neptuno/run/api.sock",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s", strings.Join(f.cmds, "\n"))
	}
	if err := o.Restart("api", "nope"); err == nil || !strings.Contains(err.Error(), "web, worker") {
		t.Errorf("unknown process: %v", err)
	}
}

func TestUseOuterTmux(t *testing.T) {
	t.Setenv(OuterTmuxEnv, "/tmp/tmux-0/default")
	t.Setenv("TMUX", "/tmp/tmux-0/overmind-acme-api-x,12,0")
	UseOuterTmux()
	if got := os.Getenv("TMUX"); got != "/tmp/tmux-0/default,0,0" {
		t.Errorf("inside overmind's session, TMUX = %q", got)
	}
	t.Setenv("TMUX", "/tmp/tmux-0/default,5,1")
	UseOuterTmux()
	if got := os.Getenv("TMUX"); got != "/tmp/tmux-0/default,5,1" {
		t.Errorf("in the outer session, TMUX = %q", got)
	}
}

func TestDeadPaneCommandIsMarked(t *testing.T) {
	if !ClosesOnExit(DeadPaneCommand("shop", "web", "1")) || ClosesOnExit("bin/rails server") {
		t.Error("IsDeadPaneCommand cannot tell the shell from the process")
	}
}

func TestClaudeOpensATabInTheReposWindow(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	f.containers["api"] = []string{"abc123", "abc123"}
	f.overminds[socket("api")] = true
	if err := os.MkdirAll(o.Project.Path(".neptuno", "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(o.Project.Path(overmindTmuxFile("api")), []byte("/tmp/om\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.overmindWindows = "web\nworker\n"
	o.Log = io.Discard

	if err := o.Claude(ClaudeOptions{Repo: "api"}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.cmds {
		got = append(got, strings.ReplaceAll(c, o.Project.Root, "ROOT"))
	}
	want := []string{
		"tmux -S /tmp/om new-window -d -n claude -c ROOT : neptuno-claude; docker compose exec api claude; " +
			`status=$?; if [ $status -ne 0 ]; then echo; echo "claude exited with status $status. Press enter to close this window."; read _; fi`,
		"tmux -S /tmp/om select-window -t :claude",
		"tmux select-window -t =neptuno-acme:api",
		"attach: tmux attach-session -t =neptuno-acme",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !ClosesOnExit(strings.TrimPrefix(want[0], "tmux -S /tmp/om new-window -d -n claude -c ROOT ")) {
		t.Error("the pane-died hook would turn Claude's tab into a shell")
	}

	f.cmds = nil
	f.overmindWindows = "web\nworker\nclaude\n"
	if err := o.Claude(ClaudeOptions{Repo: "api", Detach: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 1 || f.cmds[0] != "tmux -S /tmp/om select-window -t :claude" {
		t.Errorf("with the tab open, ran:\n%s", strings.Join(f.cmds, "\n"))
	}
}
