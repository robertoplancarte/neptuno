// Package procs keeps the processes that Procfile lines run in containers
// from outliving the overmind that started them.
//
// docker compose exec does not stop the process it started when its client
// goes away, so a Procfile line that overmind stops, restarts or loses, or
// whose tmux server dies, would leave its command running in the container
// with no owner. Two things prevent that:
//
//   - Exec runs each line with a second, non-TTY exec beside it, the
//     watchdog. Its stdin is a pipe from Exec, which closes however Exec
//     ends, even when it is killed. The watchdog forwards the signals Exec
//     gets to the command's process group, and stops the group once the
//     pipe closes.
//   - Every command is recorded under the run of the overmind that started
//     it, and SweepScript, which neptuno runs before it starts or restarts a
//     repo's processes, stops those recorded under any other run.
//
// Both only ever signal process groups recorded under procfile.ProcsDir
// whose leader is still the process that was recorded, never PID 1, the
// entrypoint or anything else in the container.
package procs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robertoplancarte/neptuno/internal/procfile"
)

// RunEnv names the variable that holds the ID of the overmind run a
// process belongs to. neptuno sets it in overmind's tmux session.
const RunEnv = "NEPTUNO_RUN"

// ProcEnv names the variable that tells the container which file under
// procfile.ProcsDir records a process.
const ProcEnv = "NEPTUNO_PROC"

// NewRunID returns a fresh ID for an overmind run.
func NewRunID() string {
	return time.Now().UTC().Format("20060102T150405") + "-" + randomHex(3)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// oursFunc defines neptuno_ours, which reads a file under ProcsDir into pid
// and succeeds when that process group still exists and is the one
// recorded: its leader is the recorded process, or has exited, in which
// case Linux does not reuse its PID while the group has members. It never
// matches PID 1.
const oursFunc = procfile.StartTimeFunc +
	`neptuno_ours() { read -r pid start < "$1" 2>/dev/null || return 1; ` +
	`case $pid in ''|*[!0-9]*|0|1) return 1;; esac; ` +
	`if [ -d /proc/$pid ]; then [ "$(neptuno_start $pid)" = "$start" ] || return 1; fi; ` +
	`kill -0 -$pid 2>/dev/null; }; `

// stopFunc defines neptuno_stop, which sends TERM to the process groups
// whose leader PIDs it is given, waits up to five seconds for them to exit,
// and sends KILL to what is left.
const stopFunc = `neptuno_stop() { for g in "$@"; do kill -TERM -$g 2>/dev/null; done; i=0; ` +
	`while [ $i -lt 50 ]; do left=; for g in "$@"; do kill -0 -$g 2>/dev/null && left="$left $g"; done; ` +
	`[ -z "$left" ] && return 0; sleep 0.1 2>/dev/null || sleep 1; i=$((i+1)); done; ` +
	`for g in $left; do kill -KILL -$g 2>/dev/null; done; }; `

// WatchScript is the watchdog's shell script. Each line on its stdin names
// a signal to forward to the process group recorded in NEPTUNO_PROC's
// file. When stdin closes, the Procfile line has ended: it waits a few
// seconds for a group that was sent a signal to exit, then stops it.
const WatchScript = oursFunc + stopFunc +
	`f="` + procfile.ProcsDir + `/$NEPTUNO_RUN/$NEPTUNO_PROC"; sent=; ` +
	`while read -r sig; do case $sig in INT|TERM|QUIT) neptuno_ours "$f" && kill -$sig -$pid 2>/dev/null && sent=1;; esac; done; ` +
	`if [ -n "$sent" ]; then i=0; while [ $i -lt 50 ] && neptuno_ours "$f"; do sleep 0.1 2>/dev/null || sleep 1; i=$((i+1)); done; fi; ` +
	`neptuno_ours "$f" && neptuno_stop $pid; ` +
	`rm -f "$f"; rmdir "` + procfile.ProcsDir + `/$NEPTUNO_RUN" 2>/dev/null; exit 0`

// SweepScript stops the processes recorded under any run but NEPTUNO_RUN,
// prints the name of each, one per line, and removes their records.
const SweepScript = oursFunc + stopFunc +
	`cd "` + procfile.ProcsDir + `" 2>/dev/null || exit 0; groups=; ` +
	`for f in */*; do [ -f "$f" ] || continue; [ "${f%%/*}" = "$NEPTUNO_RUN" ] && continue; ` +
	`if neptuno_ours "$f"; then groups="$groups $pid"; n=${f#*/}; echo "${n%.*}"; fi; rm -f "$f"; done; ` +
	`[ -n "$groups" ] && neptuno_stop $groups; ` +
	`for d in */; do [ "${d%/}" = "$NEPTUNO_RUN" ] || rmdir "$d" 2>/dev/null; done; exit 0`

// ParseSweep reads SweepScript's output: the names of the processes it
// stopped, each once, in order.
func ParseSweep(out string) []string {
	var names []string
	seen := map[string]bool{}
	for _, n := range strings.Fields(out) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names
}

// ExecOptions configures Exec.
type ExecOptions struct {
	// Service is the compose service to run in.
	Service string
	// Process is the Procfile line's name.
	Process string
	// Script is the shell script to run, from procfile.ContainerScript.
	Script string
	// Run is the overmind run the process belongs to.
	Run string
	// Docker is the docker binary, "docker" when empty.
	Docker string
	// Stdin, Stdout and Stderr are the Procfile line's terminal.
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Signals receives the signals to forward. Exec subscribes it to
	// SIGINT, SIGTERM, SIGQUIT and SIGHUP when nil.
	Signals chan os.Signal
	// WatchdogTimeout bounds how long Exec waits, after the command's exec
	// ends, for the watchdog to finish stopping what is left.
	WatchdogTimeout time.Duration
}

// Exec runs a Procfile line in its container and returns its exit status.
// It forwards SIGINT, SIGTERM and SIGQUIT, which overmind sends to stop the
// line, to the command's process group in the container, and a SIGHUP,
// which means the line's terminal went away, ends the line.
func Exec(opts ExecOptions) int {
	docker := opts.Docker
	if docker == "" {
		docker = "docker"
	}
	if opts.Run == "" {
		opts.Run = "manual"
	}
	if opts.WatchdogTimeout == 0 {
		opts.WatchdogTimeout = 15 * time.Second
	}
	env := []string{"-e", RunEnv + "=" + opts.Run, "-e", ProcEnv + "=" + opts.Process + "." + randomHex(4)}

	sigs := opts.Signals
	if sigs == nil {
		sigs = make(chan os.Signal, 4)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGHUP)
		defer signal.Stop(sigs)
	}

	// The watchdog gets its own process group, so the signals overmind
	// sends the line's group reach it only through the pipe, in order.
	watch := exec.Command(docker, append(append([]string{"compose", "exec", "-T"}, env...), opts.Service, "sh", "-c", WatchScript)...)
	watch.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	pipe, err := watch.StdinPipe()
	var watchDone chan struct{}
	if err == nil {
		if err = watch.Start(); err == nil {
			watchDone = make(chan struct{})
			go func() { _ = watch.Wait(); close(watchDone) }()
		}
	}
	if err != nil {
		fmt.Fprintf(opts.Stderr, "neptuno: could not start %s's watchdog: %v\n", opts.Process, err)
	}

	// oneShot runs a fresh watchdog with input as its whole stdin, for when
	// the first one is gone: it forwards the signals input names, then
	// stops the command's group as the first one would have.
	watchArgs := watch.Args[1:]
	oneShot := func(input string) {
		c := exec.Command(docker, watchArgs...)
		c.Stdin = strings.NewReader(input)
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		_ = c.Run()
	}
	watching := func() bool {
		if watchDone == nil {
			return false
		}
		select {
		case <-watchDone:
			return false
		default:
			return true
		}
	}
	var mu sync.Mutex
	closed, stopped := false, false
	send := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		if !closed && watching() {
			if _, err := io.WriteString(pipe, line+"\n"); err == nil {
				return
			}
		}
		go oneShot(line + "\n")
	}
	// end tells the watchdog the line has ended, or stops the command
	// with a fresh one.
	end := func() {
		mu.Lock()
		defer mu.Unlock()
		if closed || stopped {
			return
		}
		if watching() {
			closed = true
			pipe.Close()
			return
		}
		stopped = true
		oneShot("")
	}

	main := exec.Command(docker, append(append([]string{"compose", "exec"}, env...), opts.Service, "sh", "-c", opts.Script)...)
	main.Stdin, main.Stdout, main.Stderr = opts.Stdin, opts.Stdout, opts.Stderr
	if err := main.Start(); err != nil {
		fmt.Fprintf(opts.Stderr, "neptuno: %v\n", err)
		end()
		return 127
	}

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case s := <-sigs:
				switch s {
				case syscall.SIGINT:
					send("INT")
				case syscall.SIGTERM:
					send("TERM")
				case syscall.SIGQUIT:
					send("QUIT")
				case syscall.SIGHUP:
					go end()
				}
			}
		}
	}()

	err = main.Wait()
	end()
	if watchDone != nil {
		select {
		case <-watchDone:
		case <-time.After(opts.WatchdogTimeout):
		}
	}
	close(stop)

	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	return 1
}

// ProbeScript finds the process that holds a port or a PID in a container
// and the run that recorded it. It reads NEPTUNO_PORT, the TCP port a
// process could not listen on, and NEPTUNO_PID, the PID a server's pid file
// names, and prints key=value lines: pid, pgid, cmd, and run and proc when
// the process is recorded under procfile.ProcsDir, or gone=<pid> when the
// PID is not running. It prints nothing when the port is free.
const ProbeScript = `pid=$NEPTUNO_PID; ` +
	`if [ -z "$pid" ] && [ -n "$NEPTUNO_PORT" ]; then hex=$(printf '%04X' "$NEPTUNO_PORT"); ` +
	`for inode in $(cat /proc/net/tcp /proc/net/tcp6 2>/dev/null | while read -r sl local rem st rest; do [ "$st" = 0A ] || continue; case $local in *:$hex) set -- $rest; echo "$6";; esac; done); do ` +
	`for d in /proc/[0-9]*; do for fd in "$d"/fd/*; do if [ "$(readlink "$fd" 2>/dev/null)" = "socket:[$inode]" ]; then pid=${d#/proc/}; break 3; fi; done; done; done; fi; ` +
	`[ -n "$pid" ] || exit 0; ` +
	`if [ ! -d /proc/$pid ]; then echo "gone=$pid"; exit 0; fi; ` +
	`s=$(cat /proc/$pid/stat); s=${s##*) }; set -- $s; pgid=$3; ` +
	`echo "pid=$pid"; echo "pgid=$pgid"; echo "cmd=$(tr '\0' ' ' < /proc/$pid/cmdline | cut -c1-80)"; ` +
	`for f in ` + procfile.ProcsDir + `/*/*; do [ -f "$f" ] || continue; read -r p st < "$f"; ` +
	`if [ "$p" = "$pgid" ]; then r=${f%/*}; n=${f##*/}; echo "run=${r##*/}"; echo "proc=${n%.*}"; break; fi; done; exit 0`

// Holder is what ProbeScript found.
type Holder struct {
	PID, PGID int
	Cmd       string
	// Run and Proc are where the holder is recorded, if it is.
	Run, Proc string
	// Gone is true when the PID asked about is not running.
	Gone bool
}

// ParseProbe reads ProbeScript's output. ok is false when it found nothing.
func ParseProbe(out string) (h Holder, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		k, v, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		switch k {
		case "pid":
			h.PID, _ = strconv.Atoi(v)
		case "pgid":
			h.PGID, _ = strconv.Atoi(v)
		case "cmd":
			h.Cmd = strings.TrimSpace(v)
		case "run":
			h.Run = v
		case "proc":
			h.Proc = v
		case "gone":
			h.PID, _ = strconv.Atoi(v)
			h.Gone = true
		}
	}
	return h, h.PID > 0
}
