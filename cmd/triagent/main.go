package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is overridden via -ldflags "-X main.version=..." in release builds.
// Local/dev builds report "dev".
var version = "dev"

func main() {
	// Intercept `triagent clear watches [flags...]` before the cobra dispatch
	// so the subcommand can parse its own flag set without cobra consuming flags.
	if len(os.Args) >= 3 && os.Args[1] == "clear" && os.Args[2] == "watches" {
		os.Exit(ClearWatches(os.Args[3:]))
	}

	// A bare `triagent` (no subcommand) opens the investigation UI.
	// Inject the default subcommand while leaving flags (--help, --version) untouched.
	if len(os.Args) == 1 {
		os.Args = append(os.Args, "start")
	}

	root := &cobra.Command{
		Use:     "triagent",
		Short:   "AI-assisted investigation agent.",
		Version: version,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	for _, c := range Commands() {
		root.AddCommand(c)
	}

	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
