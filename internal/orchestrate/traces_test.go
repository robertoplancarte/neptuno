package orchestrate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/robertoplancarte/neptuno/internal/project"
)

func tracesProject(t *testing.T) *Orchestrator {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"compose.yml": `name: acme
services:
  postgres: {image: postgres:17}
  playwright: {image: mcr.microsoft.com/playwright}
  shop:
    build: dockerfiles/shop
    depends_on: [postgres, playwright]
  api:
    build: dockerfiles/api
    depends_on: [postgres]
`,
		"neptuno.yml": "repos:\n  docs:\n    run: host\n    depends_on: [playwright]\n",
		// Saved by the rails stack: a failure, then a pass kept with
		// PLAYWRIGHT_TRACE=all.
		"repos/shop/tmp/playwright-traces/CheckoutTest_test_paying.zip": "zip",
		"repos/shop/tmp/playwright-traces/CheckoutTest_test_paying.json": `{"test": "CheckoutTest#test_paying", "result": "failed",
			"location": "test/system/checkout_test.rb:4", "finished_at": "2026-11-26T10:00:00Z", "duration": 1.5,
			"failure": "expected to find text \"Paid\"\n"}`,
		"repos/shop/tmp/playwright-traces/CartTest_test_adding.zip":  "zip",
		"repos/shop/tmp/playwright-traces/CartTest_test_adding.json": `{"test": "CartTest#test_adding", "result": "passed", "finished_at": "2026-11-26T11:00:00Z"}`,
		// Not a trace.
		"repos/shop/tmp/playwright-traces/notes.txt": "",
		// A host repo's trace with no .json.
		"repos/docs/tmp/playwright-traces/search.zip": "zip",
		// api doesn't use Playwright, so its folder is not read.
		"repos/api/tmp/playwright-traces/stray.zip": "zip",
	}
	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, "repos/docs/tmp/playwright-traces/search.zip"), old, old); err != nil {
		t.Fatal(err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return &Orchestrator{Project: p}
}

func TestPlaywrightRepos(t *testing.T) {
	o := tracesProject(t)
	if got := o.PlaywrightRepos(); !reflect.DeepEqual(got, []string{"docs", "shop"}) {
		t.Errorf("PlaywrightRepos = %v, want docs and shop", got)
	}
	if UsesPlaywright(o.Project, "playwright") {
		t.Error("the playwright service itself is not a repo")
	}
}

func TestTraces(t *testing.T) {
	o := tracesProject(t)
	list, err := o.Traces()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tr := range list {
		got = append(got, tr.Repo+" "+tr.Test+" "+tr.Result)
	}
	// Newest first.
	want := []string{"shop CartTest#test_adding passed", "shop CheckoutTest#test_paying failed", "docs search "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("traces = %q, want %q", got, want)
	}
	pay := list[1]
	if pay.Location != "test/system/checkout_test.rb:4" || pay.Duration != 1500*time.Millisecond ||
		pay.Failure != `expected to find text "Paid"` || !pay.Failed() || pay.File != "CheckoutTest_test_paying.zip" ||
		pay.Path != o.Project.Path("repos/shop/tmp/playwright-traces/CheckoutTest_test_paying.zip") || pay.Size != 3 {
		t.Errorf("failed trace = %+v", pay)
	}
	if !list[2].Finished.Equal(time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("a trace with no .json is dated by its file: %v", list[2].Finished)
	}
}

func TestTracesWithoutFolders(t *testing.T) {
	o := tracesProject(t)
	if err := os.RemoveAll(o.Project.Path("repos/shop/tmp")); err != nil {
		t.Fatal(err)
	}
	list, err := o.Traces()
	if err != nil || len(list) != 1 {
		t.Errorf("Traces = %v, %v; want docs's one", list, err)
	}
}
