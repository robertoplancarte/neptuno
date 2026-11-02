package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

func testVolumes() ([]orchestrate.Volume, error) {
	return []orchestrate.Volume{
		{Name: "api_bundle", Docker: "acme_api_bundle", Services: []string{"api"}, Exists: true,
			Mounts: []orchestrate.Mount{{Service: "api", Target: "/usr/local/bundle"}}},
		{Name: "postgres_data", Docker: "acme_postgres_data", Services: []string{"postgres"}, Exists: true,
			Mounts: []orchestrate.Mount{{Service: "postgres", Target: "/var/lib/postgresql/data"}}},
		{Name: "shared", Docker: "shared", External: true},
	}, nil
}

func testInspect(v orchestrate.Volume) (orchestrate.VolumeDetail, error) {
	return orchestrate.VolumeDetail{Driver: "local", Size: "48M", Entries: []string{"base/", "PG_VERSION"},
		Created: time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local), Via: "inside the running postgres container"}, nil
}

func newVolumesDashboard(t *testing.T) *dashboard {
	m := newTestDashboard()
	m.opts.Volumes, m.opts.InspectVolume = testVolumes, testInspect
	m.opts.Neptuno, m.opts.Root = "echo", t.TempDir()
	m.width, m.height = 140, 34
	m.Update(m.loadVolumes()())
	return m
}

// moveTo puts the cursor on a volume and loads its panel, as bubbletea
// would.
func moveTo(t *testing.T, m *dashboard, name string) {
	t.Helper()
	for i, v := range m.volumes {
		if v.Name == name {
			m.cursor = len(m.rows) + i
			if cmd := m.fetchPanel(); cmd != nil {
				m.Update(cmd())
			}
			return
		}
	}
	t.Fatalf("no volume %s", name)
}

func TestDashboardListsVolumesOnTheLeft(t *testing.T) {
	m := newVolumesDashboard(t)
	view := m.View()
	for _, want := range []string{"Volumes 3", "api_bundle", "● created", "postgres_data", "● external", "api_bundle  (in the Volumes list)"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}

	// tab cycles repos, services, volumes; down walks into the volumes.
	key(m, "tab", "tab")
	if v, ok := m.currentVolume(); !ok || v.Name != "api_bundle" {
		t.Fatalf("tab tab did not reach the volumes: cursor %d", m.cursor)
	}
	key(m, "tab")
	if r, ok := m.current(); !ok || r.Name != "api" {
		t.Errorf("tab from the volumes did not go back to the repos: cursor %d", m.cursor)
	}
	key(m, "down", "down", "down")
	if v, ok := m.currentVolume(); !ok || v.Name != "api_bundle" {
		t.Errorf("down from postgres did not reach the first volume: cursor %d", m.cursor)
	}
	if cmd := key(m, "u"); cmd != nil || !strings.Contains(m.message, "enter clears a volume") {
		t.Errorf("u on a volume ran %v, message %q", cmd, m.message)
	}
}

func TestDashboardVolumePanel(t *testing.T) {
	m := newVolumesDashboard(t)
	moveTo(t, m, "postgres_data")
	view := m.View()
	for _, want := range []string{
		"Volume postgres_data", "PostgreSQL's data directory", "acme_postgres_data",
		"postgres ● running at /var/lib/postgresql/data", "48M", "base/", "PG_VERSION",
		"enter clears it", "1. stop postgres and remove its container", "2. delete acme_postgres_data", "3. start postgres again",
		"enter clear volume",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("volume panel missing %q:\n%s", want, view)
		}
	}

	moveTo(t, m, "shared")
	if key(m, "enter"); m.confirm != "" || !strings.Contains(m.message, "external") {
		t.Errorf("enter on an external volume: confirm %q, message %q", m.confirm, m.message)
	}
}

func TestDashboardClearsVolumeAfterConfirming(t *testing.T) {
	m := newVolumesDashboard(t)
	moveTo(t, m, "postgres_data")
	key(m, "enter")
	if m.confirm != "postgres_data" {
		t.Fatal("enter did not ask to confirm")
	}
	if view := m.View(); !strings.Contains(view, "Delete acme_postgres_data? Its data is gone for good.") || !strings.Contains(view, "Press y to delete it") {
		t.Errorf("confirmation:\n%s", view)
	}
	if cmd := key(m, "n"); cmd != nil || m.confirm != "" || m.actions["postgres"] != nil || m.message != "nothing deleted" {
		t.Fatalf("n went ahead: %v, %q", cmd, m.message)
	}

	cmd := key(m, "enter", "y")
	a := m.actions["postgres"]
	if cmd == nil || a == nil || m.actions[volumeAction("postgres_data")] != a {
		t.Fatalf("y did not start the clear: %v %+v", cmd, a)
	}
	drain(t, m, cmd)
	if a.err != nil || !reflect.DeepEqual(a.lines, []string{"volumes reset --yes postgres_data"}) {
		t.Errorf("clear ran %+v", a)
	}
	if view := m.View(); !strings.Contains(view, "clear volume postgres_data") || !strings.Contains(view, "done") {
		t.Errorf("panel does not show how the clear went:\n%s", view)
	}
}

func TestDescribeVolume(t *testing.T) {
	for target, want := range map[string]string{
		"/usr/local/bundle":   "Ruby gems",
		"/app/node_modules":   "npm packages",
		"/var/lib/mysql":      "MySQL",
		"/srv/something/else": "Data web keeps at /srv/something/else",
	} {
		v := orchestrate.Volume{Mounts: []orchestrate.Mount{{Service: "web", Target: target}}}
		if got := describeVolume(v); !strings.Contains(got, want) {
			t.Errorf("describeVolume(%s) = %q, want it to mention %q", target, got, want)
		}
	}
}

func TestArrowsMoveBetweenListsAndBracketsBetweenTabs(t *testing.T) {
	m := newVolumesDashboard(t)
	key(m, "right")
	if r, ok := m.current(); !ok || r.Name != "postgres" || m.tab != tabInfo {
		t.Fatalf("right went to cursor %d, tab %d; want postgres on Info", m.cursor, m.tab)
	}
	key(m, "right")
	if v, ok := m.currentVolume(); !ok || v.Name != "api_bundle" {
		t.Fatalf("right from the services did not reach the volumes: cursor %d", m.cursor)
	}
	key(m, "right")
	if r, ok := m.current(); !ok || r.Name != "api" {
		t.Errorf("right from the volumes did not wrap to the repos: cursor %d", m.cursor)
	}
	key(m, "left")
	if _, ok := m.currentVolume(); !ok {
		t.Errorf("left from the repos did not wrap to the volumes: cursor %d", m.cursor)
	}
	key(m, "left", "left")
	if r, ok := m.current(); !ok || r.Name != "api" || m.tab != tabInfo {
		t.Errorf("left left went to cursor %d, tab %d", m.cursor, m.tab)
	}

	key(m, "]")
	if m.tab != tabEnv || m.cursor != 0 {
		t.Errorf("] went to tab %d, cursor %d; want Env on api", m.tab, m.cursor)
	}
	key(m, "[", "[")
	if m.tab != tabActions {
		t.Errorf("[ [ went to tab %d, want Actions", m.tab)
	}
	key(m, "[")
	if m.tab != tabLogs {
		t.Errorf("[ [ [ went to tab %d, want Logs", m.tab)
	}
}
