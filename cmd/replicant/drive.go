package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/replicant/internal/scan"
	"github.com/fenixstarlord/replicant/internal/store"
)

// driveInfo is how the catalog knows the drive mounted at a path.
type driveInfo struct {
	Path       string `json:"path"`
	Key        string `json:"key"`         // the drive's catalog key (volume UUID, or a name-based fallback)
	VolumeName string `json:"volume_name"` // the name the volume reports
	Known      bool   `json:"known"`       // the server already has this drive
	ID         int64  `json:"id,omitempty"`
	Name       string `json:"name,omitempty"` // catalog name, when known
	Renamed    bool   `json:"renamed,omitempty"`
	Error      string `json:"error,omitempty"` // why the server could not be asked
}

// lookup fills in the catalog fields from the server's drive list.
func (d *driveInfo) lookup(drives []store.Drive) {
	for _, dr := range drives {
		if dr.VolumeUUID == d.Key {
			d.Known, d.ID, d.Name, d.Renamed = true, dr.ID, dr.Name, dr.Renamed
			return
		}
	}
}

func newDriveCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "drive <path>",
		Short: "Show how the catalog knows the drive at a path",
		Long: `Identify the volume at path the way a scan does, without reading any file,
and look it up on the server: its catalog name, or that it is new. The macOS app
uses --json to suggest a name before a drive's first scan. With --json the
result is printed even when the server cannot be reached ("error" says why).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			abs, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			vol, err := scan.VolumeInfo(abs)
			if err != nil {
				return err
			}
			info := driveInfo{Path: abs, Key: vol.Key(), VolumeName: vol.Name}
			if info.VolumeName == "" {
				info.VolumeName = vol.MountPoint
			}
			if c, err := loadClient(); err != nil {
				info.Error = err.Error()
			} else {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				defer cancel()
				if drives, err := c.Drives(ctx); err != nil {
					info.Error = err.Error()
				} else {
					info.lookup(drives)
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(info)
			}
			fmt.Fprintf(out, "volume   %s (%s)\n", info.VolumeName, info.Key)
			switch {
			case info.Error != "":
				return fmt.Errorf("could not ask the server: %s", info.Error)
			case info.Known && info.Renamed:
				fmt.Fprintf(out, "catalog  %s (drive #%d, renamed)\n", info.Name, info.ID)
			case info.Known:
				fmt.Fprintf(out, "catalog  %s (drive #%d)\n", info.Name, info.ID)
			default:
				fmt.Fprintln(out, "catalog  not scanned yet")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the result as JSON")
	return cmd
}
