package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mattn/go-isatty"

	"github.com/robertoplancarte/neptuno/internal/project"
	"github.com/robertoplancarte/neptuno/internal/scaffold"
	"github.com/robertoplancarte/neptuno/internal/tui"
	"github.com/spf13/cobra"
)

// pickServices shows a checklist of services; tests replace it.
var pickServices = tui.PickChecklist

func newInitCommand() *cobra.Command {
	var noGit bool
	var services []string
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Create a new environment, or add default services to this one",
		Long: "Creates <name>/ with the environments, dockerfiles, procfiles and repos folders,\n" +
			"a neptuno.yml, and a compose.yml and Caddyfile wired up with Caddy and the\n" +
			"default services you choose: " + strings.Join(scaffold.DefaultServiceNames(), ", ") + ".\n" +
			"The new folder is a git repository so repos can be added as submodules.\n\n" +
			"In a terminal, init shows a checklist of the default services, all checked.\n" +
			"Pass --services to choose without it, as in scripts; services that a chosen\n" +
			"one needs are added too.\n\n" +
			"Run in an existing project, with no name or with the project's folder, init\n" +
			"offers the default services the project doesn't have yet and adds the ones\n" +
			"you pick to compose.yml, the Caddyfile and environments/_shared/base.env.\n" +
			"Services already there, and everything else in those files, are left alone.",
		Example: "  neptuno init acme                           # pick services from a checklist\n" +
			"  neptuno init acme --services postgres,valkey\n" +
			"  neptuno init acme --services=               # only the proxy\n" +
			"  neptuno init                                # in a project: add services it lacks\n" +
			"  neptuno init --services svix                # in a project: add svix (and what it needs)",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chosen := services
			if !cmd.Flags().Changed("services") {
				chosen = nil
			}
			interactive := chosen == nil &&
				isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())

			root, err := existingProject(args)
			if err != nil {
				return err
			}
			if root != "" {
				return addServices(cmd, root, chosen, interactive)
			}
			return createProject(cmd, args[0], chosen, interactive, noGit)
		},
	}
	cmd.Flags().BoolVar(&noGit, "no-git", false, "do not run git init in the new folder")
	cmd.Flags().StringSliceVar(&services, "services", nil,
		"default services to set up, skipping the checklist: any of "+strings.Join(scaffold.DefaultServiceNames(), ", ")+
			" (--services= for none)")
	return cmd
}

// existingProject returns the root of the project init should add services
// to, or "" when it should create a new one.
func existingProject(args []string) (string, error) {
	if len(args) == 0 {
		root, err := project.Find(".")
		if errors.Is(err, project.ErrNotFound) {
			return "", errors.New("name the new project, as in `neptuno init acme`, or run init inside a project to add services to it")
		}
		return root, err
	}
	dir, err := filepath.Abs(args[0])
	if err != nil {
		return "", err
	}
	if root, err := project.Find(dir); err == nil && root == dir {
		if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err == nil {
			return dir, nil
		}
	}
	return "", nil
}

func createProject(cmd *cobra.Command, name string, chosen []string, interactive, noGit bool) error {
	if err := scaffold.ValidateName(name); err != nil {
		return err
	}
	if interactive {
		picked, err := pickDefaultServices(fmt.Sprintf("Services for %s", name), scaffold.DefaultServiceNames(), scaffold.DefaultServiceNames(), nil, "create")
		if err != nil {
			return err
		}
		chosen = picked
	}
	if chosen == nil {
		chosen = scaffold.DefaultServiceNames()
	}
	out := cmd.OutOrStdout()
	resolved, required, err := scaffold.ResolveServices(chosen)
	if err != nil {
		return err
	}
	noteRequired(cmd, required)

	created, err := scaffold.Init(scaffold.Options{Name: name, Services: chosen})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Created %s/ with %s\n", name, serviceList(resolved))
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
}

func addServices(cmd *cobra.Command, root string, chosen []string, interactive bool) error {
	out := cmd.OutOrStdout()
	name := filepath.Base(root)
	have, err := scaffold.ExistingServices(root)
	if err != nil {
		return err
	}
	var missing []string
	for _, s := range scaffold.DefaultServiceNames() {
		if !slices.Contains(have, s) {
			missing = append(missing, s)
		}
	}

	if chosen == nil {
		if len(missing) == 0 {
			fmt.Fprintf(out, "%s already has every default service: %s\n", name, strings.Join(have, ", "))
			return nil
		}
		if !interactive {
			return fmt.Errorf("%s is already a Neptuno project; pass --services to add default services to it (it lacks %s)", name, strings.Join(missing, ", "))
		}
		title := fmt.Sprintf("Add services to %s", name)
		if len(have) > 0 {
			title += " (it has " + strings.Join(have, ", ") + ")"
		}
		chosen, err = pickDefaultServices(title, missing, nil, have, "add")
		if err != nil {
			return err
		}
		if len(chosen) == 0 {
			fmt.Fprintln(out, "Nothing to add.")
			return nil
		}
	}

	_, required, err := scaffold.ResolveServices(chosen)
	if err != nil {
		return err
	}
	var newlyRequired []string
	for _, r := range required {
		if !slices.Contains(have, r) {
			newlyRequired = append(newlyRequired, r)
		}
	}
	noteRequired(cmd, newlyRequired)

	log, err := scaffold.AddServices(root, chosen)
	added := false
	fmt.Fprintf(out, "Updated %s/\n", name)
	for _, l := range log {
		fmt.Fprintf(out, "  %s\n", l)
		added = added || strings.HasPrefix(l, "added   compose.yml")
	}
	if err != nil {
		return err
	}
	if added {
		fmt.Fprintln(out, "\nStart them with `neptuno up -d` or `docker compose up -d`.")
	}
	return nil
}

// pickDefaultServices shows a checklist of names with their descriptions.
// The preview of what else gets added leaves out the ones in have.
func pickDefaultServices(title string, names, preselected, have []string, action string) ([]string, error) {
	descriptions := map[string]string{}
	for _, s := range scaffold.DefaultServices {
		descriptions[s.Name] = s.Description
	}
	return pickServices(tui.ChecklistOptions{
		Title:        title,
		Header:       "Services",
		Names:        names,
		Descriptions: descriptions,
		Preselected:  preselected,
		Action:       action,
		DepsLabel:    "Also adds, since a chosen service needs them: ",
		Closure: func(sel []string) ([]string, error) {
			resolved, _, err := scaffold.ResolveServices(sel)
			var out []string
			for _, s := range resolved {
				if !slices.Contains(have, s.Name) {
					out = append(out, s.Name)
				}
			}
			return out, err
		},
	})
}

func noteRequired(cmd *cobra.Command, required []string) {
	if len(required) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Adding %s too, since a chosen service needs it.\n", strings.Join(required, ", "))
	}
}

func serviceList(services []scaffold.Service) string {
	names := []string{scaffold.Proxy.Name}
	for _, s := range services {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

func gitInit(dir string) error {
	git := exec.Command("git", "init", "--quiet")
	git.Dir = dir
	if out, err := git.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}
