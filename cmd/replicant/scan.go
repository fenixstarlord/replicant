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
	"github.com/fenixstarlord/replicant/internal/scanprofile"
)

// scanFlags are shared by `scan` and `dump`.
type scanFlags struct {
	extractors      []string
	profile         bool
	expectedVolume  string
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
	fl.StringSliceVar(&f.extractors, "extractors", nil, "metadata extractor allowlist (comma-separated; empty disables all)")
	fl.BoolVar(&f.fullHash, "full-hash", false, "hash entire file contents (slow)")
	fl.BoolVar(&f.noFingerprint, "no-fingerprint", false, "skip the head+tail fingerprint")
	fl.BoolVar(&f.descendPackages, "descend-packages", false, "walk into macOS packages (.fcpbundle, .app, ...)")
	fl.BoolVar(&f.keepFrames, "keep-frames", false, "keep individual ARRIRAW frame entries instead of only the clip")
	fl.StringArrayVar(&f.skip, "skip", nil, "extra name or glob to skip (repeatable)")
	fl.IntVar(&f.workers, "workers", 0, "concurrent hashing workers (default: CPU count; 1 in tape mode)")
	fl.BoolVar(&f.fast, "fast", false, "skip metadata extraction (still fingerprints files unless --no-fingerprint)")
	fl.IntVar(&f.extractWorkers, "extract-workers", 0, "concurrent clips during extraction (default: CPUs, max 8; 1 in tape mode)")
	fl.DurationVar(&f.clipTimeout, "clip-timeout", 0, "per-extractor timeout for one clip (default 1m, 20m in tape mode; negative: no limit)")
	fl.Var(&f.tape, "tape", "tape mode: read one file at a time in tape order (auto: when the volume is LTFS, on, off)")
	fl.Lookup("tape").NoOptDefVal = string(scanner.TapeOn)
	fl.Int64Var(&f.tapeStreamMB, "tape-stream-mb", scanner.DefaultTapeStreamMB, "tape mode: files up to this many MB are read front to back instead of at head and tail (0: always head and tail)")
}

func (f *scanFlags) options(tools cliconfig.Tools) scanner.Options {
	return scanner.Options{
		Extractors: f.extractors, ExpectedVolume: f.expectedVolume,
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

// scanContext is the context of a scan command, from start to the end of
// the upload: cancelled by SIGINT or SIGTERM, so the app's Cancel, quit and
// eject stop a scan cleanly at any stage, and, when the macOS app started
// the command, once the app has gone.
func scanContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	// After the first Ctrl-C, restore the default so a second one exits
	// even while a tape read is still in progress.
	context.AfterFunc(ctx, stop)
	if os.Getenv("REPLICANT_EXIT_WITH_PARENT") == "1" {
		// If the app quits or crashes, stop rather than keep the drive busy
		// with nobody to report to.
		ctx = cancelWhenOrphaned(ctx, os.Getppid, time.Second)
	}
	return ctx, stop
}

// finishedReadingLine starts the stderr line printed once a scan has
// closed every file on the drive. The macOS app watches for it: from then
// on an eject no longer needs to stop the scan. Keep it stable.
const finishedReadingLine = "finished reading "

// runScan runs the shared pipeline with terminal progress. rep, if not
// nil, also sends progress to the server, showing the drive as driveName
// when one is given.
func runScan(ctx context.Context, cmd *cobra.Command, root string, f *scanFlags, rep *client.ActivityReporter, driveName string) (*scanner.Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stderr := cmd.ErrOrStderr()
	cfg, err := cliconfig.Load()
	if err != nil {
		return nil, err
	}
	hashPrinter, endLine := progressPrinter(stderr)
	var printMu sync.Mutex
	var active scanner.Progress
	var activeSince time.Time
	// Keep both the server and the menu-bar app informed during a tape locate.
	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()
	keepaliveDone := make(chan struct{})
	defer func() {
		cancel()
		<-keepaliveDone
	}()
	go func() {
		defer close(keepaliveDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-keepalive.C:
				rep.Keepalive()
				printMu.Lock()
				if active.Event == "start" {
					p := active
					p.Event, p.Elapsed = "waiting", time.Since(activeSince)
					printTapeProgress(stderr, p)
				}
				printMu.Unlock()
			}
		}
	}()
	progress := func(p scanner.Progress) {
		rep.Progress(p.Stage, p.Done, p.Total)
		printMu.Lock()
		defer printMu.Unlock()
		active = p
		if p.Event != "" {
			if p.Event == "start" {
				activeSince = time.Now()
			}
			endLine()
			printTapeProgress(stderr, p)
			return
		}
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
	opts := f.options(cfg.Tools)
	if f.profile {
		c, err := client.New(cfg.Server, cfg.Token)
		if err != nil {
			return nil, err
		}
		opts.Profile = func(ctx context.Context, key string) (*scanprofile.Settings, error) {
			p, err := resolveProfile(ctx, c, key, stderr)
			if err != nil {
				return nil, err
			}
			if cmd.Flags().Changed("no-fingerprint") {
				p.Fingerprint = !f.noFingerprint
			}
			if cmd.Flags().Changed("extractors") {
				p.Extractors = append([]string{}, f.extractors...)
			}
			return &p.Settings, nil
		}
	}
	res, err := scanner.Run(ctx, root, opts, progress)
	printMu.Lock()
	active = scanner.Progress{}
	endLine()
	printMu.Unlock()
	if err == nil {
		fmt.Fprintf(stderr, "%s%s\n", finishedReadingLine, res.Root)
	}
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
			ctx, stop := scanContext(cmd)
			defer stop()
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
				rep, aerr = client.NewActivityReporter(ctx, c, api.ActivityStart{Host: host, DriveName: shown, Root: abs})
				if aerr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: could not report progress to the server: %v\n", aerr)
				}
			}
			res, err := runScan(ctx, cmd, args[0], &f, rep, driveName)
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
				stored, err := uploadBundleActivity(ctx, cmd, c, output, rep.ID())
				if err != nil {
					if ctx.Err() != nil {
						rep.Finish("cancelled", "")
						return ctx.Err()
					}
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
	cmd.Flags().BoolVar(&f.profile, "profile", false, "use the volume's shared server scan settings (explicit flags override them)")
	cmd.Flags().StringVar(&f.expectedVolume, "expect-volume", "", "refuse to scan if the mounted volume's catalog key changed")
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

// cancelWhenOrphaned returns a context that is cancelled once the parent
// process has exited: the process is re-parented, so getppid changes.
func cancelWhenOrphaned(parent context.Context, getppid func() int, every time.Duration) context.Context {
	ctx, cancel := context.WithCancel(parent)
	ppid := getppid()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if getppid() != ppid {
					cancel()
					return
				}
			}
		}
	}()
	return ctx
}
