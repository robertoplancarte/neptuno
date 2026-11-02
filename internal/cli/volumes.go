package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newVolumesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volumes",
		Short: "List the project's volumes, or reset one to start it over empty",
		Long: "Lists the named volumes in compose.yml, the services that mount each, and\n" +
			"whether docker has created it yet. `neptuno volumes reset` deletes one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			vols, err := o.Volumes()
			if err != nil {
				return err
			}
			if len(vols) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "compose.yml declares no named volumes.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "VOLUME\tDOCKER NAME\tUSED BY\tSTATE")
			for _, v := range vols {
				state := "not created"
				if v.Exists {
					state = "created"
				}
				if v.External {
					state += ", external"
				}
				users := strings.Join(v.Services, ", ")
				if users == "" {
					users = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", v.Name, v.Docker, users, state)
			}
			return w.Flush()
		},
	}
	cmd.AddCommand(newVolumesResetCommand())
	return cmd
}

func newVolumesResetCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "reset <volume>...",
		Short: "Delete a volume's data and restart what uses it, which recreates it",
		Long: "Stops the services that mount the volume and removes their containers,\n" +
			"deletes the volume, and starts again those that were running, which\n" +
			"recreates it empty: postgres sets up a new database, and a Rails app's\n" +
			"container reinstalls its gems. Its data is gone for good, so this asks\n" +
			"first unless you pass --yes.",
		Example: "  neptuno volumes reset postgres_data\n" +
			"  neptuno volumes reset api_bundle --yes",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o, err := loadOrchestrator(cmd)
			if err != nil {
				return err
			}
			if !yes {
				if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
					return errors.New("no terminal to confirm on: pass --yes to delete the data")
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "Delete %s and all its data? [y/N] ", strings.Join(args, ", "))
				answer, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
					fmt.Fprintln(cmd.ErrOrStderr(), "Nothing deleted.")
					return nil
				}
			}
			for _, name := range args {
				if err := o.ResetVolume(name); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "delete without asking")
	return cmd
}
