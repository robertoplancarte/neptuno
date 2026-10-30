package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-isatty"

	"github.com/robertoplancarte/neptuno/internal/add"
	"github.com/robertoplancarte/neptuno/internal/orchestrate"
	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/scaffold"
	"github.com/robertoplancarte/neptuno/internal/stacks"
	"github.com/robertoplancarte/neptuno/internal/tui"
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
			"  neptuno add shop --stack rails --with action_text,storage_s3\n" +
			"  neptuno add shop --wildcard     # route *.shop.localhost to shop too\n" +
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
			if err := pickOptions(cmd, p, &opts); err != nil {
				return err
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
	cmd.Flags().StringSliceVarP(&opts.With, "with", "w", nil, "stack options to turn on, such as --with action_text,storage_s3")
	cmd.Flags().StringSliceVar(&opts.Without, "without", nil, "stack options to turn off, such as --without tailwind")
	cmd.Flags().BoolVar(&opts.Wildcard, "wildcard", false, "also route *.<repo>.localhost to the repo, such as one subdomain per tenant; rerun on an existing repo to turn it on")
	return cmd
}

// pickOptions shows a checklist of the stack's options when it has some,
// neither --with nor --without was given, and the terminal is interactive.
func pickOptions(cmd *cobra.Command, p *project.Project, opts *add.Options) error {
	if entries, _ := os.ReadDir(p.Path("repos", opts.Name)); len(entries) > 0 {
		return nil // existing repo: options only shape new apps
	}
	if cmd.Flags().Changed("with") || cmd.Flags().Changed("without") ||
		!isatty.IsTerminal(os.Stdin.Fd()) || !isatty.IsTerminal(os.Stdout.Fd()) {
		return nil
	}
	name := opts.Stack
	if name == "" {
		name = stacks.Default
	}
	s, err := stacks.Get(p.Path("stacks"), name)
	if err != nil || len(s.Options) == 0 {
		return err
	}
	var names, defaults []string
	descriptions := map[string]string{}
	for _, o := range s.Options {
		names = append(names, o.Name)
		descriptions[o.Name] = o.Description
		if o.Default {
			defaults = append(defaults, o.Name)
		}
	}
	chosen, err := tui.PickOptions(fmt.Sprintf("Options for %s (%s stack)", opts.Name, s.Name), names, descriptions, defaults)
	if err != nil {
		return err
	}
	on := map[string]bool{}
	for _, c := range chosen {
		on[c] = true
	}
	for _, n := range names {
		if on[n] {
			opts.With = append(opts.With, n)
		} else {
			opts.Without = append(opts.Without, n)
		}
	}
	return nil
}

func newStacksCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stacks",
		Short: "List the stacks `neptuno add --stack` can use",
		Long: "Lists built-in stacks and the project's own stacks/<name>/ folders, which\n" +
			"add to and override the built-ins. A stack.yml with `extends: <name>` starts\n" +
			"from that stack and only needs the settings and files it changes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, root := "", ""
			if p, err := loadProject(); err == nil {
				dir, root = p.Path("stacks"), p.Root
			}
			list, err := stacks.List(dir)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "STACK\tPORT\tSOURCE\tDESCRIPTION")
			for _, s := range list {
				source := s.Source
				if rel, err := filepath.Rel(root, source); err == nil && root != "" && source != stacks.BuiltIn {
					source = filepath.ToSlash(rel)
				}
				if s.Extends != "" {
					source += " (extends " + s.Extends + ")"
				}
				desc := s.Description
				if len(s.Options) > 0 {
					desc += " (options: " + strings.Join(s.OptionNames(), ", ") + ")"
				}
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", s.Name, s.Port, source, desc)
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(newStacksCopyCommand())
	return cmd
}

func newStacksCopyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "copy <stack> [new-name]",
		Short: "Copy a stack into stacks/ to customize it",
		Long: "Writes every file of a stack, merged with any stack it extends, into\n" +
			"stacks/<new-name>/ (default: the same name, which overrides the original).\n" +
			"For small changes, a stacks/<name>/stack.yml with `extends:` and only the\n" +
			"files you change is usually enough.",
		Example: "  neptuno stacks copy rails             # override the built-in rails stack\n" +
			"  neptuno stacks copy rails rails-api   # a new stack based on rails",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := loadProject()
			if err != nil {
				return err
			}
			s, err := stacks.Get(p.Path("stacks"), args[0])
			if err != nil {
				return err
			}
			target := args[0]
			if len(args) == 2 {
				target = args[1]
			}
			if err := scaffold.ValidateName(target); err != nil {
				return err
			}
			files, err := s.Copy(p.Path("stacks", target))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Copied the %s stack (%s) to stacks/%s/\n", s.Name, s.Source, target)
			for _, f := range files {
				fmt.Fprintf(out, "  %s\n", f)
			}
			return nil
		},
	}
}
