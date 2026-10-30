package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/robertoplancarte/neptuno/internal/env"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/state"
	"github.com/spf13/cobra"
)

func newEnvCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Switch and inspect environment configurations",
	}
	cmd.AddCommand(newEnvUseCommand(), newEnvShowCommand())
	return cmd
}

func newEnvUseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "use <config> | <repo>=<config>...",
		Short: "Set the active configuration for the project or single repos",
		Example: "  neptuno env use staging\n" +
			"  neptuno env use api=staging web=local",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject()
			if err != nil {
				return err
			}
			st, err := state.Load(p.Root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, arg := range args {
				repo, config, perRepo := strings.Cut(arg, "=")
				if !perRepo {
					st.Env, st.RepoEnv = arg, nil
					fmt.Fprintf(out, "Active configuration: %s\n", arg)
					warnUnknownConfig(cmd, p, "", arg)
					continue
				}
				if !p.IsRepo(repo) {
					return fmt.Errorf("unknown repo %q", repo)
				}
				if st.RepoEnv == nil {
					st.RepoEnv = map[string]string{}
				}
				st.RepoEnv[repo] = config
				fmt.Fprintf(out, "%s: %s\n", repo, config)
				warnUnknownConfig(cmd, p, repo, config)
			}
			if err := state.Save(p.Root, st); err != nil {
				return err
			}
			o := &orchestrate.Orchestrator{Project: p}
			if err := o.WriteEnvFiles(st, nil); err != nil {
				return err
			}
			fmt.Fprintln(out, "Run `neptuno up` to restart running repos with the new configuration.")
			return nil
		},
	}
}

// warnUnknownConfig warns when no env file exists for a configuration,
// looking in _shared and repo, or in every folder when repo is empty.
func warnUnknownConfig(cmd *cobra.Command, p *project.Project, repo, config string) {
	dirs := []string{"*"}
	if repo != "" {
		dirs = []string{env.SharedDir, repo}
	}
	for _, dir := range dirs {
		if matches, _ := filepath.Glob(filepath.Join(p.Path("environments"), dir, config+".env")); len(matches) > 0 {
			return
		}
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "warning: no %s.env file found; only base.env files will apply\n", config)
}

func newEnvShowCommand() *cobra.Command {
	var (
		config string
		set    []string
	)
	cmd := &cobra.Command{
		Use:   "show [repo]",
		Short: "Print a repo's merged variables and where each came from",
		Long:  "Prints a repo's merged variables. The repo defaults to the one whose folder you are in.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject()
			if err != nil {
				return err
			}
			var repo string
			if len(args) == 1 {
				repo = args[0]
			} else if repo = serviceFromFolder(cmd, p); repo == "" {
				return fmt.Errorf("name a repo, or run this inside one of its folders")
			}
			if !p.IsRepo(repo) {
				return fmt.Errorf("unknown repo %q", repo)
			}
			extra, err := env.ParseAssignments(set)
			if err != nil {
				return err
			}
			if config == "" {
				st, err := state.Load(p.Root)
				if err != nil {
					return err
				}
				config = st.EnvFor(repo, p.Config.DefaultEnv)
			}
			vars, err := env.Resolver{Dir: p.Path("environments")}.Resolve(repo, config, extra)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "# %s, configuration %s\n", repo, config)
			width := 0
			for _, v := range vars {
				width = max(width, len(v.Key)+len(v.Value)+1)
			}
			for _, v := range vars {
				fmt.Fprintf(out, "%-*s  # %s\n", width, v.Key+"="+v.Value, v.Source)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&config, "config", "", "configuration to resolve instead of the active one")
	cmd.Flags().StringArrayVar(&set, "set", nil, "add KEY=value on top, as neptuno up --set would")
	return cmd
}

// newHostExecCommand backs host-mode Procfiles: overmind runs it with the
// project root as working directory, and it loads the repo's resolved env,
// changes into repos/<repo> and replaces itself with the command.
func newHostExecCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "host-exec <repo> <command>",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject()
			if err != nil {
				return err
			}
			repo, command := args[0], args[1]
			vars, err := env.ParseFile(orchestrate.EnvFile(p, repo))
			if err != nil {
				return err
			}
			environ := map[string]string{}
			for _, kv := range os.Environ() {
				k, v, _ := strings.Cut(kv, "=")
				environ[k] = v
			}
			for _, kv := range vars {
				environ[kv[0]] = kv[1]
			}
			list := make([]string, 0, len(environ))
			for k, v := range environ {
				list = append(list, k+"="+v)
			}
			sort.Strings(list)

			if err := os.Chdir(p.Path("repos", repo)); err != nil {
				return err
			}
			return syscall.Exec("/bin/sh", []string{"sh", "-c", command}, list)
		},
	}
}

func loadProject() (*project.Project, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return project.Load(wd)
}
