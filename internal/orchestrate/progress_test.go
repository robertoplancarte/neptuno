package orchestrate

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// trackerAt is a tracker whose clock moves only when the test says.
func trackerAt(t *testing.T, services []string, images map[string]string) (*progressTracker, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	tr := newProgressTracker(t.TempDir(), "demo", services, images, func() time.Time { return now })
	return tr, &now
}

func feed(tr *progressTracker, lines ...string) {
	for _, l := range lines {
		tr.Write([]byte(l + "\n"))
	}
}

func phases(tr *progressTracker) map[string]string {
	out := map[string]string{}
	for name, p := range tr.file.Services {
		out[name] = p.Phase
	}
	return out
}

// The lines below are docker compose 5's plain output, trimmed.
func TestProgressTrackerPullAndBuild(t *testing.T) {
	tr, now := trackerAt(t, []string{"cache", "web", "api"}, map[string]string{"cache": "valkey/valkey:8-alpine", "api": "myapi:dev"})
	if got := phases(tr); got["cache"] != PhaseWaiting || got["web"] != PhaseWaiting {
		t.Fatalf("at start = %v", got)
	}
	feed(tr,
		" Image valkey/valkey:8-alpine Pulling ",
		" ff91ba65c239 Pulling fs layer 0B",
		" d8641609d97e Pulling fs layer 0B",
		" ba51a09e5647 Pulling fs layer 0B",
		" ff91ba65c239 Download complete 0B",
		" ba51a09e5647 Downloading 4.194MB",
		" ff91ba65c239 Pull complete 0B",
	)
	cache := tr.file.Services["cache"]
	if cache.Phase != PhasePulling || cache.Step != 1 || cache.Steps != 3 || cache.Output != "ff91ba65c239 Pull complete 0B" {
		t.Errorf("pulling cache = %+v", cache)
	}
	feed(tr, " Image valkey/valkey:8-alpine Pulled ", " Image demo-web Building ", " Image myapi:dev Building ")
	if got := phases(tr); got["cache"] != PhaseWaiting || got["web"] != PhaseBuilding || got["api"] != PhaseBuilding {
		t.Fatalf("after pulls = %v", got)
	}
	*now = now.Add(5 * time.Second)
	feed(tr,
		"#1 [internal] load local bake definitions",
		"#1 DONE 0.0s",
		"",
		"#2 [api internal] load build definition from Dockerfile",
		"#6 [web 2/3] RUN make -j8",
		"#6 0.284 cc -c foo.c",
		"#7 [api 3/4] RUN bundle install",
		"#6 1.286 cc -c bar.c",
		"#6 DONE 6.4s",
	)
	web, api := tr.file.Services["web"], tr.file.Services["api"]
	if web.Step != 2 || web.Steps != 3 || web.Detail != "RUN make -j8" || web.Output != "cc -c bar.c" || !web.Updated.Equal(*now) {
		t.Errorf("web = %+v", web)
	}
	if api.Step != 3 || api.Steps != 4 || api.Detail != "RUN bundle install" || api.Output != "" {
		t.Errorf("api = %+v", api)
	}
	feed(tr,
		" Image demo-web Built ",
		" Image myapi:dev Built ",
		" Network demo_default Creating ",
		" Container demo-web-1 Creating ",
		" Container demo-api-1 Recreate ",
		" Container demo-cache-1 Starting ",
	)
	if got := phases(tr); got["web"] != PhaseCreating || got["api"] != PhaseCreating || got["cache"] != PhaseStarting {
		t.Fatalf("creating = %v", got)
	}
	feed(tr, " Container demo-cache-1 Started ", " Container demo-web-1 Running ")
	if got := phases(tr); len(got) != 1 || got["api"] != PhaseCreating {
		t.Errorf("after start = %v", got)
	}
}

// Older compose versions name services rather than images, and a single
// build's steps carry no service.
func TestProgressTrackerServiceNames(t *testing.T) {
	tr, _ := trackerAt(t, []string{"postgres", "web"}, nil)
	feed(tr, " postgres Pulling", " web Building", "#5 [2/3] RUN npm ci", "#5 12.01 added 812 packages")
	pg, web := tr.file.Services["postgres"], tr.file.Services["web"]
	if pg.Phase != PhasePulling || web.Phase != PhaseBuilding || web.Step != 2 || web.Output != "added 812 packages" {
		t.Errorf("postgres = %+v, web = %+v", pg, web)
	}
}

func TestProgressTrackerWritesFile(t *testing.T) {
	root := t.TempDir()
	tr := newProgressTracker(root, "demo", []string{"web"}, nil, time.Now)
	feed(tr, " Image demo-web Building ")
	path := filepath.Join(root, progressDir(), strconv.Itoa(os.Getpid())+".json")
	got := readProgress(root)
	if p := got["web"]; p == nil || p.Phase != PhaseBuilding {
		t.Fatalf("readProgress = %v", got)
	}
	tr.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("progress file left after Close: %v", err)
	}

	// A run that died without cleaning up is ignored and removed.
	stale := filepath.Join(root, progressDir(), "999999999.json")
	b, _ := json.Marshal(progressFile{PID: 999999999, Services: map[string]*Progress{"web": {Phase: PhaseBuilding}}})
	if err := os.WriteFile(stale, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readProgress(root); len(got) != 0 {
		t.Errorf("stale progress = %v", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file not removed")
	}
}

func TestStateShowsProgress(t *testing.T) {
	building := &Progress{Phase: PhaseBuilding}
	waiting := &Progress{Phase: PhaseWaiting}
	for _, c := range []struct {
		s    ServiceStatus
		want string
	}{
		{ServiceStatus{Progress: building}, "building"},
		{ServiceStatus{Container: "running", Progress: building}, "building"},
		{ServiceStatus{Progress: waiting}, "waiting"},
		{ServiceStatus{Container: "running", Progress: waiting}, "running"},
		{ServiceStatus{Container: "exited", Progress: waiting}, "waiting"},
	} {
		if got := c.s.State(); got != c.want {
			t.Errorf("State(%s, %s) = %s, want %s", c.s.Container, c.s.Progress.Phase, got, c.want)
		}
	}
}

// progressRunner plays compose output into Stream's writer, and records
// what the dashboard would read meanwhile.
type progressRunner struct {
	*fakeRunner
	root   string
	during map[string]*Progress
}

func (r *progressRunner) Stream(w io.Writer, dir, name string, args ...string) error {
	r.fakeRunner.Run(dir, name, args...)
	io.WriteString(w, " postgres Pulling\n Image acme-api Building\n#3 [api 1/5] FROM ruby:3.4\n")
	r.during = readProgress(r.root)
	return nil
}

func TestComposeUpTracksProgress(t *testing.T) {
	o, f := setup(t)
	r := &progressRunner{fakeRunner: f, root: o.Project.Root}
	o.Runner = r
	if err := o.composeUp([]string{"postgres", "api"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.cmds, "\n"), "docker compose up --detach postgres api") {
		t.Errorf("cmds = %v", f.cmds)
	}
	if r.during["api"] == nil || r.during["api"].Phase != PhaseBuilding || r.during["postgres"].Phase != PhasePulling {
		t.Errorf("progress during up = %v", r.during)
	}
	if got := readProgress(o.Project.Root); len(got) != 0 {
		t.Errorf("progress after up = %v", got)
	}
}
