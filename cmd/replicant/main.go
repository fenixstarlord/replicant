// Command replicant indexes a mounted drive of camera media and sends the
// result to a replicant-server, or writes it to a bundle file.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/replicant/internal/scanner"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// Exit codes. The macOS app tells a cancelled scan and a drive pulled out
// mid-scan apart from a failure by them.
const (
	exitFailed    = 1
	exitDriveGone = 3   // the scanned drive or folder went away during the scan
	exitCancelled = 130 // interrupted (SIGINT or SIGTERM), as shells report it
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(exitCode(err))
	}
}

func exitCode(err error) int {
	switch {
	case errors.Is(err, scanner.ErrRootGone):
		return exitDriveGone
	case errors.Is(err, context.Canceled):
		return exitCancelled
	}
	return exitFailed
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "replicant",
		Short:         "Catalog offline drives of camera media",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(newDumpCmd())
	root.AddCommand(newScanCmd())
	root.AddCommand(newLoginCmd())
	root.AddCommand(newUploadCmd())
	root.AddCommand(newDrivesCmd())
	root.AddCommand(newDriveCmd())
	root.AddCommand(newDoctorCmd())
	return root
}
