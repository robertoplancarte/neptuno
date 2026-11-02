package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

func testVolumes() ([]orchestrate.Volume, error) {
	return []orchestrate.Volume{
		{Name: "api_bundle", Docker: "acme_api_bundle", Services: []string{"api"}, Exists: true},
		{Name: "postgres_data", Docker: "acme_postgres_data", Services: []string{"postgres"}, Exists: true},
		{Name: "shared", Docker: "shared", External: true},
	}, nil
}

func newVolumesDashboard(t *testing.T) *dashboard {
	m := newTestDashboard()
	m.opts.Volumes = testVolumes
	m.opts.Neptuno, m.opts.Root = "echo", t.TempDir()
	m.width, m.height = 140, 30
	m.Update(m.loadVolumes()())
	return m
}

func TestDashboardShowsVolumes(t *testing.T) {
	m := newVolumesDashboard(t)
	if view := m.View(); !strings.Contains(view, "3 volumes (v to manage)") || !strings.Contains(view, "api_bundle  (v to reset)") {
		t.Errorf("view does not mention the volumes:\n%s", view)
	}

	key(m, "down", "down", "v")
	if m.vols == nil || m.vols.cursor != 1 {
		t.Fatalf("v on postgres did not open the view on postgres_data: %+v", m.vols)
	}
	view := m.View()
	for _, want := range []string{"api_bundle", "postgres_data", "shared", "external", "1. stop postgres and remove its container", "2. delete acme_postgres_data", "3. start postgres again"} {
		if !strings.Contains(view, want) {
			t.Errorf("volumes view missing %q:\n%s", want, view)
		}
	}

	key(m, "down", "enter")
	if m.vols.confirm || !strings.Contains(m.message, "external") {
		t.Errorf("enter on an external volume: confirm %v, message %q", m.vols.confirm, m.message)
	}
	key(m, "esc")
	if m.vols != nil {
		t.Error("esc left the view open")
	}
}

func TestDashboardResetsVolumeAfterConfirming(t *testing.T) {
	m := newVolumesDashboard(t)
	key(m, "down", "down", "v", "enter")
	if !m.vols.confirm {
		t.Fatal("enter did not ask to confirm")
	}
	if view := m.View(); !strings.Contains(view, "Delete acme_postgres_data? Its data is gone for good.") || !strings.Contains(view, "Press y to delete it") {
		t.Errorf("confirmation:\n%s", view)
	}
	if cmd := key(m, "n"); cmd != nil || m.vols.confirm || m.actions["postgres"] != nil {
		t.Fatalf("n went ahead: %v", cmd)
	}

	cmd := key(m, "enter", "y")
	a := m.actions["postgres"]
	if cmd == nil || a == nil || m.actions[volumeAction("postgres_data")] != a {
		t.Fatalf("y did not start the reset: %v %+v", cmd, a)
	}
	drain(t, m, cmd)
	if a.err != nil || !reflect.DeepEqual(a.lines, []string{"volumes reset --yes postgres_data"}) {
		t.Errorf("reset ran %+v", a)
	}
	if view := m.View(); !strings.Contains(view, "reset volume postgres_data") || !strings.Contains(view, "done") {
		t.Errorf("view does not show how the reset went:\n%s", view)
	}
}
