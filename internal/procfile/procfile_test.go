package procfile

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
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
	got := WrapContainer([]Entry{{"web", "echo 'hi' && rails s"}}, "api")
	want := `exec docker compose exec api sh -c 'echo '\''hi'\'' && rails s'`
	if got[0].Command != want {
		t.Errorf("WrapContainer = %s\nwant %s", got[0].Command, want)
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
