package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/robertoplancarte/neptuno/internal/orchestrate"
)

// errStop ends neptuno exec before it runs anything.
var errStop = errors.New("stop")

// execProject makes a project with a container repo web, a run: host repo
// docs and a postgres service, and returns its root.
func execProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"repos/web/app/models", "repos/docs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"compose.yml": "services:\n  postgres: {image: postgres:18}\n  web:\n    build: dockerfiles/web\n    volumes: [\"./repos/web:/app\"]\n",
		"neptuno.yml": "repos:\n  docs:\n    run: host\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runExec runs `neptuno exec args...` in dir and returns the options it
// would run with, and the host command for a run: host repo.
func runExec(t *testing.T, dir string, args ...string) (orchestrate.ExecOptions, orchestrate.Command, error) {
	t.Helper()
	var opts orchestrate.ExecOptions
	var command orchestrate.Command
	execCommand = func(o *orchestrate.Orchestrator, got orchestrate.ExecOptions) (orchestrate.Command, error) {
		opts = got
		if o.Project.RunMode(got.Service) == "host" {
			c, err := o.ExecCommand(got)
			if err != nil {
				return c, err
			}
			command = c
		}
		return orchestrate.Command{}, errStop
	}
	t.Cleanup(func() { execCommand = (*orchestrate.Orchestrator).ExecCommand })
	t.Chdir(dir)
	cmd := newExecCommand()
	cmd.SetArgs(args)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	err := cmd.Execute()
	if errors.Is(err, errStop) {
		err = nil
	}
	return opts, command, err
}

func TestExecSplitsTheRepoFromTheCommand(t *testing.T) {
	root := execProject(t)
	for _, tc := range []struct {
		dir     string
		args    []string
		service string
		command []string
	}{
		{"", []string{"web", "--", "bin/rails", "test", "-v"}, "web", []string{"bin/rails", "test", "-v"}},
		{"", []string{"web", "bin/rails", "test", "-v"}, "web", []string{"bin/rails", "test", "-v"}},
		{"", []string{"web", "echo a && echo b"}, "web", []string{"echo a && echo b"}},
		{"", []string{"web", "--", "echo a && echo b"}, "web", []string{"echo a && echo b"}},
		{"", []string{"postgres", "--", "psql", "-U", "postgres"}, "postgres", []string{"psql", "-U", "postgres"}},
		// Only the separator after the repo goes; the command keeps its own.
		{"", []string{"web", "--", "grep", "--", "foo"}, "web", []string{"grep", "--", "foo"}},
		{"", []string{"web", "grep", "--", "foo"}, "web", []string{"grep", "--", "foo"}},
		// Inside a repo's folder the repo can be left out.
		{"repos/web/app/models", []string{"--", "pwd"}, "web", []string{"pwd"}},
		{"repos/web/app/models", []string{"--", "grep", "--", "x"}, "web", []string{"grep", "--", "x"}},
		{"repos/web", []string{"web", "--", "pwd"}, "web", []string{"pwd"}},
	} {
		opts, _, err := runExec(t, filepath.Join(root, tc.dir), tc.args...)
		if err != nil {
			t.Errorf("exec %q in %s: %v", tc.args, tc.dir, err)
			continue
		}
		if opts.Service != tc.service || !reflect.DeepEqual(opts.Args, tc.command) {
			t.Errorf("exec %q in %s: service %q, command %q; want %q, %q", tc.args, tc.dir, opts.Service, opts.Args, tc.service, tc.command)
		}
	}
}

func TestExecOnAHostRepo(t *testing.T) {
	root := execProject(t)
	for _, args := range [][]string{
		{"docs", "--", "npm", "run", "build", "--", "--watch"},
		{"docs", "npm", "run", "build", "--", "--watch"},
	} {
		opts, c, err := runExec(t, root, args...)
		if err != nil {
			t.Fatal(err)
		}
		if opts.Service != "docs" || len(c.Args) < 4 {
			t.Fatalf("exec %q: %+v, %q", args, opts, c.Args)
		}
		if got := c.Args[1:]; !reflect.DeepEqual(got, []string{"host-exec", "docs", "npm run build -- --watch"}) {
			t.Errorf("exec %q runs %q", args, got)
		}
	}
}

func TestExecWithAnUnknownRepo(t *testing.T) {
	root := execProject(t)
	_, _, err := runExec(t, filepath.Join(root, "repos/web"), "pwd")
	if err == nil {
		t.Fatal("ran pwd as a repo")
	}
	msg := err.Error()
	if !strings.Contains(msg, `no repo or service named "pwd"`) || !strings.Contains(msg, "neptuno exec -- pwd") || strings.Contains(msg, "exec pwd --") {
		t.Errorf("error = %s", msg)
	}

	// Outside a repo's folder there is nothing to suggest running it in.
	_, _, err = runExec(t, root, "nope", "--", "ls")
	if err == nil || strings.Contains(err.Error(), "put -- first") || !strings.Contains(err.Error(), "neptuno status") {
		t.Errorf("error = %v", err)
	}

	if _, _, err := runExec(t, root, "--", "ls"); err == nil || !strings.Contains(err.Error(), "name a repo before --") {
		t.Errorf("-- outside a repo: %v", err)
	}
}
