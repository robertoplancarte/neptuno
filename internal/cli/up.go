package cli

import (
	"errors"
	"fmt"
	"os"

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
			"With no arguments, shows a menu of the repos and services in compose.yml.\n" +
			"Containers start with docker compose; each repo's Procfile runs under\n" +
			"overmind in its own window of the project's tmux session, which Neptuno\n" +
			"then attaches to.",
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			extra, err := env.ParseAssignments(set)
			if err != nil {
				return err
			}

			selected, err := chooseServices(o.Project, args, all)
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

func chooseServices(p *project.Project, args []string, all bool) ([]string, error) {
	repos, services := p.Selectable()
	switch {
	case len(args) > 0:
		return args, nil
	case all:
		return append(repos, services...), nil
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
	return &cobra.Command{
		Use:   "down [service...]",
		Short: "Stop overmind and the containers",
		Long: "Stops the named repos' overmind and containers. With no arguments, stops\n" +
			"everything, closes the tmux session and runs docker compose down.",
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			return o.Down(args)
		},
	}
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
