package orchestrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExecCommandInARunningContainer(t *testing.T) {
	o, f := setup(t)
	f.containers["api"] = []string{"abc123"}

	c, err := o.ExecCommand(ExecOptions{Service: "api", Args: []string{"bin/rails", "test", "test/models"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "compose", "exec", "-T", "api", "bin/rails", "test", "test/models"}
	if !reflect.DeepEqual(c.Args, want) || c.Dir != o.Project.Root {
		t.Errorf("command = %q in %s, want %q", c.Args, c.Dir, want)
	}
	if len(f.cmds) != 0 {
		t.Errorf("ran %q for a running container", f.cmds)
	}
}

func TestExecCommandRunsOneArgumentInAShellWithATerminal(t *testing.T) {
	o, f := setup(t)
	f.containers["postgres"] = []string{"abc123"}

	c, err := o.ExecCommand(ExecOptions{Service: "postgres", Args: []string{"psql -U postgres -c 'select 1'"}, TTY: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker", "compose", "exec", "postgres", "sh", "-c", "psql -U postgres -c 'select 1'"}
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("command = %q, want %q", c.Args, want)
	}
}

func TestExecCommandMapsTheFolderIntoTheContainer(t *testing.T) {
	o, f := setup(t)
	f.containers["api"] = []string{"abc123"}
	if err := os.WriteFile(o.Project.Path("compose.override.yml"),
		[]byte("services:\n  api:\n    volumes: [\"./repos/api:/app\", \"api_bundle:/usr/local/bundle\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(o.Project.Root, "repos", "api", "engines", "billing")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := o.ExecCommand(ExecOptions{Service: "api", Args: []string{"bin/rails", "test"}, Dir: sub})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Args, " "); got != "docker compose exec -T --workdir /app/engines/billing api bin/rails test" {
		t.Errorf("command = %s", got)
	}

	// The project root is outside the repo, so the container's own
	// working directory applies.
	c, _ = o.ExecCommand(ExecOptions{Service: "api", Args: []string{"ls"}, Dir: o.Project.Root})
	if strings.Contains(strings.Join(c.Args, " "), "--workdir") {
		t.Errorf("command from the project root = %q", c.Args)
	}
}

func TestExecCommandStartsAStoppedRepo(t *testing.T) {
	t.Setenv("TMUX", "")
	o, f := setup(t)

	c, err := o.ExecCommand(ExecOptions{Service: "api", Args: []string{"bin/rails", "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) == 0 || !strings.HasPrefix(f.cmds[0], "docker compose up --detach postgres api") {
		t.Errorf("did not start api first:\n%s", strings.Join(f.cmds, "\n"))
	}
	for _, cmd := range f.cmds {
		if strings.HasPrefix(cmd, "attach:") {
			t.Errorf("attached to tmux: %s", cmd)
		}
	}
	if c.Args[len(c.Args)-2] != "bin/rails" {
		t.Errorf("command = %q", c.Args)
	}
}

func TestExecCommandOnAHostRepo(t *testing.T) {
	o, _ := setup(t)

	c, err := o.ExecCommand(ExecOptions{Service: "docs", Args: []string{"npm", "run", "build: all"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/bin/neptuno", "host-exec", "docs", "npm run 'build: all'"}
	if !reflect.DeepEqual(c.Args, want) {
		t.Errorf("command = %q, want %q", c.Args, want)
	}
}

func TestExecCommandRejectsUnknownServicesAndNoCommand(t *testing.T) {
	o, _ := setup(t)
	if _, err := o.ExecCommand(ExecOptions{Service: "nope", Args: []string{"ls"}}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown service: %v", err)
	}
	if _, err := o.ExecCommand(ExecOptions{Service: "api"}); err == nil {
		t.Error("ran without a command")
	}
}
