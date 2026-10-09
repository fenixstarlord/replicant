package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/indexserver/internal/scan"
)

func newDumpCmd() *cobra.Command {
	var (
		f           scanFlags
		summaryOnly bool
		showClips   bool
	)
	cmd := &cobra.Command{
		Use:   "dump <path>",
		Short: "Print what would be indexed as JSON lines (for debugging)",
		Long: `Walk a mounted volume read-only and print one JSON object per entry (or
per clip with --clips) to stdout. Progress and a summary go to stderr.
Nothing is sent anywhere.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			stderr := cmd.ErrOrStderr()
			res, err := runScan(cmd, args[0], &f)
			if err != nil {
				return err
			}
			if !summaryOnly {
				w := bufio.NewWriter(cmd.OutOrStdout())
				enc := json.NewEncoder(w)
				if showClips {
					for i := range res.Clips {
						if err := enc.Encode(&res.Clips[i]); err != nil {
							return err
						}
					}
				} else {
					for i := range res.Entries {
						if err := enc.Encode(&res.Entries[i]); err != nil {
							return err
						}
					}
				}
				if err := w.Flush(); err != nil {
					return err
				}
			}
			s := scan.Summarize(res.Entries)
			fmt.Fprintf(stderr, "files %d  dirs %d  packages %d  symlinks %d  errors %d  bytes %d  clips %d  took %s\n",
				s.Files, s.Dirs, s.Packages, s.Symlinks, s.Errors, s.Bytes, len(res.Clips), time.Since(res.Started).Round(time.Millisecond))
			for k, n := range s.ByKind {
				fmt.Fprintf(stderr, "  %-8s %d\n", k, n)
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().BoolVar(&summaryOnly, "summary", false, "print only the summary, no entries")
	cmd.Flags().BoolVar(&showClips, "clips", false, "print clips instead of entries")
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
