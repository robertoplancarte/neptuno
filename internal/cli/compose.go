package cli

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/robertoplancarte/neptuno/internal/engine"
	"github.com/spf13/cobra"
)

// newComposeCommand runs compose with the machine's container engine, in
// the project folder, so the same command works with Docker and Podman.
func newComposeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compose [args...]",
		Short: "Run docker compose, or podman compose, in the project",
		Long: "Runs compose in the project folder with this machine's container engine:\n" +
			"`docker compose` where Docker is installed, else `podman compose`.\n" +
			"Set " + engine.EnvEngine + "=docker or podman to choose, and " + engine.EnvCompose + "\n" +
			"(such as podman-compose) to run another compose command.\n\n" +
			"Examples:\n" +
			"  neptuno compose ps\n" +
			"  neptuno compose logs --tail 100 api\n" +
			"  neptuno compose build api",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
				return cmd.Help()
			}
			p, err := loadProject()
			if err != nil {
				return err
			}
			e := engine.Current()
			argv := e.Argv(append([]string{engine.Docker, "compose"}, args...))
			if _, err := exec.LookPath(argv[0]); err != nil {
				return errors.New(strings.Join(e.Programs(), " and ") + " not found on PATH; install Docker or Podman, or set " + engine.EnvEngine)
			}
			run := exec.Command(argv[0], argv[1:]...)
			run.Dir, run.Stdin, run.Stdout, run.Stderr = p.Root, os.Stdin, os.Stdout, os.Stderr
			err = run.Run()
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				os.Exit(exit.ExitCode())
			}
			return err
		},
	}
	return cmd
}
