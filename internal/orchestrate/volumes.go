package orchestrate

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Volume is a named volume of the project's compose file.
type Volume struct {
	// Name is the volume's key in compose.yml, such as postgres_data.
	Name string
	// Docker is the volume's name in docker, such as acme_postgres_data.
	Docker string
	// External is true for a volume compose.yml declares external, which
	// compose doesn't create, so neptuno won't delete it.
	External bool
	// Services are the services that mount it, sorted.
	Services []string
	// Mounts are where each of them mounts it, in the same order.
	Mounts []Mount
	// Exists is true when docker has the volume.
	Exists bool
}

// Mount is where a service mounts a volume.
type Mount struct {
	Service string
	// Target is the path inside the service's container.
	Target string
}

// composeConfig is the part of `docker compose config --format json` that
// lists volumes.
type composeConfig struct {
	Services map[string]struct {
		Volumes []struct {
			Type   string `json:"type"`
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"volumes"`
	} `json:"services"`
	Volumes map[string]struct {
		Name     string `json:"name"`
		External bool   `json:"external"`
	} `json:"volumes"`
}

// Volumes lists the project's named volumes, the services that mount each,
// and whether docker has it yet. Compose resolves the files, so overrides,
// long syntax and custom names all count.
func (o *Orchestrator) Volumes() ([]Volume, error) {
	root := o.Project.Root
	// Env files needn't exist to list volumes.
	out, err := o.Runner.Output(root, "docker", "compose", "config", "--format", "json", "--no-env-resolution")
	if err != nil {
		out, err = o.Runner.Output(root, "docker", "compose", "config", "--format", "json")
	}
	if err != nil {
		return nil, fmt.Errorf("docker compose config: %w", err)
	}
	vols, err := parseComposeVolumes(out)
	if err != nil {
		return nil, err
	}
	if len(vols) == 0 {
		return nil, nil
	}
	if list, err := o.Runner.Output(root, "docker", "volume", "ls", "--quiet"); err == nil {
		have := strings.Fields(list)
		for i := range vols {
			vols[i].Exists = slices.Contains(have, vols[i].Docker)
		}
	}
	return vols, nil
}

// parseComposeVolumes reads the named volumes out of `docker compose config
// --format json`, sorted by name.
func parseComposeVolumes(out string) ([]Volume, error) {
	var cf composeConfig
	if err := json.Unmarshal([]byte(out), &cf); err != nil {
		return nil, fmt.Errorf("reading docker compose config: %w", err)
	}
	mounts := map[string][]Mount{}
	for svc, def := range cf.Services {
		for _, v := range def.Volumes {
			// Anonymous volumes have no source and belong to one container.
			if v.Type == "volume" && v.Source != "" {
				mounts[v.Source] = append(mounts[v.Source], Mount{Service: svc, Target: v.Target})
			}
		}
	}
	var vols []Volume
	for name, def := range cf.Volumes {
		v := Volume{Name: name, Docker: def.Name, External: def.External, Mounts: mounts[name]}
		if v.Docker == "" {
			v.Docker = name
		}
		sort.Slice(v.Mounts, func(i, j int) bool {
			a, b := v.Mounts[i], v.Mounts[j]
			return a.Service < b.Service || a.Service == b.Service && a.Target < b.Target
		})
		for _, m := range v.Mounts {
			if !slices.Contains(v.Services, m.Service) {
				v.Services = append(v.Services, m.Service)
			}
		}
		vols = append(vols, v)
	}
	sort.Slice(vols, func(i, j int) bool { return vols[i].Name < vols[j].Name })
	return vols, nil
}

// findVolume picks a volume by its compose or docker name.
func findVolume(vols []Volume, name string) (Volume, error) {
	for _, v := range vols {
		if v.Name == name || v.Docker == name {
			return v, nil
		}
	}
	var names []string
	for _, v := range vols {
		names = append(names, v.Name)
	}
	if len(names) == 0 {
		return Volume{}, fmt.Errorf("unknown volume %q: compose.yml declares no named volumes", name)
	}
	return Volume{}, fmt.Errorf("unknown volume %q (the project has %s)", name, strings.Join(names, ", "))
}

// ResetVolume deletes a volume's data so it starts over: it stops the
// services that mount it and removes their containers, which hold on to it,
// deletes it, then starts again those that were running, which recreates
// it empty. Postgres then initializes a new database, and a Rails app's
// entrypoint reinstalls its gems.
func (o *Orchestrator) ResetVolume(name string) error {
	vols, err := o.Volumes()
	if err != nil {
		return err
	}
	v, err := findVolume(vols, name)
	if err != nil {
		return err
	}
	if v.External {
		return fmt.Errorf("%s is an external volume, which compose.yml doesn't own; remove it with `docker volume rm %s` if you mean to", v.Name, v.Docker)
	}
	p := o.Project
	containers := o.composeContainers()
	var running []string
	for _, svc := range v.Services {
		if containers[svc].State == "running" {
			running = append(running, svc)
		}
	}

	if len(v.Services) > 0 {
		fmt.Fprintf(o.Log, "Stopping what uses %s: %s\n", v.Name, strings.Join(v.Services, ", "))
		if err := o.Down(v.Services); err != nil {
			return err
		}
		// A stopped container still holds its volumes.
		args := append([]string{"compose", "rm", "--force", "--stop"}, v.Services...)
		if err := o.Runner.Run(p.Root, "docker", args...); err != nil {
			return err
		}
	}
	if v.Exists {
		fmt.Fprintf(o.Log, "Deleting volume %s\n", v.Docker)
		if err := o.Runner.Run(p.Root, "docker", "volume", "rm", v.Docker); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(o.Log, "Volume %s doesn't exist yet; nothing to delete\n", v.Docker)
	}
	if len(running) == 0 {
		if len(v.Services) > 0 {
			fmt.Fprintf(o.Log, "None of %s was running; the volume is recreated the next time one starts\n", strings.Join(v.Services, ", "))
		}
		return nil
	}
	fmt.Fprintf(o.Log, "Starting %s again, which recreates %s\n", strings.Join(running, ", "), v.Name)
	return o.Up(UpOptions{Selected: running, Detach: true})
}

// VolumeDetail is what docker knows about a volume and what is in it.
type VolumeDetail struct {
	// Driver, Mountpoint and Created come from `docker volume inspect`.
	// The mountpoint is inside Docker's VM on a Mac.
	Driver, Mountpoint string
	Created            time.Time
	// Size is its disk usage as du prints it, such as 48M.
	Size string
	// Entries are its top-level files and folders, folders ending in /,
	// and More counts the ones left out.
	Entries []string
	More    int
	// Via says where it was looked at: in which running container, or in
	// a throwaway one.
	Via string
}

// maxEntries caps how many of a volume's top-level entries are listed.
const maxEntries = 30

// peekScript prints the disk usage of the folder in $1, then its top-level
// entries, folders marked with a slash.
const peekScript = `cd "$1" && du -sh . 2>/dev/null | cut -f1 && ls -A1p`

// InspectVolume looks inside a volume: its size and top-level entries. It
// runs du and ls in a running container that mounts it, or else in a
// throwaway busybox container that mounts it read-only.
func (o *Orchestrator) InspectVolume(v Volume) (VolumeDetail, error) {
	root := o.Project.Root
	var d VolumeDetail
	if !v.Exists {
		return d, nil
	}
	if out, err := o.Runner.Output(root, "docker", "volume", "inspect", "--format", "{{json .}}", v.Docker); err == nil {
		var info struct {
			Driver     string `json:"Driver"`
			Mountpoint string `json:"Mountpoint"`
			CreatedAt  string `json:"CreatedAt"`
		}
		if json.Unmarshal([]byte(out), &info) == nil {
			d.Driver, d.Mountpoint = info.Driver, info.Mountpoint
			d.Created, _ = time.Parse(time.RFC3339, info.CreatedAt)
		}
	}
	containers := o.composeContainers()
	out, ran := "", false
	for _, m := range v.Mounts {
		if containers[m.Service].State != "running" {
			continue
		}
		var err error
		if out, err = o.Runner.Output(root, "docker", "compose", "exec", "-T", m.Service, "sh", "-c", peekScript, "sh", m.Target); err == nil {
			d.Via, ran = "inside the running "+m.Service+" container, at "+m.Target, true
			break
		}
	}
	if !ran {
		var err error
		out, err = o.Runner.Output(root, "docker", "run", "--rm", "-v", v.Docker+":/volume:ro", "busybox", "sh", "-c", peekScript, "sh", "/volume")
		if err != nil {
			return d, fmt.Errorf("looking inside %s: %w", v.Docker, err)
		}
		d.Via = "in a throwaway busybox container"
	}
	d.Size, d.Entries, d.More = parsePeek(out)
	return d, nil
}

// parsePeek reads peekScript's output.
func parsePeek(out string) (size string, entries []string, more int) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	size = strings.TrimSpace(lines[0])
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if len(entries) == maxEntries {
			more++
			continue
		}
		entries = append(entries, l)
	}
	return size, entries, more
}
