package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
	"github.com/robertoplancarte/neptuno/internal/env"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/tui"
	"github.com/spf13/cobra"
)

func newRebuildCommand() *cobra.Command {
	var (
		all, detach, noCache, pull bool
		set                        []string
	)
	cmd := &cobra.Command{
		Use:   "rebuild [service...]",
		Short: "Rebuild services' images and recreate their containers",
		Long: "Builds the named services' images again with compose build, as after a\n" +
			"change to a Dockerfile, then recreates their containers from the new\n" +
			"images and restarts their processes. Their dependencies start as with\n" +
			"`neptuno up`, but are not rebuilt.\n\n" +
			"With no arguments inside a repo's folder (such as repos/api or\n" +
			"dockerfiles/api), rebuilds that repo. Elsewhere with no arguments, shows a\n" +
			"menu of the services compose.yml builds. Works with Docker and Podman.",
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			extra, err := env.ParseAssignments(set)
			if err != nil {
				return err
			}
			selected, err := chooseRebuild(cmd, o.Project, args, all)
			if errors.Is(err, tui.ErrCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			return o.Up(orchestrate.UpOptions{
				Selected: selected, Set: extra, Detach: detach,
				Rebuild: true, NoCache: noCache, Pull: pull,
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "rebuild every service compose.yml builds, without the menu")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "do not attach to the tmux session")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "build without the build cache")
	cmd.Flags().BoolVar(&pull, "pull", false, "pull newer base images while building")
	cmd.Flags().StringArrayVar(&set, "set", nil, "set KEY=value for this run, overriding env files (repeatable)")
	return cmd
}

// buildable splits the services compose.yml builds into repos and others.
func buildable(p *project.Project) (repos, services []string) {
	r, s := p.Selectable()
	for _, name := range r {
		if p.Buildable(name) {
			repos = append(repos, name)
		}
	}
	for _, name := range s {
		if p.Buildable(name) {
			services = append(services, name)
		}
	}
	return repos, services
}

// chooseRebuild picks what `rebuild` rebuilds: the named services, every
// buildable one with --all, the repo whose folder the user is in, or else
// a menu of the buildable ones.
func chooseRebuild(cmd *cobra.Command, p *project.Project, args []string, all bool) ([]string, error) {
	repos, services := buildable(p)
	switch {
	case len(args) > 0:
		return args, nil
	case all:
		if len(repos)+len(services) == 0 {
			return nil, errors.New("compose.yml builds no images: no service has a build section")
		}
		return append(repos, services...), nil
	}
	if name := serviceFromFolder(cmd, p); name != "" {
		return []string{name}, nil
	}
	switch {
	case len(repos)+len(services) == 0:
		return nil, errors.New("compose.yml builds no images: no service has a build section")
	case !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()):
		return nil, errors.New("no terminal for the menu: name the services to rebuild, or pass --all")
	}
	return tui.PickServices(tui.PickerOptions{
		Title:    fmt.Sprintf("Rebuild which services in %s?", p.Name),
		Repos:    repos,
		Services: services,
		Closure:  p.Closure,
	})
}
