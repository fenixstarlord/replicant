package main

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/indexserver/internal/cliconfig"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/extract/registry"
)

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Show extractors, whether their tools were found, and versions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cliconfig.Load()
			if err != nil {
				return err
			}
			runner := extract.NewRunner(cmd.Context(), registry.All("", nil, cfg.Tools))
			if asJSON {
				p, _ := cliconfig.Path()
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"extractors": runner.Statuses(), "config": p, "server": cfg.Server, "logged_in": cfg.Token != "",
					"docs": "https://github.com/fenixstarlord/indexserver/blob/main/docs/tools.md",
				})
			}
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
			fmt.Fprintln(cmd.OutOrStdout(), "where to get missing tools: docs/tools.md")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}
