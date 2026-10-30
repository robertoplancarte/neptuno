package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/spf13/cobra"
)

func TestChooseServicesFromFolder(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"repos/api", "dockerfiles/api/nested", "repos/web"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	compose := "services:\n  postgres: {image: postgres:17}\n  api: {build: dockerfiles/api}\n  web: {build: dockerfiles/web}\n"
	if err := os.WriteFile(filepath.Join(root, "compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})

	t.Chdir(filepath.Join(root, "dockerfiles/api/nested"))
	got, err := chooseServices(cmd, p, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"api"}) {
		t.Errorf("inside dockerfiles/api: %v, want [api]", got)
	}

	// Explicit names and --all win over the folder.
	if got, _ := chooseServices(cmd, p, []string{"web"}, false); !reflect.DeepEqual(got, []string{"web"}) {
		t.Errorf("with args: %v, want [web]", got)
	}
	if got, _ := chooseServices(cmd, p, nil, true); len(got) != 3 {
		t.Errorf("with --all: %v, want all three", got)
	}
}
