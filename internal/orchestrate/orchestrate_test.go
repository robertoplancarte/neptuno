package orchestrate

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/state"
)

// fakeRunner records commands and pretends tmux sessions exist once created.
type fakeRunner struct {
	cmds     []string
	sessions bool
	windows  map[string]bool
	// containers maps a service to the IDs `docker compose ps` returns on
	// successive calls.
	containers map[string][]string
}

func (f *fakeRunner) Run(dir, name string, args ...string) error {
	f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
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
	f := &fakeRunner{windows: map[string]bool{}, containers: map[string][]string{}}
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
		"tmux new-session -d -s neptuno-acme -n api -c ROOT overmind start -f ROOT/.neptuno/procfiles/api/Procfile -d ROOT -s .neptuno/run/api.sock -w acme-api; " +
			`echo; echo "overmind exited with status $?. Press enter to close this window."; read _`,
		"tmux new-window -d -t =neptuno-acme: -n docs -c ROOT overmind start -f ROOT/.neptuno/procfiles/docs/Procfile -d ROOT -s .neptuno/run/docs.sock -w acme-docs; " +
			`echo; echo "overmind exited with status $?. Press enter to close this window."; read _`,
		"attach: tmux attach-session -t =neptuno-acme",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	procfile := read(t, filepath.Join(root, ".neptuno/procfiles/api/Procfile"))
	if !strings.Contains(procfile, "web: exec docker compose exec api sh -c 'bin/rails s -b 0.0.0.0'\n") {
		t.Errorf("api Procfile not wrapped:\n%s", procfile)
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
	f.sessions, f.windows["api"] = true, true

	if err := o.Up(UpOptions{Selected: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker compose up --detach postgres api",
		"tmux switch-client -t =neptuno-acme",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestUpRestartsOvermindWhenContainerIsRecreated(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)
	f.sessions, f.windows["api"] = true, true
	f.containers["api"] = []string{"old", "new"}

	if err := o.Up(UpOptions{Selected: []string{"api"}, Detach: true}); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.cmds {
		got = append(got, strings.Fields(c)[0]+" "+strings.Fields(c)[1])
	}
	want := []string{"docker compose", "tmux kill-window", "tmux new-window"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n%s\nwant prefixes %v", strings.Join(f.cmds, "\n"), want)
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
