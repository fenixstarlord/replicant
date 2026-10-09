// Command replicant-server stores scans from the replicant CLI and serves the
// catalog web UI and API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/replicant/internal/bundle"
	"github.com/fenixstarlord/replicant/internal/client"
	"github.com/fenixstarlord/replicant/internal/sched"
	"github.com/fenixstarlord/replicant/internal/store"
	"github.com/fenixstarlord/replicant/internal/web"
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

func dataDir() string { return envOr("REPLICANT_DATA_DIR", "/data") }

func openStore() (*store.Store, error) {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	if err := checkWritable(dir); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "replicant.db")
	st, err := store.Open(p)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	return st, nil
}

// checkWritable turns SQLite's "unable to open database file (14)" into a
// message that says what to do: the usual cause in Docker is a bind-mounted
// data folder owned by another user.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err == nil {
		f.Close()
		os.Remove(f.Name())
		return nil
	}
	return fmt.Errorf("the data directory %s is not writable by this process (uid %d, gid %d): %w\n"+
		"On the host, give the folder to that user, e.g. `chown -R %d:%d <host path mounted at %s>`, "+
		"or run the container as the folder's owner (compose: user: \"<uid>:<gid>\")",
		dir, os.Getuid(), os.Getgid(), err, os.Getuid(), os.Getgid(), dir)
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "replicant-server",
		Short:        "Catalog server for replicant scans",
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
	root.AddCommand(newBackupCmd())
	root.AddCommand(newHealthzCmd())
	return root
}

// exitWithParent cancels the context once the parent process has exited
// (we get re-parented to launchd/init, so the ppid changes).
func exitWithParent(parent context.Context, log *slog.Logger) context.Context {
	ctx, cancel := context.WithCancel(parent)
	ppid := os.Getppid()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if os.Getppid() != ppid {
					log.Info("parent process exited; stopping")
					cancel()
					return
				}
			}
		}
	}()
	return ctx
}

// loopback reports whether a listen address binds only to this machine.
func loopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// newHealthzCmd is the container HEALTHCHECK: GET /healthz on the listen
// port, exit 0 when the server answers.
func newHealthzCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "healthz",
		Short:  "Exit 0 if the server on REPLICANT_LISTEN answers",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, port, err := net.SplitHostPort(envOr("REPLICANT_LISTEN", ":8080"))
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 4*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/healthz", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("healthz: HTTP %d", resp.StatusCode)
			}
			return nil
		},
	}
}

func serve(parent context.Context) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.Default()
	if os.Getenv("REPLICANT_EXIT_WITH_PARENT") == "1" {
		// Started by the standalone Mac app: stop when it is gone, even if it
		// crashed or was killed without a chance to stop us.
		ctx = exitWithParent(ctx, log)
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := store.CheckFTS5Trigram(ctx, st.DB); err != nil {
		return err
	}
	log.Info("database ready", "path", filepath.Join(dataDir(), "replicant.db"), "fts5_trigram", "ok")

	scheduler := sched.New(st, log, version)
	scheduler.Start(ctx)

	listen := envOr("REPLICANT_LISTEN", ":8080")
	open := os.Getenv("REPLICANT_AUTH") == "open"
	if open && !loopback(listen) {
		return fmt.Errorf("REPLICANT_AUTH=open needs a loopback listen address (127.0.0.1:port or [::1]:port), got %q", listen)
	}
	if open {
		log.Warn("authentication is off: anyone who can reach this address can read and change the catalog", "listen", listen)
	}
	handler, err := web.New(ctx, st, web.Config{
		Password:      os.Getenv("REPLICANT_PASSWORD"),
		PasswordHash:  os.Getenv("REPLICANT_PASSWORD_HASH"),
		SessionSecret: os.Getenv("REPLICANT_SESSION_SECRET"),
		DataDir:       dataDir(),
		Version:       version,
		Sched:         scheduler,
		Listen:        listen,
		PublicURL:     os.Getenv("REPLICANT_PUBLIC_URL"),
		Open:          open,
	}, log)
	if err != nil {
		return err
	}

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
	cmd := &cobra.Command{Use: "token", Short: "Manage API tokens for the replicant CLI"}
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
			addr, src := web.PublicURL(cmd.Context(), st, os.Getenv("REPLICANT_PUBLIC_URL"), envOr("REPLICANT_LISTEN", ":8080"))
			if addr == "" {
				h, _ := os.Hostname()
				addr, src = "http://"+h+":"+strings.TrimPrefix(envOr("REPLICANT_LISTEN", ":8080"), ":"), "hostname"
			}
			conn, err := client.ConnectionString(addr, plain)
			if err != nil {
				conn = plain
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "key %d (%s) created. It is shown once. On the Mac, paste it into Replicant's Settings or run:\n  replicant login '<key>'\nServer address %s (%s); set REPLICANT_PUBLIC_URL or Settings → Server address to change it.\n", id, args[0], addr, src)
			fmt.Fprintln(cmd.OutOrStdout(), conn)
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
		Use:   "ingest <file.replicant>",
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
			res, err := st.IngestFrom(cmd.Context(), b, "file", 0)
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

func newBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup <path>",
		Short: "Write a consistent copy of the database (SQLite VACUUM INTO)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			p := args[0]
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				p = filepath.Join(p, store.BackupName(time.Now()))
			}
			if err := st.Backup(cmd.Context(), p); err != nil {
				return err
			}
			fi, _ := os.Stat(p)
			fmt.Fprintf(cmd.OutOrStdout(), "backup written: %s (%d bytes)\n", p, fi.Size())
			return nil
		},
	}
}
