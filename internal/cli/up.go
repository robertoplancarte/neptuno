package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/robertoplancarte/neptuno/internal/env"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/state"
	"github.com/robertoplancarte/neptuno/internal/tui"
	"github.com/spf13/cobra"
)

func newUpCommand() *cobra.Command {
	var (
		all, detach bool
		set         []string
	)
	cmd := &cobra.Command{
		Use:   "up [service...]",
		Short: "Pick services and start them with their dependencies",
		Long: "Starts the chosen repos and services plus everything they depend on.\n\n" +
			"With no arguments inside a repo's folder (such as repos/api or\n" +
			"dockerfiles/api), starts that repo. Elsewhere with no arguments, shows a\n" +
			"menu of the repos and services in compose.yml.\n" +
			"Containers start with docker compose; each repo's Procfile runs under\n" +
			"overmind with --any-can-die, so one process exiting leaves the others\n" +
			"running. Each repo gets a window in the project's tmux session showing\n" +
			"one overmind window per process, and Neptuno then attaches to it.",
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			extra, err := env.ParseAssignments(set)
			if err != nil {
				return err
			}

			selected, err := chooseServices(cmd, o.Project, args, all)
			if errors.Is(err, tui.ErrCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			return o.Up(orchestrate.UpOptions{Selected: selected, Set: extra, Detach: detach})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "start every repo and service without showing the menu")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "do not attach to the tmux session")
	cmd.Flags().StringArrayVar(&set, "set", nil, "set KEY=value for this run, overriding env files (repeatable)")
	return cmd
}

// chooseServices picks what `up` starts: the named services, everything
// with --all, the repo whose folder the user is in, or else the menu.
func chooseServices(cmd *cobra.Command, p *project.Project, args []string, all bool) ([]string, error) {
	repos, services := p.Selectable()
	switch {
	case len(args) > 0:
		return args, nil
	case all:
		return append(repos, services...), nil
	}
	if name := serviceFromFolder(cmd, p); name != "" {
		return []string{name}, nil
	}
	switch {
	case len(repos)+len(services) == 0:
		return nil, errors.New("compose.yml has no services to start")
	case !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()):
		return nil, errors.New("no terminal for the menu: name the services to start, or pass --all")
	}
	st, err := state.Load(p.Root)
	if err != nil {
		return nil, err
	}
	return tui.PickServices(tui.PickerOptions{
		Title:       fmt.Sprintf("Start which services in %s?", p.Name),
		Repos:       repos,
		Services:    services,
		Preselected: st.LastUp,
		Closure:     p.Closure,
	})
}

func newDownCommand() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "down [service...]",
		Short: "Stop overmind and the containers",
		Long: "Stops the named repos' overmind and containers. With no arguments inside a\n" +
			"repo's folder (such as repos/api or dockerfiles/api), stops that repo.\n" +
			"Otherwise stops everything, closes the tmux session and runs docker compose down.",
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			if len(args) == 0 && !all {
				if name := serviceFromFolder(cmd, o.Project); name != "" {
					args = []string{name}
				}
			}
			return o.Down(args)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "stop everything, even inside a repo's folder")
	return cmd
}

// serviceFromFolder returns the repo whose folder the working directory is
// in, and says so, so `neptuno up` in dockerfiles/api means `neptuno up api`.
func serviceFromFolder(cmd *cobra.Command, p *project.Project) string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	name := p.ServiceAt(wd)
	if name != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Using %s (from the current folder; pass names or --all to choose others)\n", name)
	}
	return name
}

func loadOrchestrator(cmd *cobra.Command) (*orchestrate.Orchestrator, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	p, err := project.Load(wd)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return &orchestrate.Orchestrator{
		Project: p,
		Runner:  orchestrate.ExecRunner{Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()},
		Log:     cmd.ErrOrStderr(),
		Neptuno: self,
	}, nil
}

func newRestartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restart [repo[:process]]",
		Short: "Restart a repo's processes, or one of them",
		Long: "Restarts the repo's processes through its running overmind, or only the\n" +
			"named one, as in `neptuno restart api:web`. With no arguments inside a\n" +
			"repo's folder, restarts that repo.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			var target string
			if len(args) == 1 {
				target = args[0]
			} else if target = serviceFromFolder(cmd, o.Project); target == "" {
				return errors.New("name a repo, such as `neptuno restart api` or `neptuno restart api:web`")
			}
			repo, process, _ := strings.Cut(target, ":")
			return o.Restart(repo, process)
		},
	}
}

// newPaneDiedCommand backs the pane-died hook in overmind's tmux config: it
// replaces the dead pane of a process that exited with a shell on the host.
func newPaneDiedCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "pane-died <repo> <tmux-socket> <pane> <process> <status>",
		Hidden: true,
		Args:   cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, socket, pane, process, status := args[0], args[1], args[2], args[3], args[4]
			return exec.Command("tmux", "-S", socket, "respawn-pane", "-k", "-t", pane,
				orchestrate.DeadPaneCommand(repo, process, status)).Run()
		},
	}
}
