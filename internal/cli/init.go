package cli

import (
	"fmt"
	"os/exec"

	"github.com/robertoplancarte/neptuno/internal/scaffold"
	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	var noGit bool
	cmd := &cobra.Command{
		Use:   "init <name>",
		Short: "Create a new environment with the default layout and services",
		Long: "Creates <name>/ with the environments, dockerfiles, procfiles and repos folders,\n" +
			"a neptuno.yml, and a compose.yml and Caddyfile wired up with Caddy,\n" +
			"PostgreSQL 17, Valkey 8 and SeaweedFS. The new folder is a git repository\n" +
			"so repos can be added as submodules.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			created, err := scaffold.Init(scaffold.Options{Name: name})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Created %s/\n", name)
			for _, path := range created {
				fmt.Fprintf(out, "  %s\n", path)
			}

			if !noGit {
				if err := gitInit(name); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: git init failed: %v\n", err)
				} else {
					fmt.Fprintln(out, "Initialized a git repository.")
				}
			}

			fmt.Fprintf(out, "\nNext:\n  cd %s\n  neptuno add <repo> <git-url>\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noGit, "no-git", false, "do not run git init in the new folder")
	return cmd
}

func gitInit(dir string) error {
	git := exec.Command("git", "init", "--quiet")
	git.Dir = dir
	if out, err := git.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
