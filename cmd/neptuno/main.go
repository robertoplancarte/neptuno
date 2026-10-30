// Command neptuno runs a multi-repo development environment on top of
// docker compose, overmind and tmux.
package main

import (
	"os"

	"github.com/robertoplancarte/neptuno/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
