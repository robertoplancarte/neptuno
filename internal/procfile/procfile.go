// Package procfile reads Procfiles and rewrites them so overmind, running on
// the host, executes each process inside its repo's container.
package procfile

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Entry is one Procfile line.
type Entry struct {
	Name, Command string
}

var linePattern = regexp.MustCompile(`^([A-Za-z0-9_-]+):\s*(.+)$`)

// Parse reads Procfile entries, skipping blank lines and # comments.
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := linePattern.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: expected \"name: command\"", n)
		}
		entries = append(entries, Entry{Name: m[1], Command: strings.TrimSpace(m[2])})
	}
	return entries, sc.Err()
}

// ParseFile reads a Procfile from disk.
func ParseFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return entries, nil
}

// ReadyMarker and FailedMarker are what a container's entrypoint.sh writes
// when its setup, such as bundle install, succeeds or fails. They live on
// /dev/shm, a tmpfs docker mounts fresh each time a container starts, so a
// marker never survives into the next start.
const (
	ReadyMarker  = "/dev/shm/neptuno-ready"
	FailedMarker = "/dev/shm/neptuno-failed"
	// SetupLog holds the entrypoint's setup output, which a process shows
	// when setup failed.
	SetupLog = "/dev/shm/neptuno-setup.log"
)

// ProcsDir holds a file per process that a Procfile line runs in a
// container, at <ProcsDir>/<run>/<process>.<instance>, with the PID and start
// time of the shell that runs it. That shell is the leader of the process's
// process group, since docker compose exec starts each command in its own
// session, so killing the group stops the command and all it started.
const ProcsDir = "/dev/shm/neptuno-procs"

// StartTimeFunc defines neptuno_start, which prints a process's start time
// in clock ticks since boot: field 22 of /proc/<pid>/stat, counted after
// the name in parentheses, which can hold spaces. A PID and its start time
// name a process exactly, even after the PID is reused.
const StartTimeFunc = `neptuno_start() { s=$(cat /proc/$1/stat 2>/dev/null) || return 1; s=${s##*) }; set -- $s; echo "${20}"; }; `

// ContainerScript is the shell script `neptuno container-exec` runs in the
// container for a Procfile command. It first records the shell under
// ProcsDir, by the NEPTUNO_RUN and NEPTUNO_PROC variables container-exec
// sets, so the process can be found and stopped later. With
// waitForEntrypoint it then waits for the entrypoint to write a marker, and
// stops if it failed.
func ContainerScript(service, command string, waitForEntrypoint bool) string {
	record := StartTimeFunc + fmt.Sprintf(`if [ -n "$NEPTUNO_PROC" ] && mkdir -p "%[1]s/$NEPTUNO_RUN"; then echo "$$ $(neptuno_start $$)" > "%[1]s/$NEPTUNO_RUN/$NEPTUNO_PROC"; fi; `, ProcsDir)
	wait := ""
	if waitForEntrypoint {
		done := fmt.Sprintf("[ -e %s ] || [ -e %s ]", ReadyMarker, FailedMarker)
		wait = fmt.Sprintf("if ! { %[1]s; }; then echo \"Waiting for the %[3]s entrypoint.sh to finish (docker compose logs -f %[3]s)\"; "+
			"until %[1]s; do sleep 1; done; fi; "+
			"if [ -e %[2]s ]; then { echo \"The %[3]s entrypoint.sh failed, so this process did not start.\"; "+
			"if [ -s %[4]s ]; then echo \"Its last lines (all of them: docker compose logs %[3]s):\"; echo; tail -n 40 %[4]s; else echo \"See docker compose logs %[3]s\"; fi; "+
			"echo; echo \"Fix it, then run: docker compose restart %[3]s && neptuno restart %[3]s\"; } >&2; exit 1; fi; ",
			done, FailedMarker, service, SetupLog)
	}
	return record + wait + command
}

// WrapContainer runs each command inside the compose service through
// `neptuno container-exec`, which runs it with `docker compose exec` and
// stops it when the Procfile line is stopped or its session goes away.
// overmind gives each line a TTY, so exec allocates one too and debuggers
// and REPLs work. With waitForEntrypoint, each command first waits for the
// entrypoint to write a marker, and stops if it failed.
func WrapContainer(entries []Entry, neptuno, service string, waitForEntrypoint bool) []Entry {
	flag := ""
	if waitForEntrypoint {
		flag = " --wait-for-entrypoint"
	}
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[i] = Entry{e.Name, fmt.Sprintf("exec %s container-exec%s %s %s %s", Quote(neptuno), flag, Quote(service), Quote(e.Name), Quote(e.Command))}
	}
	return out
}

// WrapHost runs each command on the host through `neptuno host-exec`, which
// loads the repo's resolved env file and changes into its folder first.
func WrapHost(entries []Entry, neptuno, repo string) []Entry {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[i] = Entry{e.Name, fmt.Sprintf("exec %s host-exec %s %s", Quote(neptuno), Quote(repo), Quote(e.Command))}
	}
	return out
}

// Write saves entries as a Procfile.
func Write(path string, entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Generated by neptuno from procfiles/. Edit that Procfile instead.\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "%s: %s\n", e.Name, e.Command)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// Quote single-quotes s for sh.
func Quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]{}~#!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
