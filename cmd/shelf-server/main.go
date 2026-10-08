// Command shelf-server stores scans from the shelf CLI and serves the
// catalog web UI and API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/store"
	"github.com/fenixstarlord/indexserver/internal/web"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func dataDir() string { return envOr("SHELF_DATA_DIR", "/data") }

func openStore() (*store.Store, error) {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	p := filepath.Join(dir, "shelf.db")
	st, err := store.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return st, nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "shelf-server",
		Short:        "Catalog server for shelf scans",
		Version:      version,
		SilenceUsage: true,
		RunE:         func(cmd *cobra.Command, args []string) error { return serve(cmd.Context()) },
	}
	root.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server (default)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return serve(cmd.Context()) },
	})
	root.AddCommand(newTokenCmd())
	root.AddCommand(newIngestCmd())
	return root
}

func serve(parent context.Context) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.Default()

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := store.CheckFTS5Trigram(ctx, st.DB); err != nil {
		return err
	}
	log.Info("database ready", "path", filepath.Join(dataDir(), "shelf.db"), "fts5_trigram", "ok")

	handler, err := web.New(ctx, st, web.Config{
		Password:      os.Getenv("SHELF_PASSWORD"),
		PasswordHash:  os.Getenv("SHELF_PASSWORD_HASH"),
		SessionSecret: os.Getenv("SHELF_SESSION_SECRET"),
		DataDir:       dataDir(),
		Version:       version,
	}, log)
	if err != nil {
		return err
	}

	listen := envOr("SHELF_LISTEN", ":8080")
	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown", "err", err)
		}
	}()
	log.Info("listening", "addr", listen, "data_dir", dataDir(), "version", version)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("stopped")
	return nil
}

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Manage API tokens for the shelf CLI"}
	cmd.AddCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "Create a token and print it once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			plain, id, err := st.CreateToken(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "token %d (%s) created. It is shown once; store it with:\n  shelf login <server-url> --token <token>\n", id, args[0])
			fmt.Fprintln(cmd.OutOrStdout(), plain)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			toks, err := st.ListTokens(cmd.Context())
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tCREATED\tLAST USED")
			for _, t := range toks {
				used := "never"
				if t.LastUsedAt != nil {
					used = t.LastUsedAt.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", t.ID, t.Name, t.CreatedAt.Local().Format("2006-01-02 15:04"), used)
			}
			return w.Flush()
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "revoke <id>",
		Short: "Delete a token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("token id must be a number: %w", err)
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			gone, err := st.RevokeToken(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !gone {
				return fmt.Errorf("no token with id %d", id)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "token %d revoked\n", id)
			return nil
		},
	})
	return cmd
}

func newIngestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ingest <file.shelf>",
		Short: "Import a bundle file directly into the database (no HTTP)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			fi, err := f.Stat()
			if err != nil {
				return err
			}
			b, err := bundle.Read(f, fi.Size())
			if err != nil {
				return err
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			start := time.Now()
			res, err := st.Ingest(cmd.Context(), b)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scan %d stored for drive %q: %d files, %d clips, +%d -%d ~%d (latest=%v) in %s\n",
				res.ScanID, res.DriveName, res.Files, res.Clips, res.Added, res.Removed, res.Changed, res.IsLatest,
				time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
}
