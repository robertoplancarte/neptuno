package orchestrate

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/robertoplancarte/neptuno/internal/project"
)

// PlaywrightService is the shared service that runs the browsers repos'
// system tests drive, and the Trace Viewer that opens their traces.
const PlaywrightService = "playwright"

// TracesDir is where, inside a repo, its system tests save Playwright
// traces: the rails stack's playwright option saves each test's as
// <test>.zip, next to a <test>.json describing the run.
const TracesDir = "tmp/playwright-traces"

// maxTraces is how many traces Traces lists, newest first.
const maxTraces = 200

// Trace is one Playwright trace a repo's system test saved.
type Trace struct {
	// Repo is the repo whose test saved it.
	Repo string
	// Test names the test, such as CheckoutTest#test_paying_by_card.
	Test string
	// Result is how the test went: passed, failed, error or skipped, or ""
	// when nothing says.
	Result string
	// Location is the test's file and line in the repo, such as
	// test/system/checkout_test.rb:12, when known.
	Location string
	// Failure is the start of the failure message of a test that failed.
	Failure string
	// File is the trace's base name and Path its full path on this machine.
	File, Path string
	// Finished is when the test ended, and Duration how long it ran.
	Finished time.Time
	Duration time.Duration
	// Size is the trace file's size in bytes.
	Size int64
}

// Failed reports whether the trace is of a test that failed or errored.
func (t Trace) Failed() bool { return t.Result == "failed" || t.Result == "error" }

// UsesPlaywright reports whether a repo's system tests drive the playwright
// service's browsers: its container depends on the service, or, for a repo
// run on the host, neptuno.yml lists it in its depends_on.
func UsesPlaywright(p *project.Project, repo string) bool {
	if !p.IsRepo(repo) {
		return false
	}
	deps := p.Services[repo].DependsOn
	if p.RunMode(repo) == project.RunHost {
		deps = p.Config.Repos[repo].DependsOn
	}
	return slices.Contains(deps, PlaywrightService)
}

// PlaywrightRepos lists the repos that use Playwright, sorted.
func (o *Orchestrator) PlaywrightRepos() []string {
	var out []string
	for _, r := range o.Project.Repos {
		if UsesPlaywright(o.Project, r) {
			out = append(out, r)
		}
	}
	return out
}

// Traces lists the Playwright traces in the TracesDir of every repo that
// uses Playwright, newest first.
func (o *Orchestrator) Traces() ([]Trace, error) {
	var all []Trace
	var errs []error
	for _, repo := range o.PlaywrightRepos() {
		list, err := readTraces(repo, o.Project.Path("repos", repo, TracesDir))
		if err != nil {
			errs = append(errs, err)
		}
		all = append(all, list...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Finished.Equal(all[j].Finished) {
			return all[i].Finished.After(all[j].Finished)
		}
		return all[i].Repo+all[i].Test < all[j].Repo+all[j].Test
	})
	if len(all) > maxTraces {
		all = all[:maxTraces]
	}
	return all, errors.Join(errs...)
}

// traceInfo is the .json the rails stack saves next to each trace.
type traceInfo struct {
	Test       string  `json:"test"`
	Result     string  `json:"result"`
	Location   string  `json:"location"`
	FinishedAt string  `json:"finished_at"`
	Duration   float64 `json:"duration"`
	Failure    string  `json:"failure"`
}

// readTraces reads the traces in one repo's folder: each .zip, with what
// the .json of the same name says about it. A trace without one, such as a
// test suite of another stack saved, is named after its file.
func readTraces(repo, dir string) ([]Trace, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Trace
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".zip") || strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(name, ".zip")
		t := Trace{Repo: repo, Test: base, File: name, Path: filepath.Join(dir, name), Finished: info.ModTime(), Size: info.Size()}
		if b, err := os.ReadFile(filepath.Join(dir, base+".json")); err == nil {
			var ti traceInfo
			if json.Unmarshal(b, &ti) == nil {
				if ti.Test != "" {
					t.Test = ti.Test
				}
				t.Result, t.Location, t.Failure = ti.Result, ti.Location, strings.TrimSpace(ti.Failure)
				if at, err := time.Parse(time.RFC3339, ti.FinishedAt); err == nil {
					t.Finished = at
				}
				t.Duration = time.Duration(ti.Duration * float64(time.Second))
			}
		}
		out = append(out, t)
	}
	return out, nil
}
