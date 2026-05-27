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
	root := &cobra.Command{
		Use:     "triagent-mcp",
		Short:   "MCP server multiplexer for triagent.",
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
