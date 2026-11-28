package orchestrate

import (
	"testing"

	"github.com/robertoplancarte/neptuno/internal/engine"
)

func TestExecRunnerUsesEngine(t *testing.T) {
	// echo stands in for podman, so the output shows what would have run.
	r := ExecRunner{Engine: &engine.Engine{Name: "echo", Compose: []string{"echo", "compose-via"}}}
	for want, args := range map[string][]string{
		"compose-via ps --quiet": {"compose", "ps", "--quiet"},
		"volume ls":              {"volume", "ls"},
	} {
		got, err := r.Output(t.TempDir(), "docker", args...)
		if err != nil || got != want {
			t.Errorf("Output(docker %v) = %q, %v; want %q", args, got, err, want)
		}
	}
	if got, _ := r.Output(t.TempDir(), "echo", "plain"); got != "plain" {
		t.Errorf("Output(echo plain) = %q", got)
	}
	if err := r.LookPath("docker"); err != nil {
		t.Errorf("LookPath(docker) = %v, want echo found", err)
	}
	missing := ExecRunner{Engine: &engine.Engine{Name: "podman", Compose: []string{"no-such-compose-neptuno"}}}
	if missing.LookPath("docker") == nil {
		t.Error("LookPath(docker) found a compose command that isn't installed")
	}
}
