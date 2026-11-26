package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func TestPlaywrightIsOptIn(t *testing.T) {
	if slices.Contains(DefaultServiceNames(), "playwright") || !slices.Contains(ServiceNames(), "playwright") || !IsOptIn("playwright") || IsOptIn("postgres") {
		t.Fatalf("defaults %v, all %v", DefaultServiceNames(), ServiceNames())
	}
	dir := filepath.Join(t.TempDir(), "acme")
	if _, err := Init(Options{Name: "acme", Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if have, _ := ExistingServices(dir); slices.Contains(have, "playwright") {
		t.Errorf("a new project got playwright without asking: %v", have)
	}

	if _, err := AddServices(dir, []string{"playwright"}); err != nil {
		t.Fatal(err)
	}
	compose := read(t, filepath.Join(dir, "compose.yml"))
	for _, want := range []string{
		"image: mcr.microsoft.com/playwright:v" + PlaywrightVersion + "-noble",
		"@playwright/mcp@" + PlaywrightMCPVersion,
		"-p playwright@" + PlaywrightVersion + " -c 'command -v playwright'",
		`"$$pw" run-server --host 0.0.0.0 --port 3000 &`,
		`net.connect(80, "proxy")`,
		`}).listen(9323, "0.0.0.0");' "$$(dirname "$$pw")/../playwright-core/lib/vite/traceViewer" &`,
		`volumes: ["playwright:/root/.npm", "./repos:/repos:ro"]`,
		"\n  playwright:\n",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yml lacks %q:\n%s", want, compose)
		}
	}
	if got := read(t, filepath.Join(dir, "Caddyfile")); !strings.Contains(got, PlaywrightSite) || !strings.Contains(got, "reverse_proxy playwright:9323") {
		t.Errorf("Caddyfile:\n%s", got)
	}
	if got := read(t, filepath.Join(dir, "environments/_shared/base.env")); !strings.Contains(got, "PLAYWRIGHT_SERVER_URL=ws://playwright:3000/") {
		t.Errorf("base.env:\n%s", got)
	}
}
