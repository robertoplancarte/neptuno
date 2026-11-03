package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/scaffold"
	"github.com/robertoplancarte/neptuno/internal/tui"
)

func TestInitCommand(t *testing.T) {
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "acme", "--no-git"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Created acme/") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
	if _, err := os.Stat("acme/compose.yml"); err != nil {
		t.Errorf("compose.yml not created: %v", err)
	}
	if _, err := os.Stat("acme/.git"); err == nil {
		t.Errorf("--no-git still created a git repository")
	}
}

func TestInitCommandRejectsBadName(t *testing.T) {
	t.Chdir(t.TempDir())

	root := NewRootCommand()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"init", "Acme"})
	if err := root.Execute(); err == nil {
		t.Fatal("init Acme succeeded, want error")
	}
	if _, err := os.Stat("Acme"); err == nil {
		t.Error("directory created for invalid name")
	}
}

func runInit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRootCommand()
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"init"}, args...))
	err := root.Execute()
	return out.String(), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitServicesFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	out, err := runInit(t, "acme", "--no-git", "--services", "svix")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Adding postgres, valkey too") || !strings.Contains(out, "with proxy, postgres, valkey, svix") {
		t.Errorf("output doesn't explain the services:\n%s", out)
	}
	compose := readFile(t, "acme/compose.yml")
	for _, want := range []string{"image: caddy:2", "image: postgres:17", "image: valkey/valkey:8", "image: svix/svix-server:v1"} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yml missing %q", want)
		}
	}
	if strings.Contains(compose, "seaweedfs") || strings.Contains(readFile(t, "acme/Caddyfile"), "s3.localhost") ||
		strings.Contains(readFile(t, "acme/environments/_shared/base.env"), "S3_ENDPOINT") {
		t.Error("s3 was set up without being chosen")
	}
}

func TestInitNoServices(t *testing.T) {
	t.Chdir(t.TempDir())
	if out, err := runInit(t, "acme", "--no-git", "--services="); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	compose := readFile(t, "acme/compose.yml")
	if !strings.Contains(compose, "proxy:") || strings.Contains(compose, "postgres") {
		t.Errorf("compose.yml =\n%s", compose)
	}
}

func TestInitRejectsUnknownService(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := runInit(t, "acme", "--no-git", "--services", "mysql"); err == nil || !strings.Contains(err.Error(), "mysql") {
		t.Fatalf("err = %v, want unknown service mysql", err)
	}
	if _, err := os.Stat("acme"); err == nil {
		t.Error("directory created for an unknown service")
	}
}

func TestInitAddsServicesToExistingProject(t *testing.T) {
	t.Chdir(t.TempDir())
	if out, err := runInit(t, "acme", "--no-git", "--services", "postgres"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	// The user's own edits must survive.
	compose := readFile(t, "acme/compose.yml") + "\n# my note\n"
	if err := os.WriteFile("acme/compose.yml", []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runInit(t, "acme"); err == nil || !strings.Contains(err.Error(), "--services") {
		t.Fatalf("non-interactive init in a project = %v, want a hint about --services", err)
	}

	out, err := runInit(t, "acme", "--services", "postgres,s3,svix")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, want := range []string{"Adding valkey too", "kept    compose.yml service postgres", "added   compose.yml service svix", "added   Caddyfile site http://s3.localhost"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	got := readFile(t, "acme/compose.yml")
	if !strings.HasPrefix(got, strings.TrimRight(compose, "\n")[:strings.Index(compose, "volumes:")]) {
		t.Errorf("existing services changed:\n%s", got)
	}
	for _, want := range []string{"# my note", "image: valkey/valkey:8", "image: chrislusf/seaweedfs", "image: svix/svix-server:v1", "  s3:\n", "  valkey:\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("compose.yml missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "postgres:\n    image") != 1 {
		t.Errorf("postgres duplicated:\n%s", got)
	}
	env := readFile(t, "acme/environments/_shared/base.env")
	if strings.Count(env, "DATABASE_HOST=") != 1 || !strings.Contains(env, "REDIS_URL=") || !strings.Contains(env, "SVIX_AUTH_TOKEN=") {
		t.Errorf("base.env =\n%s", env)
	}

	// From inside the project, with no name, and everything there already.
	t.Chdir("acme")
	out, err = runInit(t, "--services", "svix")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if strings.Contains(out, "added") {
		t.Errorf("second run added something:\n%s", out)
	}
}

func TestInitMenu(t *testing.T) {
	t.Chdir(t.TempDir())
	var offered [][]string
	pickServices = func(opts tui.ChecklistOptions) ([]string, error) {
		offered = append(offered, opts.Names)
		if len(offered) == 1 {
			if len(opts.Preselected) != len(scaffold.DefaultServices) {
				t.Errorf("new project preselects %v, want every default", opts.Preselected)
			}
			return []string{"postgres"}, nil
		}
		if deps, _ := opts.Closure([]string{"svix"}); strings.Join(deps, ",") != "valkey,svix" {
			t.Errorf("closure = %v, want valkey,svix (postgres is there)", deps)
		}
		return []string{"svix"}, nil
	}
	t.Cleanup(func() { pickServices = tui.PickChecklist })

	root, _ := os.Getwd()
	if _, err := createAndAdd(t, root); err != nil {
		t.Fatal(err)
	}
	if strings.Join(offered[1], ",") != "valkey,s3,svix" {
		t.Errorf("second menu offered %v, want the missing services", offered[1])
	}
	if got := readFile(t, "acme/compose.yml"); !strings.Contains(got, "svix:") || strings.Contains(got, "seaweedfs") {
		t.Errorf("compose.yml =\n%s", got)
	}
}

// createAndAdd runs the interactive paths directly, since tests have no
// terminal.
func createAndAdd(t *testing.T, dir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newInitCommand()
	cmd.SetOut(&out)
	if err := createProject(cmd, "acme", nil, true, true); err != nil {
		return out.String(), err
	}
	err := addServices(cmd, dir+"/acme", nil, true)
	return out.String(), err
}
