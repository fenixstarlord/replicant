package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
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
	tape            tapeFlag
	tapeStreamMB    int64
}

// tapeFlag is the --tape flag: auto, on or off.
type tapeFlag struct{ mode scanner.TapeMode }

func (t *tapeFlag) String() string {
	if t.mode == "" {
		return string(scanner.TapeAuto)
	}
	return string(t.mode)
}

func (t *tapeFlag) Set(s string) error {
	m, err := scanner.ParseTapeMode(s)
	if err != nil {
		return err
	}
	t.mode = m
	return nil
}

func (t *tapeFlag) Type() string { return "mode" }

func (f *scanFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.fullHash, "full-hash", false, "hash entire file contents (slow)")
	fl.BoolVar(&f.noFingerprint, "no-fingerprint", false, "skip the head+tail fingerprint")
	fl.BoolVar(&f.descendPackages, "descend-packages", false, "walk into macOS packages (.fcpbundle, .app, ...)")
	fl.BoolVar(&f.keepFrames, "keep-frames", false, "keep individual ARRIRAW frame entries instead of only the clip")
	fl.StringArrayVar(&f.skip, "skip", nil, "extra name or glob to skip (repeatable)")
	fl.IntVar(&f.workers, "workers", 0, "concurrent hashing workers (default: CPU count; 1 in tape mode)")
	fl.BoolVar(&f.fast, "fast", false, "filesystem only, no metadata extraction")
	fl.IntVar(&f.extractWorkers, "extract-workers", 0, "concurrent clips during extraction (default: CPUs, max 8; 1 in tape mode)")
	fl.DurationVar(&f.clipTimeout, "clip-timeout", 0, "per-extractor timeout for one clip (default 1m, 20m in tape mode; negative: no limit)")
	fl.Var(&f.tape, "tape", "tape mode: read one file at a time in tape order (auto: when the volume is LTFS, on, off)")
	fl.Lookup("tape").NoOptDefVal = string(scanner.TapeOn)
	fl.Int64Var(&f.tapeStreamMB, "tape-stream-mb", scanner.DefaultTapeStreamMB, "tape mode: files up to this many MB are read front to back instead of at head and tail (0: always head and tail)")
}

func (f *scanFlags) options(tools cliconfig.Tools) scanner.Options {
	return scanner.Options{
		Fast: f.fast, Fingerprint: !f.noFingerprint, FullHash: f.fullHash, DescendPackages: f.descendPackages,
		KeepFrames: f.keepFrames, Skip: f.skip, Workers: f.workers, ExtractWorkers: f.extractWorkers,
		ClipTimeout: f.clipTimeout, Tape: f.tape.mode, StreamLimit: streamLimit(f.tapeStreamMB),
		Tools: tools, Version: version,
	}
}

// streamLimit converts --tape-stream-mb to scanner.Options.StreamLimit.
func streamLimit(mb int64) int64 {
	if mb <= 0 {
		return -1
	}
	return mb << 20
}

// runScan runs the shared pipeline with terminal progress. rep, if not
// nil, also sends progress to the server, showing the drive as driveName
// when one is given.
func runScan(cmd *cobra.Command, root string, f *scanFlags, rep *client.ActivityReporter, driveName string) (*scanner.Result, error) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first Ctrl-C, restore the default so a second one exits
	// even while a tape read is still in progress.
	context.AfterFunc(ctx, stop)
	stderr := cmd.ErrOrStderr()
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, err
	}
	// Keep the server's activity alive through long reads (a tape locate,
	// one extractor on a large file) that report no progress for minutes.
	keepalive := time.NewTicker(time.Minute)
	defer keepalive.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-keepalive.C:
				rep.Keepalive()
			}
		}
	}()
	hashPrinter, endLine := progressPrinter(stderr)
	var printMu sync.Mutex
	progress := func(p scanner.Progress) {
		rep.Progress(p.Stage, p.Done, p.Total)
		printMu.Lock()
		defer printMu.Unlock()
		switch p.Stage {
		case "walk":
			endLine()
			fmt.Fprintf(stderr, "walked %d entries\n", p.Done)
		case "tape":
			endLine()
			switch {
			case p.Total == 0:
				fmt.Fprintf(stderr, "tape mode: no file data to read\n")
			case p.Done == 0:
				fmt.Fprintf(stderr, "tape mode: no tape positions available, reading one file at a time in path order\n")
			default:
				fmt.Fprintf(stderr, "tape mode: tape positions for %d/%d files, reading one file at a time in tape order\n", p.Done, p.Total)
			}
		case "hash":
			hashPrinter(p.Done, p.Total)
		case "extract":
			if p.Done%25 == 0 || p.Done == p.Total {
				endLine()
				fmt.Fprintf(stderr, "extracted %d/%d\n", p.Done, p.Total)
			}
		}
	}
	res, err := scanner.Run(ctx, root, f.options(cfg.Tools), progress)
	printMu.Lock()
	endLine()
	printMu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			rep.Finish("cancelled", "")
		} else {
			rep.Finish("error", err.Error())
		}
		return nil, err
	}
	v := res.Volume
	shown := v.Name
	if driveName != "" {
		shown = driveName
	}
	rep.Describe(shown, v.UUID)
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
		for _, t := range res.ExtractorTimes {
			if t.Runs > 0 {
				fmt.Fprintf(stderr, "  %-12s %d clips in %s (%d errors, %d timeouts)\n", t.Name, t.Runs, t.Total.Round(time.Millisecond), t.Errors, t.Timeouts)
			}
		}
	}
	if t := res.Tape; t != nil {
		fmt.Fprintf(stderr, "tape: hashed %d files (%d positioned, %d streamed, %d head+tail, %d failed), %d backward reads, read %d bytes in %s\n",
			t.Files, t.Positioned, t.Streamed, t.HeadTail, t.Failed, t.Backward, t.BytesRead, res.HashTime.Round(time.Millisecond))
	}
	return res, nil
}

func newScanCmd() *cobra.Command {
	var (
		f         scanFlags
		output    string
		driveName string
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
				shown := filepath.Base(abs)
				if driveName != "" {
					shown = driveName
				}
				rep, aerr = client.NewActivityReporter(cmd.Context(), c, api.ActivityStart{Host: host, DriveName: shown, Root: abs})
				if aerr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: could not report progress to the server: %v\n", aerr)
				}
			}
			res, err := runScan(cmd, args[0], &f, rep, driveName)
			if err != nil {
				return err
			}
			m := res.Manifest()
			m.DriveName = driveName
			if c != nil {
				tmp, err := os.CreateTemp("", "replicant-push-*.replicant")
				if err != nil {
					return err
				}
				tmp.Close()
				defer os.Remove(tmp.Name())
				output = tmp.Name()
			}
			if err := writeBundleFile(output, m, res.Entries, res.Clips); err != nil {
				rep.Finish("error", err.Error())
				return err
			}
			st, _ := os.Stat(output)
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s (%d bytes) in %s\n", output, st.Size(), time.Since(res.Started).Round(time.Millisecond))
			if c != nil {
				rep.Progress("upload", 0, 0)
				stored, err := uploadBundleActivity(cmd, c, output, rep.ID())
				if err != nil {
					rep.Finish("error", err.Error())
					return err
				}
				if driveName != "" && stored.DriveName != driveName {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: the server kept the name %q, not %q (an older server, or it has a newer scan of this drive)\n",
						stored.DriveName, driveName)
				}
				return nil
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the bundle to this file instead of pushing")
	cmd.Flags().StringVar(&driveName, "name", "", "catalog name for the drive, kept on later scans; the volume's own name makes it follow the volume again (default: leave the catalog name as it is)")
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
