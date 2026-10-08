package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/client"
	"github.com/fenixstarlord/indexserver/internal/clips"
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
}

func (f *scanFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.BoolVar(&f.fullHash, "full-hash", false, "hash entire file contents (slow)")
	fl.BoolVar(&f.noFingerprint, "no-fingerprint", false, "skip the head+tail fingerprint")
	fl.BoolVar(&f.descendPackages, "descend-packages", false, "walk into macOS packages (.fcpbundle, .app, ...)")
	fl.BoolVar(&f.keepFrames, "keep-frames", false, "keep individual ARRIRAW frame entries instead of only the clip")
	fl.StringArrayVar(&f.skip, "skip", nil, "extra name or glob to skip (repeatable)")
	fl.IntVar(&f.workers, "workers", 0, "concurrent hashing workers (default: CPU count)")
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
	root     string
	volume   scan.Volume
	entries  []scan.Entry
	clips    []clips.Clip
	started  time.Time
	walkTime time.Duration
	hashTime time.Duration
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
	return res, nil
}

func newScanCmd() *cobra.Command {
	var (
		f      scanFlags
		output string
		fast   bool
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
			if !fast {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: metadata extraction arrives in Phase 4; scanning filesystem only")
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
					Fast:            true,
					Fingerprint:     !f.noFingerprint,
					FullHash:        f.fullHash,
					DescendPackages: f.descendPackages,
					KeepFrames:      f.keepFrames,
					Skip:            f.scanOptions().Skip,
				},
				Extractors: []bundle.ExtractorInfo{},
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
	cmd.Flags().BoolVar(&fast, "fast", false, "filesystem only, no metadata extraction")
	return cmd
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
