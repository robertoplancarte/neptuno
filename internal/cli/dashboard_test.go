package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
)

func TestLiveOrchestratorReloadsTheProject(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("neptuno.yml", "")
	write("compose.yml", "services:\n  postgres: {image: postgres:17}\n  api: {build: dockerfiles/api}\n")
	write("repos/api/README", "")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	live := newLiveOrchestrator(&orchestrate.Orchestrator{Project: p})
	if got := live.get().Project.Repos; !reflect.DeepEqual(got, []string{"api"}) {
		t.Fatalf("repos = %v", got)
	}

	// What `neptuno add shop` writes.
	write("repos/shop/README", "")
	write("compose.yml", "services:\n  postgres: {image: postgres:17}\n  api: {build: dockerfiles/api}\n  shop: {build: dockerfiles/shop}\n")
	got := live.get().Project
	if !reflect.DeepEqual(got.Repos, []string{"api", "shop"}) || !got.IsRepo("shop") {
		t.Errorf("after the add: repos %v", got.Repos)
	}
	if _, ok := got.Services["shop"]; !ok {
		t.Errorf("after the add: services %v", got.Services)
	}

	// A half-saved compose.yml keeps the last good project.
	write("compose.yml", "services: [\n")
	if got := live.get().Project; !got.IsRepo("shop") {
		t.Errorf("a broken compose.yml lost the project: %v", got.Repos)
	}
}
