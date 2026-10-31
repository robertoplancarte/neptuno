package procfile

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	got, err := Parse(strings.NewReader("# comment\nweb: bin/rails server -p 3000\n\nworker:   bundle exec sidekiq\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{"web", "bin/rails server -p 3000"}, {"worker", "bundle exec sidekiq"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse = %v, want %v", got, want)
	}
	if _, err := Parse(strings.NewReader("no colon here\n")); err == nil {
		t.Error("Parse accepted a line without a name")
	}
}

func TestWrapContainer(t *testing.T) {
	got := WrapContainer([]Entry{{"web", "echo 'hi' && rails s"}}, "api", false)
	want := `exec docker compose exec api sh -c 'echo '\''hi'\'' && rails s'`
	if got[0].Command != want {
		t.Errorf("WrapContainer = %s\nwant %s", got[0].Command, want)
	}
}

// TestWrapContainerWaitsForEntrypoint runs the wrapped command's script with
// sh, with a scratch folder standing in for the container's /dev/shm.
func TestWrapContainerWaitsForEntrypoint(t *testing.T) {
	dir := t.TempDir()
	cmd := WrapContainer([]Entry{{"web", "echo started"}}, "api", true)[0].Command
	inner, err := exec.Command("sh", "-c", "printf %s "+strings.TrimPrefix(cmd, "exec docker compose exec api sh -c ")).Output()
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(inner), "/dev/shm/", dir+"/")

	go func() {
		time.Sleep(1500 * time.Millisecond)
		os.WriteFile(dir+"/neptuno-ready", nil, 0o644)
	}()
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "Waiting for the api entrypoint.sh") || !strings.HasSuffix(string(out), "started\n") {
		t.Errorf("waiting for setup: %q, %v", out, err)
	}

	if out, err := exec.Command("sh", "-c", script).CombinedOutput(); err != nil || string(out) != "started\n" {
		t.Errorf("setup done: %q, %v", out, err)
	}

	os.Remove(dir + "/neptuno-ready")
	if err := os.WriteFile(dir+"/neptuno-failed", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command("sh", "-c", script).CombinedOutput()
	if err == nil || strings.Contains(string(out), "started") || !strings.Contains(string(out), "api entrypoint.sh failed") {
		t.Errorf("setup failed: %q, %v", out, err)
	}

	// The window shows why setup failed.
	if err := os.WriteFile(dir+"/neptuno-setup.log", []byte("PG::DuplicateTable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = exec.Command("sh", "-c", script).CombinedOutput()
	if !strings.Contains(string(out), "PG::DuplicateTable") || !strings.Contains(string(out), "docker compose restart api && neptuno restart api") {
		t.Errorf("setup log: %q", out)
	}
}

func TestWrapHost(t *testing.T) {
	got := WrapHost([]Entry{{"web", "npm run dev"}}, "/usr/local/bin/neptuno", "web")
	want := `exec /usr/local/bin/neptuno host-exec web 'npm run dev'`
	if got[0].Command != want {
		t.Errorf("WrapHost = %s\nwant %s", got[0].Command, want)
	}
}

func TestQuoteRoundTripsThroughShell(t *testing.T) {
	for _, s := range []string{"plain", "two words", `it's`, `$HOME "x" \n`, ""} {
		out, err := exec.Command("sh", "-c", "printf %s "+Quote(s)).Output()
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != s {
			t.Errorf("Quote(%q) through sh = %q", s, out)
		}
	}
}
