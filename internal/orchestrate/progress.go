package orchestrate

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The phases a service goes through while `neptuno up` gets its container
// going, before docker compose ps shows it running.
const (
	// PhaseWaiting is a service whose image is ready while compose pulls or
	// builds the others' first.
	PhaseWaiting  = "waiting"
	PhasePulling  = "pulling"
	PhaseBuilding = "building"
	PhaseCreating = "creating"
	PhaseStarting = "starting"
)

// Progress is what a running `neptuno up` is doing to a service's container,
// read from docker compose's output as it comes.
type Progress struct {
	Phase string `json:"phase"`
	// Step and Steps count a build's Dockerfile steps, or a pull's layers,
	// when compose's output tells them apart: step 3 of 7.
	Step  int `json:"step,omitempty"`
	Steps int `json:"steps,omitempty"`
	// Detail is the build step under way, such as RUN bundle install.
	Detail string `json:"detail,omitempty"`
	// Output is the last line the step printed.
	Output string `json:"output,omitempty"`
	// Since is when the phase began, and Updated when compose last printed
	// anything about the service.
	Since   time.Time `json:"since"`
	Updated time.Time `json:"updated"`
}

// Busy reports whether the phase is work under way, not waiting.
func (p *Progress) Busy() bool {
	return p != nil && p.Phase != PhaseWaiting
}

// progressFile is what one `neptuno up` writes to .neptuno/run/progress.
type progressFile struct {
	PID      int                  `json:"pid"`
	Services map[string]*Progress `json:"services"`
}

func progressDir() string {
	return filepath.Join(".neptuno", "run", "progress")
}

// readProgress collects what every running `neptuno up` reports, by
// service, removing the files of those that are gone. The latest report
// wins when two runs start the same service.
func readProgress(root string) map[string]*Progress {
	dir := filepath.Join(root, progressDir())
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string]*Progress{}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var f progressFile
		if json.Unmarshal(b, &f) != nil || !alive(f.PID) {
			_ = os.Remove(path)
			continue
		}
		for name, p := range f.Services {
			if prev, ok := out[name]; !ok || p.Updated.After(prev.Updated) {
				out[name] = p
			}
		}
	}
	return out
}

// alive reports whether a process with this pid runs.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// progressTracker reads docker compose up's plain output and keeps a file
// under .neptuno/run/progress saying where each service is, so the
// dashboard can show it pulling or building instead of stopped.
type progressTracker struct {
	path     string
	project  string
	services []string
	// images maps each service to the image compose pulls or builds for
	// it, normalized.
	images map[string]string
	now    func() time.Time

	mu      sync.Mutex
	file    progressFile
	partial string
	// vertices maps buildkit's step numbers, such as #6, to the service
	// they build.
	vertices map[string]string
	// layers are the image layers being pulled, done or not.
	layers  map[string]bool
	written time.Time
	dirty   bool
}

// newProgressTracker starts tracking services, all waiting for now. images
// maps a service to its image as compose.yml names it, if it does.
func newProgressTracker(root, project string, services []string, images map[string]string, now func() time.Time) *progressTracker {
	t := &progressTracker{
		path:     filepath.Join(root, progressDir(), strconv.Itoa(os.Getpid())+".json"),
		project:  project,
		services: services,
		images:   map[string]string{},
		now:      now,
		file:     progressFile{PID: os.Getpid(), Services: map[string]*Progress{}},
		vertices: map[string]string{},
		layers:   map[string]bool{},
	}
	for _, s := range services {
		image := images[s]
		if image == "" {
			image = project + "-" + s
		}
		t.images[s] = normalizeImage(image)
	}
	start := now()
	for _, s := range services {
		t.file.Services[s] = &Progress{Phase: PhaseWaiting, Since: start, Updated: start}
	}
	t.save()
	return t
}

// Close removes the progress file: the containers' own state takes over.
func (t *progressTracker) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = os.Remove(t.path)
}

func (t *progressTracker) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.partial + string(b)
	for {
		i := strings.IndexAny(s, "\r\n")
		if i < 0 {
			break
		}
		t.line(s[:i])
		s = s[i+1:]
	}
	t.partial = s
	if t.dirty && t.now().Sub(t.written) >= 250*time.Millisecond {
		t.save()
	}
	return len(b), nil
}

// save writes the file, through a rename so readers never see half of it.
func (t *progressTracker) save() {
	b, err := json.Marshal(t.file)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return
	}
	tmp := t.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, t.path)
	}
	t.written, t.dirty = t.now(), false
}

var (
	// buildkitLine is a line of a build's plain output, such as
	// `#6 [web 2/3] RUN make` or `#6 12.34 compiling foo.c`.
	buildkitLine = regexp.MustCompile(`^#(\d+) (.*)$`)
	buildkitStep = regexp.MustCompile(`^\[([^\]]*)\] ?(.*)$`)
	stepCount    = regexp.MustCompile(`^(\d+)/(\d+)$`)
	// layerLine is a pull's line about one layer, such as
	// `ff91ba65c239 Downloading 4.194MB`.
	layerLine = regexp.MustCompile(`^([0-9a-f]{12}) (.+)$`)
	// timestamp starts the lines a build step prints.
	timestamp = regexp.MustCompile(`^\d+\.\d+ `)
)

// line reads one line of compose's output.
func (t *progressTracker) line(line string) {
	line = strings.TrimRight(line, " \t")
	if m := buildkitLine.FindStringSubmatch(line); m != nil {
		t.buildLine(m[1], m[2])
		return
	}
	text := strings.TrimSpace(line)
	if m := layerLine.FindStringSubmatch(text); m != nil {
		t.layerLine(m[1], m[2])
		return
	}
	f := strings.Fields(text)
	if len(f) > 0 && (f[0] == "Image" || f[0] == "Service") {
		f = f[1:]
	}
	if len(f) == 2 {
		switch f[1] {
		case "Pulling":
			t.set(t.byImage(f[0]), PhasePulling)
		case "Building":
			t.set(t.byImage(f[0]), PhaseBuilding)
		case "Pulled", "Built", "Skipped":
			t.set(t.byImage(f[0]), PhaseWaiting)
		}
		return
	}
	if len(f) == 3 && f[0] == "Container" {
		svc := t.byContainer(f[1])
		switch f[2] {
		case "Creating", "Recreate":
			t.set(svc, PhaseCreating)
		case "Starting":
			t.set(svc, PhaseStarting)
		case "Created", "Recreated", "Waiting", "Healthy":
		default:
			// Started, Running and the like: compose ps tells the rest.
			t.done(svc)
		}
	}
}

// buildLine reads a line of a build: a step starting, such as
// `[web 2/3] RUN make`, or what step id printed.
func (t *progressTracker) buildLine(id, rest string) {
	if m := buildkitStep.FindStringSubmatch(rest); m != nil {
		fields := strings.Fields(m[1])
		svc := ""
		if len(fields) > 0 && slices.Contains(t.services, fields[0]) {
			svc, fields = fields[0], fields[1:]
		} else if b := t.inPhase(PhaseBuilding); len(b) == 1 {
			svc = b[0]
		}
		if svc == "" {
			return
		}
		t.vertices[id] = svc
		p := t.file.Services[svc]
		if p.Phase != PhaseBuilding {
			t.set([]string{svc}, PhaseBuilding)
		}
		if len(fields) == 1 {
			if c := stepCount.FindStringSubmatch(fields[0]); c != nil {
				p.Step, _ = strconv.Atoi(c[1])
				p.Steps, _ = strconv.Atoi(c[2])
			}
		}
		p.Detail, p.Output = m[2], ""
		t.touch(svc)
		return
	}
	svc, ok := t.vertices[id]
	if !ok {
		return
	}
	if !strings.HasPrefix(rest, "DONE") && rest != "CACHED" {
		t.file.Services[svc].Output = timestamp.ReplaceAllString(rest, "")
	}
	t.touch(svc)
}

// layerLine reads a pull's line about one layer. Layers can't be told
// apart by image, so they count toward the image only while it pulls
// alone.
func (t *progressTracker) layerLine(id, status string) {
	switch {
	case strings.HasPrefix(status, "Pulling fs layer"), strings.HasPrefix(status, "Waiting"):
		if _, ok := t.layers[id]; !ok {
			t.layers[id] = false
		}
	case strings.HasPrefix(status, "Pull complete"), strings.HasPrefix(status, "Already exists"):
		t.layers[id] = true
	}
	pulling := t.inPhase(PhasePulling)
	for _, svc := range pulling {
		p := t.file.Services[svc]
		if len(pulling) == 1 {
			p.Steps, p.Step = len(t.layers), 0
			for _, done := range t.layers {
				if done {
					p.Step++
				}
			}
			p.Output = id + " " + status
		}
		t.touch(svc)
	}
}

// set moves services to a phase.
func (t *progressTracker) set(svcs []string, phase string) {
	now := t.now()
	for _, s := range svcs {
		p, ok := t.file.Services[s]
		if !ok || p.Phase == phase {
			continue
		}
		if phase == PhasePulling && len(t.inPhase(PhasePulling)) == 0 {
			t.layers = map[string]bool{}
		}
		*p = Progress{Phase: phase, Since: now, Updated: now}
		// A new phase is worth showing right away.
		t.written = time.Time{}
		t.dirty = true
	}
}

func (t *progressTracker) done(svcs []string) {
	for _, s := range svcs {
		if _, ok := t.file.Services[s]; ok {
			delete(t.file.Services, s)
			t.written, t.dirty = time.Time{}, true
		}
	}
}

func (t *progressTracker) touch(svc string) {
	t.file.Services[svc].Updated = t.now()
	t.dirty = true
}

func (t *progressTracker) inPhase(phase string) []string {
	var out []string
	for _, s := range t.services {
		if p, ok := t.file.Services[s]; ok && p.Phase == phase {
			out = append(out, s)
		}
	}
	return out
}

// byImage finds the services an image is for: compose names images in its
// output, or services in older versions.
func (t *progressTracker) byImage(name string) []string {
	if slices.Contains(t.services, name) {
		return []string{name}
	}
	image := normalizeImage(name)
	var out []string
	for _, s := range t.services {
		if t.images[s] == image {
			out = append(out, s)
		}
	}
	return out
}

// byContainer finds the service of a container compose names, such as
// acme-web-1.
func (t *progressTracker) byContainer(name string) []string {
	base := name
	if i := strings.LastIndex(base, "-"); i > 0 {
		if _, err := strconv.Atoi(base[i+1:]); err == nil {
			base = base[:i]
		}
	}
	best := ""
	for _, s := range t.services {
		if (base == s || base == t.project+"-"+s || strings.HasSuffix(base, "-"+s)) && len(s) > len(best) {
			best = s
		}
	}
	if best == "" {
		return nil
	}
	return []string{best}
}

// normalizeImage drops what docker fills in by default, so valkey/valkey,
// docker.io/valkey/valkey and docker.io/valkey/valkey:latest match.
func normalizeImage(image string) string {
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "library/")
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		if image[i+1:] == "latest" {
			image = image[:i]
		}
	}
	return image
}

// composeImages asks compose for the project's name and the image each
// service names, which its output refers to them by.
func (o *Orchestrator) composeImages() (string, map[string]string) {
	project := strings.ToLower(o.Project.Name)
	out, err := o.Runner.Output(o.Project.Root, "docker", "compose", "config", "--format", "json")
	if err != nil {
		return project, nil
	}
	var cfg struct {
		Name     string `json:"name"`
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if json.Unmarshal([]byte(out), &cfg) != nil {
		return project, nil
	}
	if cfg.Name != "" {
		project = cfg.Name
	}
	images := map[string]string{}
	for name, s := range cfg.Services {
		images[name] = s.Image
	}
	return project, images
}

// streamRunner is a Runner that can also copy a command's output to w.
type streamRunner interface {
	Stream(w io.Writer, dir, name string, args ...string) error
}

// composeUp runs docker compose up for services, reporting each one's
// progress for the dashboard while it pulls, builds and starts them.
func (o *Orchestrator) composeUp(services []string) error {
	args := append([]string{"compose", "up", "--detach"}, services...)
	sr, ok := o.Runner.(streamRunner)
	if !ok {
		return o.Runner.Run(o.Project.Root, "docker", args...)
	}
	project, images := o.composeImages()
	t := newProgressTracker(o.Project.Root, project, services, images, time.Now)
	defer t.Close()
	return sr.Stream(t, o.Project.Root, "docker", args...)
}
