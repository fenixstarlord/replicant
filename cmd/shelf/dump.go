package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/indexserver/internal/scan"
)

func newDumpCmd() *cobra.Command {
	var (
		fullHash      bool
		noFingerprint bool
		descend       bool
		summaryOnly   bool
		skip          []string
		workers       int
	)
	cmd := &cobra.Command{
		Use:   "dump <path>",
		Short: "Print what would be indexed as JSON lines (for debugging)",
		Long: `Walk a mounted volume read-only and print one JSON object per entry to
stdout. Progress and a summary go to stderr. Nothing is sent anywhere.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			stderr := cmd.ErrOrStderr()

			opts := scan.DefaultOptions()
			opts.Fingerprint = !noFingerprint
			opts.FullHash = fullHash
			opts.DescendPackages = descend
			opts.Skip = append(opts.Skip, skip...)
			opts.Workers = workers

			start := time.Now()
			entries, err := scan.Walk(ctx, args[0], opts)
			if err != nil {
				return fmt.Errorf("walk: %w", err)
			}
			fmt.Fprintf(stderr, "walked %d entries in %s\n", len(entries), time.Since(start).Round(time.Millisecond))

			if opts.Fingerprint || opts.FullHash {
				hashStart := time.Now()
				progress := progressPrinter(stderr)
				if err := scan.Hash(ctx, args[0], entries, opts, progress); err != nil {
					return fmt.Errorf("hash: %w", err)
				}
				fmt.Fprintf(stderr, "hashed in %s\n", time.Since(hashStart).Round(time.Millisecond))
			}

			if !summaryOnly {
				w := bufio.NewWriter(cmd.OutOrStdout())
				enc := json.NewEncoder(w)
				for i := range entries {
					if err := enc.Encode(&entries[i]); err != nil {
						return err
					}
				}
				if err := w.Flush(); err != nil {
					return err
				}
			}

			s := scan.Summarize(entries)
			fmt.Fprintf(stderr, "files %d  dirs %d  packages %d  symlinks %d  errors %d  bytes %d  took %s\n",
				s.Files, s.Dirs, s.Packages, s.Symlinks, s.Errors, s.Bytes, time.Since(start).Round(time.Millisecond))
			for k, n := range s.ByKind {
				fmt.Fprintf(stderr, "  %-8s %d\n", k, n)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&fullHash, "full-hash", false, "hash entire file contents (slow)")
	f.BoolVar(&noFingerprint, "no-fingerprint", false, "skip the head+tail fingerprint")
	f.BoolVar(&descend, "descend-packages", false, "walk into macOS packages (.fcpbundle, .app, ...)")
	f.BoolVar(&summaryOnly, "summary", false, "print only the summary, no entries")
	f.StringArrayVar(&skip, "skip", nil, "extra name or glob to skip (repeatable)")
	f.IntVar(&workers, "workers", 0, "concurrent hashing workers (default: CPU count)")
	return cmd
}

// progressPrinter reports hashing progress. On a terminal it rewrites one
// line; when stderr is redirected it prints a line every few thousand files.
func progressPrinter(w io.Writer) func(done, total int) {
	tty := false
	if f, ok := w.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			tty = true
		}
	}
	return func(done, total int) {
		switch {
		case tty && (done%50 == 0 || done == total):
			fmt.Fprintf(w, "\rhashed %d/%d", done, total)
			if done == total {
				fmt.Fprint(w, "\n")
			}
		case !tty && (done%5000 == 0 || done == total):
			fmt.Fprintf(w, "hashed %d/%d\n", done, total)
		}
	}
}
