// Package engine picks the container engine neptuno drives: Docker, or
// Podman where Docker isn't installed, such as a Mac with Podman Desktop or
// `brew install podman` instead of Docker Desktop.
//
// The rest of neptuno is written against the docker CLI: it runs `docker
// compose ...` and `docker volume ...`. Command turns such a command into
// the one the detected engine runs, so `docker compose ps` becomes `podman
// compose ps` and `docker volume ls` becomes `podman volume ls`. Podman's
// CLI takes the same arguments for every command neptuno runs.
package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// EnvEngine picks the engine, "docker" or "podman", instead of
	// detecting it.
	EnvEngine = "NEPTUNO_ENGINE"
	// EnvCompose is the command that runs compose, such as
	// "podman-compose" or "docker-compose", instead of the engine's own
	// `docker compose` or `podman compose`.
	EnvCompose = "NEPTUNO_COMPOSE"

	Docker = "docker"
	Podman = "podman"
)

// Engine is a container engine and the command that runs compose with it.
type Engine struct {
	// Name is the engine's CLI, "docker" or "podman".
	Name string
	// Compose is the command line that runs compose, such as
	// ["docker", "compose"] or ["podman", "compose"].
	Compose []string
}

// Detect picks the engine: the one EnvEngine names, else docker when it is
// on the PATH, else podman when it is, else docker, so errors name the
// usual one. lookPath and getenv stand in for exec.LookPath and os.Getenv.
func Detect(lookPath func(string) error, getenv func(string) string) Engine {
	name := strings.ToLower(strings.TrimSpace(getenv(EnvEngine)))
	if name != Docker && name != Podman {
		name = Docker
		if lookPath(Docker) != nil && lookPath(Podman) == nil {
			name = Podman
		}
	}
	e := Engine{Name: name, Compose: []string{name, "compose"}}
	if c := strings.Fields(getenv(EnvCompose)); len(c) > 0 {
		e.Compose = c
	}
	return e
}

var current = sync.OnceValue(func() Engine {
	e := Detect(func(name string) error { _, err := exec.LookPath(name); return err }, os.Getenv)
	if e.IsPodman() {
		quietPodmanCompose()
	}
	return e
})

// EnvConfOverride names a containers.conf that Podman reads after all the
// others.
const EnvConfOverride = "CONTAINERS_CONF_OVERRIDE"

// quietConf turns off the banner `podman compose` prints on every run
// ("Executing external compose provider ..."), which would otherwise top
// every exec, log and Procfile line's output.
const quietConf = "[engine]\ncompose_warning_logs = false\n"

// quietPodmanCompose points EnvConfOverride, which the commands neptuno
// starts inherit, at a file with quietConf, unless it is already set.
func quietPodmanCompose() {
	if _, set := os.LookupEnv(EnvConfOverride); set {
		return
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "neptuno", "podman-compose.conf")
	if b, err := os.ReadFile(path); err != nil || string(b) != quietConf {
		if os.MkdirAll(filepath.Dir(path), 0o755) != nil || os.WriteFile(path, []byte(quietConf), 0o644) != nil {
			return
		}
	}
	os.Setenv(EnvConfOverride, path)
}

// Current is the engine on this machine, detected once. With Podman it
// also quiets podman compose's banner for the commands neptuno runs.
func Current() Engine { return current() }

// IsPodman reports whether the engine is Podman.
func (e Engine) IsPodman() bool { return e.Name == Podman }

// Command turns a docker command into the engine's: `docker compose ...`
// runs e.Compose, any other `docker ...` runs e.Name. Other commands are
// returned as they are.
func (e Engine) Command(name string, args ...string) (string, []string) {
	if name != Docker || e.Name == "" {
		return name, args
	}
	if len(args) > 0 && args[0] == "compose" && len(e.Compose) > 0 {
		return e.Compose[0], append(append([]string{}, e.Compose[1:]...), args[1:]...)
	}
	return e.Name, args
}

// Argv is Command for a whole command line.
func (e Engine) Argv(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	name, args := e.Command(argv[0], argv[1:]...)
	return append([]string{name}, args...)
}

// Programs are what must be installed for docker commands to run: the
// engine's CLI and, when it is another program, the compose command.
func (e Engine) Programs() []string {
	out := []string{e.Name}
	if len(e.Compose) > 0 && e.Compose[0] != e.Name {
		out = append(out, e.Compose[0])
	}
	return out
}

// ComposeCommand is the compose command as a person types it, such as
// "docker compose" or "podman compose".
func (e Engine) ComposeCommand() string { return strings.Join(e.Compose, " ") }
