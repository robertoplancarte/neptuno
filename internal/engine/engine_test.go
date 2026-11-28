package engine

import (
	"errors"
	"slices"
	"testing"
)

func detect(installed []string, env map[string]string) Engine {
	return Detect(func(name string) error {
		if slices.Contains(installed, name) {
			return nil
		}
		return errors.New("not found")
	}, func(k string) string { return env[k] })
}

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed []string
		env       map[string]string
		want      Engine
	}{
		{"docker only", []string{"docker"}, nil, Engine{"docker", []string{"docker", "compose"}}},
		{"both prefers docker", []string{"docker", "podman"}, nil, Engine{"docker", []string{"docker", "compose"}}},
		{"podman only", []string{"podman"}, nil, Engine{"podman", []string{"podman", "compose"}}},
		{"neither", nil, nil, Engine{"docker", []string{"docker", "compose"}}},
		{"env picks podman", []string{"docker", "podman"}, map[string]string{EnvEngine: "Podman"}, Engine{"podman", []string{"podman", "compose"}}},
		{"env picks docker", []string{"podman"}, map[string]string{EnvEngine: "docker"}, Engine{"docker", []string{"docker", "compose"}}},
		{"unknown env detects", []string{"podman"}, map[string]string{EnvEngine: "nerdctl"}, Engine{"podman", []string{"podman", "compose"}}},
		{"compose override", []string{"podman"}, map[string]string{EnvCompose: " podman-compose "}, Engine{"podman", []string{"podman-compose"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := detect(tc.installed, tc.env)
			if got.Name != tc.want.Name || !slices.Equal(got.Compose, tc.want.Compose) {
				t.Errorf("Detect = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCommand(t *testing.T) {
	podman := Engine{Name: "podman", Compose: []string{"podman", "compose"}}
	external := Engine{Name: "podman", Compose: []string{"podman-compose"}}
	docker := Engine{Name: "docker", Compose: []string{"docker", "compose"}}
	for _, tc := range []struct {
		e    Engine
		in   []string
		want []string
	}{
		{podman, []string{"docker", "compose", "ps", "--quiet"}, []string{"podman", "compose", "ps", "--quiet"}},
		{podman, []string{"docker", "volume", "ls"}, []string{"podman", "volume", "ls"}},
		{podman, []string{"tmux", "attach"}, []string{"tmux", "attach"}},
		{external, []string{"docker", "compose", "up", "--detach"}, []string{"podman-compose", "up", "--detach"}},
		{external, []string{"docker", "run", "--rm"}, []string{"podman", "run", "--rm"}},
		{docker, []string{"docker", "compose", "logs"}, []string{"docker", "compose", "logs"}},
		{podman, []string{"docker"}, []string{"podman"}},
	} {
		if got := tc.e.Argv(tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("%v.Argv(%q) = %q, want %q", tc.e.Name, tc.in, got, tc.want)
		}
	}
	in := []string{"compose", "ps"}
	podman.Command("docker", in...)
	if !slices.Equal(in, []string{"compose", "ps"}) {
		t.Errorf("Command changed its arguments: %q", in)
	}
}

func TestPrograms(t *testing.T) {
	if got := (Engine{Name: "podman", Compose: []string{"podman", "compose"}}).Programs(); !slices.Equal(got, []string{"podman"}) {
		t.Errorf("Programs = %q", got)
	}
	if got := (Engine{Name: "podman", Compose: []string{"podman-compose"}}).Programs(); !slices.Equal(got, []string{"podman", "podman-compose"}) {
		t.Errorf("Programs = %q", got)
	}
}
