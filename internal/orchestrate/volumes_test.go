package orchestrate

import (
	"reflect"
	"strings"
	"testing"
)

const composeConfigJSON = `{
  "name": "acme",
  "services": {
    "api": {"volumes": [
      {"type": "bind", "source": "/x/repos/api", "target": "/app"},
      {"type": "volume", "source": "api_bundle", "target": "/usr/local/bundle"},
      {"type": "volume", "target": "/tmp/anon"}
    ]},
    "postgres": {"volumes": [{"type": "volume", "source": "postgres_data", "target": "/var/lib/postgresql/data"}]},
    "web": {"volumes": [{"type": "volume", "source": "api_bundle", "target": "/usr/local/bundle"}]}
  },
  "volumes": {
    "postgres_data": {"name": "acme_postgres_data"},
    "api_bundle": {"name": "acme_api_bundle"},
    "shared": {"name": "shared", "external": true}
  }
}`

// volumeRunner answers the docker commands that list volumes and
// containers.
type volumeRunner struct {
	*fakeRunner
	outputs map[string]string
}

func (r volumeRunner) Output(dir, name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	for prefix, out := range r.outputs {
		if strings.HasPrefix(cmd, prefix) {
			return out, nil
		}
	}
	return r.fakeRunner.Output(dir, name, args...)
}

func TestParseComposeVolumes(t *testing.T) {
	vols, err := parseComposeVolumes(composeConfigJSON)
	if err != nil {
		t.Fatal(err)
	}
	want := []Volume{
		{Name: "api_bundle", Docker: "acme_api_bundle", Services: []string{"api", "web"},
			Mounts: []Mount{{"api", "/usr/local/bundle"}, {"web", "/usr/local/bundle"}}},
		{Name: "postgres_data", Docker: "acme_postgres_data", Services: []string{"postgres"},
			Mounts: []Mount{{"postgres", "/var/lib/postgresql/data"}}},
		{Name: "shared", Docker: "shared", External: true},
	}
	if !reflect.DeepEqual(vols, want) {
		t.Errorf("volumes:\n%+v\nwant:\n%+v", vols, want)
	}
}

func volumeSetup(t *testing.T, ps string) (*Orchestrator, *fakeRunner) {
	o, f := setup(t)
	o.Runner = volumeRunner{f, map[string]string{
		"docker compose config":     composeConfigJSON,
		"docker volume ls":          "acme_postgres_data\nother_volume",
		"docker compose ps --all":   ps,
		"docker compose ps --quiet": "",
	}}
	return o, f
}

func TestVolumes(t *testing.T) {
	o, _ := volumeSetup(t, "")
	vols, err := o.Volumes()
	if err != nil {
		t.Fatal(err)
	}
	if len(vols) != 3 || vols[0].Exists || !vols[1].Exists {
		t.Errorf("volumes: %+v", vols)
	}
}

func TestResetVolumeRestartsWhatWasRunning(t *testing.T) {
	o, f := volumeSetup(t, `{"Service":"postgres","State":"running"}`)
	if err := o.ResetVolume("postgres_data"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker compose stop postgres",
		"docker compose rm --force --stop postgres",
		"docker volume rm acme_postgres_data",
		"docker compose up --detach postgres",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestResetVolumeLeavesStoppedServicesStopped(t *testing.T) {
	// The docker name works too, and a volume docker hasn't created yet
	// has nothing to delete.
	o, f := volumeSetup(t, "")
	if err := o.ResetVolume("acme_api_bundle"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"docker compose stop api web",
		"docker compose rm --force --stop api web",
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestResetVolumeRefuses(t *testing.T) {
	o, f := volumeSetup(t, "")
	if err := o.ResetVolume("shared"); err == nil || !strings.Contains(err.Error(), "external") {
		t.Errorf("external volume: %v", err)
	}
	if err := o.ResetVolume("nope"); err == nil || !strings.Contains(err.Error(), "api_bundle, postgres_data, shared") {
		t.Errorf("unknown volume: %v", err)
	}
	if len(f.cmds) != 0 {
		t.Errorf("ran %v", f.cmds)
	}
}

func TestInspectVolume(t *testing.T) {
	o, f := setup(t)
	r := volumeRunner{f, map[string]string{
		"docker volume inspect":           `{"Driver":"local","Mountpoint":"/var/lib/docker/volumes/acme_postgres_data/_data","CreatedAt":"2026-10-30T12:00:00Z"}`,
		"docker compose ps --all":         `{"Service":"postgres","State":"running"}`,
		"docker compose exec -T postgres": "48.2M\nbase/\nglobal/\nPG_VERSION\n",
	}}
	o.Runner = r
	v := Volume{Name: "postgres_data", Docker: "acme_postgres_data", Exists: true, Services: []string{"postgres"},
		Mounts: []Mount{{"postgres", "/var/lib/postgresql/data"}}}
	d, err := o.InspectVolume(v)
	if err != nil {
		t.Fatal(err)
	}
	if d.Driver != "local" || d.Size != "48.2M" || !reflect.DeepEqual(d.Entries, []string{"base/", "global/", "PG_VERSION"}) ||
		!strings.Contains(d.Via, "running postgres container") || d.Created.IsZero() {
		t.Errorf("detail: %+v", d)
	}

	// With postgres stopped, a throwaway container looks instead.
	delete(r.outputs, "docker compose ps --all")
	delete(r.outputs, "docker compose exec -T postgres")
	r.outputs["docker run --rm -v acme_postgres_data:/volume:ro busybox"] = "8.0K\n"
	if d, err = o.InspectVolume(v); err != nil || d.Size != "8.0K" || len(d.Entries) != 0 || !strings.Contains(d.Via, "throwaway") {
		t.Errorf("detail with postgres stopped: %+v, %v", d, err)
	}

	if d, err := o.InspectVolume(Volume{Docker: "acme_new"}); err != nil || d.Size != "" {
		t.Errorf("a volume docker hasn't created: %+v, %v", d, err)
	}
}

func TestParsePeekCapsEntries(t *testing.T) {
	out := "1.0M"
	for i := 0; i < maxEntries+5; i++ {
		out += "\nfile"
	}
	if size, entries, more := parsePeek(out); size != "1.0M" || len(entries) != maxEntries || more != 5 {
		t.Errorf("parsePeek: %q, %d entries, %d more", size, len(entries), more)
	}
}
