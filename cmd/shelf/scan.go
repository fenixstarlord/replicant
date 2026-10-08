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

	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/cliconfig"
	"github.com/fenixstarlord/indexserver/internal/client"
	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/extract/registry"
	"github.com/fenixstarlord/indexserver/internal/scan"
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

func (f *scanFlags) scanOptions() scan.Options {
	opts := scan.DefaultOptions()
	opts.Fingerprint = !f.noFingerprint
	opts.FullHash = f.fullHash
	opts.DescendPackages = f.descendPackages
	opts.Skip = append(opts.Skip, f.skip...)
	opts.Workers = f.workers
	return opts
}

// scanResult is everything a scan produces before extraction.
type scanResult struct {
	root       string
	volume     scan.Volume
	entries    []scan.Entry
	clips      []clips.Clip
	started    time.Time
	walkTime   time.Duration
	hashTime   time.Duration
	extractors []extract.Status
}

// runScan walks, hashes, and groups. It is the one code path behind
// `scan` and `dump`.
func runScan(cmd *cobra.Command, root string, f *scanFlags) (*scanResult, error) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stderr := cmd.ErrOrStderr()

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	res := &scanResult{root: abs, started: time.Now()}

	vol, err := scan.VolumeInfo(abs)
	if err != nil {
		return nil, fmt.Errorf("volume info: %w", err)
	}
	res.volume = vol
	fmt.Fprintf(stderr, "volume %q uuid=%s fs=%s mount=%s\n", vol.Name, vol.UUID, vol.FSType, vol.MountPoint)

	opts := f.scanOptions()
	t0 := time.Now()
	entries, err := scan.Walk(ctx, abs, opts)
	if err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}
	res.walkTime = time.Since(t0)
	fmt.Fprintf(stderr, "walked %d entries in %s\n", len(entries), res.walkTime.Round(time.Millisecond))

	if opts.Fingerprint || opts.FullHash {
		t1 := time.Now()
		if err := scan.Hash(ctx, abs, entries, opts, progressPrinter(stderr)); err != nil {
			return nil, fmt.Errorf("hash: %w", err)
		}
		res.hashTime = time.Since(t1)
		fmt.Fprintf(stderr, "hashed in %s\n", res.hashTime.Round(time.Millisecond))
	}

	res.entries, res.clips = clips.Group(entries, clips.Options{KeepFrames: f.keepFrames})
	fmt.Fprintf(stderr, "grouped %d clips: %v\n", len(res.clips), clips.Summary(res.clips))

	if !f.fast {
		cfg, err := cliconfig.Load()
		if err != nil {
			return nil, err
		}
		runner := extract.NewRunner(ctx, registry.All(abs, res.entries, cfg.Tools))
		res.extractors = runner.Statuses()
		var names []string
		for _, s := range res.extractors {
			if s.Available {
				names = append(names, s.Name)
			}
		}
		fmt.Fprintf(stderr, "extractors available: %s\n", strings.Join(names, ", "))
		t2 := time.Now()
		progress := func(done, total int) {
			if done%25 == 0 || done == total {
				fmt.Fprintf(stderr, "extracted %d/%d\n", done, total)
			}
		}
		if err := runner.Run(ctx, abs, res.clips, extract.Options{Workers: f.extractWorkers, ClipTimeout: f.clipTimeout}, progress); err != nil {
			return nil, fmt.Errorf("extract: %w", err)
		}
		var withMeta, withErr int
		for _, c := range res.clips {
			if c.Meta != nil && len(c.Sources) > 0 {
				withMeta++
			}
			if len(c.Errors) > 0 {
				withErr++
			}
		}
		fmt.Fprintf(stderr, "extracted metadata for %d/%d clips (%d with errors) in %s\n", withMeta, len(res.clips), withErr, time.Since(t2).Round(time.Millisecond))
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
		Short: "Scan a mounted drive and write a .shelf bundle",
		Long: `Walk a mounted volume read-only, fingerprint files, group them into clips,
and write the result as a .shelf bundle. With -o the bundle is written to a file; without it the
bundle is pushed to the server configured by 'shelf login'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var c *client.Client
			if output == "" {
				var err error
				if c, err = loadClient(); err != nil {
					return fmt.Errorf("%w (or pass -o <file.shelf> to write a bundle instead)", err)
				}
			}
			res, err := runScan(cmd, args[0], &f)
			if err != nil {
				return err
			}
			m := bundle.Manifest{
				ScannerVersion: version,
				ScannedAt:      res.started.UTC(),
				Root:           res.root,
				Volume:         res.volume,
				Options: bundle.Options{
					Fast:            f.fast,
					Fingerprint:     !f.noFingerprint,
					FullHash:        f.fullHash,
					DescendPackages: f.descendPackages,
					KeepFrames:      f.keepFrames,
					Skip:            f.scanOptions().Skip,
				},
				Extractors: extractorInfos(res.extractors),
				Summary:    scan.Summarize(res.entries),
				DurationMS: time.Since(res.started).Milliseconds(),
			}
			if c != nil {
				// Direct push: same bundle, written to a temp file and streamed up.
				tmp, err := os.CreateTemp("", "shelf-push-*.shelf")
				if err != nil {
					return err
				}
				tmp.Close()
				defer os.Remove(tmp.Name())
				output = tmp.Name()
			}
			if err := writeBundleFile(output, m, res.entries, res.clips); err != nil {
				return err
			}
			st, _ := os.Stat(output)
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s (%d bytes) in %s\n", output, st.Size(), time.Since(res.started).Round(time.Millisecond))
			if c != nil {
				return uploadBundle(cmd, c, output)
			}
			return nil
		},
	}
	f.bind(cmd)
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the bundle to this file instead of pushing")
	return cmd
}

func extractorInfos(st []extract.Status) []bundle.ExtractorInfo {
	out := make([]bundle.ExtractorInfo, 0, len(st))
	for _, s := range st {
		out = append(out, bundle.ExtractorInfo{Name: s.Name, Version: s.Version, Available: s.Available})
	}
	return out
}

// writeBundleFile writes to a temp file next to path and renames it into
// place, so a failed scan never leaves a half-written bundle.
func writeBundleFile(path string, m bundle.Manifest, entries []scan.Entry, cl []clips.Clip) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".shelf-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := bundle.Write(tmp, m, entries, cl); err != nil {
		tmp.Close()
		return fmt.Errorf("write bundle: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
