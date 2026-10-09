package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/fenixstarlord/indexserver/internal/cliconfig"
	"github.com/fenixstarlord/indexserver/internal/client"
)

// loadClient builds an API client from the saved config.
func loadClient() (*client.Client, error) {
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, err
	}
	if cfg.Server == "" || cfg.Token == "" {
		return nil, errors.New("not logged in: run `shelf login <server-url>` first")
	}
	return client.New(cfg.Server, cfg.Token)
}

func newLoginCmd() *cobra.Command {
	var token string
	cmd := &cobra.Command{
		Use:   "login <connection-key | server-url>",
		Short: "Save the server address and an API key",
		Long: `Store the server URL and an API key in ~/.config/shelf/config.toml.

Create a key on the server's API keys page. It looks like
  shelf://shelf_abc123@100.64.0.5:8080
and carries the server address, so this is enough:
  shelf login 'shelf://shelf_abc123@100.64.0.5:8080'

A plain server URL also works; the key is then read from --token, or
prompted for without echo.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			server, embedded, err := client.ParseConnection(args[0])
			if err != nil {
				return err
			}
			if token == "" {
				token = embedded
			}
			if token == "" {
				token, err = promptSecret(cmd, "API key: ")
				if err != nil {
					return err
				}
			}
			token = strings.TrimSpace(token)
			if token == "" {
				return errors.New("an API key is required")
			}
			c, err := client.New(server, token)
			if err != nil {
				return err
			}
			me, err := c.Me(cmd.Context())
			if err != nil {
				return fmt.Errorf("could not verify the key with %s: %w", c.Server, err)
			}
			cfg, err := cliconfig.Load()
			if err != nil {
				return err
			}
			cfg.Server, cfg.Token = c.Server, token
			if err := cliconfig.Save(cfg); err != nil {
				return err
			}
			p, _ := cliconfig.Path()
			fmt.Fprintf(cmd.OutOrStdout(), "logged in to %s as key %q (server %s); saved to %s\n", c.Server, me.TokenName, me.Version, p)
			return nil
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "API key, if not part of the first argument (prompted if omitted)")
	return cmd
}

func promptSecret(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		return string(b), err
	}
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newUploadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upload <file.shelf>",
		Short: "Push a previously written bundle to the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			return uploadBundle(cmd, c, args[0])
		},
	}
}

func uploadBundle(cmd *cobra.Command, c *client.Client, path string) error {
	start := time.Now()
	fmt.Fprintf(cmd.ErrOrStderr(), "uploading %s to %s\n", path, c.Server)
	res, err := c.Upload(cmd.Context(), path)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "scan %d stored for drive %q: %d files, %d clips, +%d -%d ~%d (latest=%v) in %s\n",
		res.ScanID, res.DriveName, res.Files, res.Clips, res.Added, res.Removed, res.Changed, res.IsLatest,
		time.Since(start).Round(time.Millisecond))
	return nil
}

func newDrivesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "drives",
		Short: "List drives known to the server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			drives, err := c.Drives(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(drives)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tLABEL\tLOCATION\tCAPACITY\tFREE\tFILES\tCLIPS\tSCANS\tLAST SCAN")
			for _, d := range drives {
				last := ""
				if !d.LastScannedAt.IsZero() {
					last = d.LastScannedAt.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
					d.ID, d.Name, d.Label, d.Location, humanBytes(d.CapacityBytes), humanBytes(d.FreeBytes),
					d.FileCount, d.ClipCount, d.ScanCount, last)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the drives as JSON")
	return cmd
}

func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// withTimeout is a small helper for commands that should not hang forever.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}
