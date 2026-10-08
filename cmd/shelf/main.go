// Command shelf indexes a mounted drive of camera media and sends the
// result to a shelf-server, or writes it to a bundle file.
package main

import (
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "shelf",
		Short:         "Catalog offline drives of camera media",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newDumpCmd())
	return root
}
