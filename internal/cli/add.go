package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/robertoplancarte/neptuno/internal/add"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/stacks"
	"github.com/spf13/cobra"
)

func newAddCommand() *cobra.Command {
	var opts add.Options
	cmd := &cobra.Command{
		Use:   "add <repo> [git-url]",
		Short: "Add a repo: clone an existing one, or start a new one from a stack",
		Long: "Adds a repo to the project and wires it up: dockerfiles/<repo>/Dockerfile,\n" +
			"procfiles/<repo>/Procfile, environments/<repo>/base.env and local.env, a\n" +
			"service in compose.yml and an http://<repo>.localhost site in the Caddyfile.\n\n" +
			"With a git URL, the repo is cloned into repos/<repo> as a submodule. Without\n" +
			"one, a new repo is created there from the stack (blank by default) and git\n" +
			"initialized; stacks like rails run their app generator inside the new\n" +
			"container, so no local toolchain is needed.\n\n" +
			"Existing files are never overwritten, so add can be rerun after a failure.\n" +
			"Run `neptuno stacks` to list stacks; a project's stacks/<name>/ folders add\n" +
			"to and override the built-in ones.",
		Example: "  neptuno add api git@github.com:acme/api.git --stack rails\n" +
			"  neptuno add web --stack node\n" +
			"  neptuno add worker",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject()
			if err != nil {
				return err
			}
			opts.Name = args[0]
			if len(args) == 2 {
				opts.URL = args[1]
			}
			a := &add.Adder{
				Project: p,
				Runner:  orchestrate.ExecRunner{Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()},
				Log:     cmd.OutOrStdout(),
			}
			return a.Add(opts)
		},
	}
	cmd.Flags().StringVarP(&opts.Stack, "stack", "s", "", "stack template to use (default \""+stacks.Default+"\")")
	cmd.Flags().IntVarP(&opts.Port, "port", "p", 0, "dev server port, overriding the stack's")
	cmd.Flags().BoolVar(&opts.NoGenerate, "no-generate", false, "skip the stack's app generator")
	return cmd
}

func newStacksCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stacks",
		Short: "List the stacks `neptuno add --stack` can use",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := ""
			if p, err := loadProject(); err == nil {
				dir = p.Path("stacks")
			}
			list, err := stacks.List(dir)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "STACK\tPORT\tSOURCE\tDESCRIPTION")
			for _, s := range list {
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", s.Name, s.Port, s.Source, s.Description)
			}
			return w.Flush()
		},
	}
}
