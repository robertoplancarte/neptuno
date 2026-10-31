package orchestrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// statusRunner answers the read-only commands Status runs.
type statusRunner struct {
	fakeRunner
	outputs map[string]string
}

func (r *statusRunner) Output(dir, name string, args ...string) (string, error) {
	key := name + " " + strings.Join(args, " ")
	if name == "git" {
		key = filepath.Base(dir) + ": " + key
	}
	out, ok := r.outputs[key]
	if !ok {
		return "", errors.New("exit status 1")
	}
	return out, nil
}

func TestStatus(t *testing.T) {
	o, f := setup(t)
	f.sessions = true
	if err := os.WriteFile(filepath.Join(o.Project.Root, "repos/api/.git"), []byte("gitdir: ../../.git/modules/api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &statusRunner{fakeRunner: *f, outputs: map[string]string{
		"docker compose ps --all --format json": `{"Service":"api","State":"running","Health":"","Publishers":[]}
{"Service":"postgres","State":"running","Health":"healthy","Publishers":[{"PublishedPort":5432,"TargetPort":5432},{"PublishedPort":5432,"TargetPort":5432}]}
{"Service":"web","State":"exited","Health":"","Publishers":null}`,
		"tmux list-windows -t =neptuno-acme -F #{window_name}": "neptuno\napi\nclaude-api",
		"overmind status -s .neptuno/run/api.sock":             "PROCESS   PID       STATUS\nweb       101       running\nworker    102       dead\n",
		"api: git log -1 --format=%h%x1f%cI%x1f%an%x1f%s":      "abc1234\x1f2026-10-30T14:02:00Z\x1fRoberto\x1fAdd login",
		"api: git rev-parse --abbrev-ref HEAD":                 "main",
		"api: git status --porcelain --untracked-files=no":     " M app.rb",
	}}
	o.Runner = r

	got := map[string]ServiceStatus{}
	var order []string
	for _, s := range o.Status() {
		got[s.Name] = s
		order = append(order, s.Name)
	}
	if want := []string{"api", "docs", "web", "postgres", "valkey"}; !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}

	api := got["api"]
	if !api.Repo || !api.Overmind || !api.Window || !api.Claude || api.Env != "local" || api.State() != "partial" {
		t.Errorf("api = %+v (state %s)", api, api.State())
	}
	if want := []ProcessStatus{{"web", "running"}, {"worker", "dead"}}; !reflect.DeepEqual(api.Processes, want) {
		t.Errorf("api processes = %v", api.Processes)
	}
	if g := api.Git; g == nil || g.Commit != "abc1234" || g.Branch != "main" || g.Subject != "Add login" || !g.Dirty || g.Date.Day() != 30 {
		t.Errorf("api git = %+v", api.Git)
	}
	// docs has no .git of its own, so it must not report the project's.
	if docs := got["docs"]; docs.Git != nil || docs.RunMode != "host" || docs.State() != "stopped" {
		t.Errorf("docs = %+v", docs)
	}
	if web := got["web"]; web.State() != "exited" || web.Running() {
		t.Errorf("web state = %s", web.State())
	}
	if pg := got["postgres"]; pg.Repo || pg.State() != "running" || !reflect.DeepEqual(pg.Ports, []string{"5432->5432"}) {
		t.Errorf("postgres = %+v", pg)
	}
	if vk := got["valkey"]; vk.State() != "stopped" {
		t.Errorf("valkey state = %s", vk.State())
	}
}

func TestParseComposePSArray(t *testing.T) {
	m := parseComposePS(`[{"Service":"api","State":"exited"},{"Service":"api","State":"running"},{"Service":"api","State":"exited"}]`)
	if m["api"].State != "running" {
		t.Errorf("api = %+v, want the running replica", m["api"])
	}
}
