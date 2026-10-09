package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/replicant/internal/api"
	"github.com/fenixstarlord/replicant/internal/bundle"
	"github.com/fenixstarlord/replicant/internal/cliconfig"
	"github.com/fenixstarlord/replicant/internal/client"
	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/scan"
	"github.com/fenixstarlord/replicant/internal/scanner"
)

// scanFlags are shared by `scan` and `dump`.
type scanFlags struct {
	fullHash        bool
	noFingerprint   bool
	descendPackages bool
	keepFrames      bool
	skip            []string
	workers         int
	fast            bool
	extractWorkers  int
	clipTimeout     time.Duration
}

func (f *scanFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.fullHash, "full-hash", false, "hash entire file contents (slow)")
	fl.BoolVar(&f.noFingerprint, "no-fingerprint", false, "skip the head+tail fingerprint")
	fl.BoolVar(&f.descendPackages, "descend-packages", false, "walk into macOS packages (.fcpbundle, .app, ...)")
	fl.BoolVar(&f.keepFrames, "keep-frames", false, "keep individual ARRIRAW frame entries instead of only the clip")
	fl.StringArrayVar(&f.skip, "skip", nil, "extra name or glob to skip (repeatable)")
	fl.IntVar(&f.workers, "workers", 0, "concurrent hashing workers (default: CPU count)")
	fl.BoolVar(&f.fast, "fast", false, "filesystem only, no metadata extraction")
	fl.IntVar(&f.extractWorkers, "extract-workers", 0, "concurrent clips during extraction (default: CPUs, max 8)")
	fl.DurationVar(&f.clipTimeout, "clip-timeout", 60*time.Second, "per-extractor timeout for one clip")
}

func (f *scanFlags) options(tools cliconfig.Tools) scanner.Options {
	return scanner.Options{
		Fast: f.fast, Fingerprint: !f.noFingerprint, FullHash: f.fullHash, DescendPackages: f.descendPackages,
		KeepFrames: f.keepFrames, Skip: f.skip, Workers: f.workers, ExtractWorkers: f.extractWorkers,
		ClipTimeout: f.clipTimeout, Tools: tools, Version: version,
	}
}

// runScan runs the shared pipeline with terminal progress. rep, if not
// nil, also sends progress to the server.
func runScan(cmd *cobra.Command, root string, f *scanFlags, rep *client.ActivityReporter) (*scanner.Result, error) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stderr := cmd.ErrOrStderr()
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, err
	}
	hashPrinter := progressPrinter(stderr)
	progress := func(p scanner.Progress) {
		rep.Progress(p.Stage, p.Done, p.Total)
		switch p.Stage {
		case "walk":
			fmt.Fprintf(stderr, "walked %d entries\n", p.Done)
		case "hash":
			hashPrinter(p.Done, p.Total)
		case "extract":
			if p.Done%25 == 0 || p.Done == p.Total {
				fmt.Fprintf(stderr, "extracted %d/%d\n", p.Done, p.Total)
			}
		}
	}
	res, err := scanner.Run(ctx, root, f.options(cfg.Tools), progress)
	if err != nil {
		if ctx.Err() != nil {
			rep.Finish("cancelled", "")
		} else {
			rep.Finish("error", err.Error())
		}
		return nil, err
	}
	v := res.Volume
	rep.Describe(v.Name, v.UUID)
	fmt.Fprintf(stderr, "volume %q uuid=%s fs=%s mount=%s\n", v.Name, v.UUID, v.FSType, v.MountPoint)
	fmt.Fprintf(stderr, "grouped %d clips: %v\n", len(res.Clips), clips.Summary(res.Clips))
	if !f.fast {
		var names []string
		for _, s := range res.Extractors {
			if s.Available {
				names = append(names, s.Name)
			}
		}
		withMeta, withErr := res.MetaStats()
		fmt.Fprintf(stderr, "extractors available: %s\n", strings.Join(names, ", "))
		fmt.Fprintf(stderr, "extracted metadata for %d/%d clips (%d with errors) in %s\n", withMeta, len(res.Clips), withErr, res.ExtractTime.Round(time.Millisecond))
	}
	return res, nil
}

func newScanCmd() *cobra.Command {
	var (
		f      scanFlags
		output string
	)
	cmd := &cobra.Command{
		Use:   "scan <path>",
		Short: "Scan a mounted drive and write or push a .replicant bundle",
		Long: `Walk a mounted volume read-only, fingerprint files, group them into clips,
extract metadata, and write the result as a .replicant bundle. With -o the bundle
is written to a file; without it the bundle is pushed to the server configured
by 'replicant login'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var c *client.Client
			if output == "" {
				var err error
				if c, err = loadClient(); err != nil {
					return fmt.Errorf("%w (or pass -o <file.replicant> to write a bundle instead)", err)
				}
			}
			var rep *client.ActivityReporter
			if c != nil {
				abs, _ := filepath.Abs(args[0])
				host, _ := os.Hostname()
				var aerr error
				rep, aerr = client.NewActivityReporter(cmd.Context(), c, api.ActivityStart{Host: host, DriveName: filepath.Base(abs), Root: abs})
				if aerr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: could not report progress to the server: %v\n", aerr)
				}
			}
			res, err := runScan(cmd, args[0], &f, rep)
			if err != nil {
				return err
			}
			if c != nil {
				tmp, err := os.CreateTemp("", "replicant-push-*.replicant")
				if err != nil {
					return err
				}
				tmp.Close()
				defer os.Remove(tmp.Name())
				output = tmp.Name()
			}
			if err := writeBundleFile(output, res.Manifest(), res.Entries, res.Clips); err != nil {
				rep.Finish("error", err.Error())
				return err
			}
			st, _ := os.Stat(output)
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s (%d bytes) in %s\n", output, st.Size(), time.Since(res.Started).Round(time.Millisecond))
			if c != nil {
				rep.Progress("upload", 0, 0)
				if err := uploadBundleActivity(cmd, c, output, rep.ID()); err != nil {
					rep.Finish("error", err.Error())
					return err
				}
				return nil
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the bundle to this file instead of pushing")
	return cmd
}

// writeBundleFile writes to a temp file next to path and renames it into
// place, so a failed scan never leaves a half-written bundle.
func writeBundleFile(path string, m bundle.Manifest, entries []scan.Entry, cl []clips.Clip) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".replicant-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := bundle.Write(tmp, m, entries, cl); err != nil {
		tmp.Close()
		return fmt.Errorf("write bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
