package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/indexserver/internal/cliconfig"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/extract/registry"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Show extractors, whether their tools were found, and versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return err
			}
			runner := extract.NewRunner(cmd.Context(), registry.All("", nil, cfg.Tools))
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "EXTRACTOR\tSTATUS\tVERSION\tPRIORITY")
			for _, s := range runner.Statuses() {
				status := "missing"
				if s.Available {
					status = "ok"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\n", s.Name, status, s.Version, s.Priority)
			}
			w.Flush()
			p, _ := cliconfig.Path()
			fmt.Fprintf(cmd.OutOrStdout(), "\nconfig: %s\n", p)
			if cfg.Server != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "server: %s (token set: %v)\n", cfg.Server, cfg.Token != "")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "server: not logged in")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "tool paths can be set under [tools] in the config: ffprobe, art_cmd, art_cmd_args, redline")
			return nil
		},
	}
}
