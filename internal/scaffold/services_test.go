package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveServices(t *testing.T) {
	chosen, required, err := ResolveServices([]string{"hook0", "s3"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range chosen {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "postgres,s3,mailpit,hook0" {
		t.Errorf("chosen = %s, want menu order with hook0's requirements", got)
	}
	if got := strings.Join(required, ","); got != "postgres,mailpit" {
		t.Errorf("required = %s", got)
	}
	if _, _, err := ResolveServices([]string{"mysql"}); !errors.Is(err, ErrUnknownService) {
		t.Errorf("unknown service: err = %v", err)
	}
}

func TestInitWithSomeServices(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme")
	if _, err := Init(Options{Name: "acme", Dir: dir, Services: []string{"valkey"}}); err != nil {
		t.Fatal(err)
	}
	have, err := ExistingServices(dir)
	if err != nil || strings.Join(have, ",") != "valkey" {
		t.Fatalf("services = %v, %v", have, err)
	}
	if agents := read(t, filepath.Join(dir, "AGENTS.md")); !strings.Contains(agents, "`valkey:6379`") || strings.Contains(agents, "postgres:5432") {
		t.Errorf("AGENTS.md lists the wrong services:\n%s", agents)
	}

	log, err := AddServices(dir, []string{"hook0"})
	if err != nil {
		t.Fatal(err)
	}
	if have, _ := ExistingServices(dir); strings.Join(have, ",") != "postgres,valkey,mailpit,hook0" {
		t.Errorf("after add: %v (log %q)", have, log)
	}
	// Running it again changes nothing.
	before, _ := os.ReadFile(filepath.Join(dir, "compose.yml"))
	if _, err := AddServices(dir, []string{"hook0"}); err != nil {
		t.Fatal(err)
	}
	if after := read(t, filepath.Join(dir, "compose.yml")); after != string(before) {
		t.Errorf("second add changed compose.yml")
	}
}

func TestAddHook0WarnsAboutOldPostgres(t *testing.T) {
	dir := t.TempDir()
	compose := "services:\n  postgres:\n    image: postgres:17\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	log, err := AddServices(dir, []string{"hook0"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(log, "\n"); !strings.Contains(got, "hook0 needs PostgreSQL 18 or later, and compose.yml's postgres is postgres:17") {
		t.Errorf("log = %s", got)
	}
}
