// Package scanner runs the whole indexing pipeline for one root: volume
// identity, walk, fingerprint, clip grouping, and metadata extraction.
// The replicant CLI and the server's scheduled scans share it.
package scanner

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/cliconfig"
	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/extract"
	"github.com/fenixstarlord/indexserver/internal/extract/registry"
	"github.com/fenixstarlord/indexserver/internal/scan"
)

// Options controls a run.
type Options struct {
	Fast            bool // filesystem only, no metadata extraction
	Fingerprint     bool
	FullHash        bool
	DescendPackages bool
	KeepFrames      bool
	Skip            []string // extra skip patterns
	Workers         int
	ExtractWorkers  int
	ClipTimeout     time.Duration
	Tools           cliconfig.Tools
	Version         string // scanner version recorded in the manifest
}

// Progress reports a stage and its counters; Total is 0 when unknown.
type Progress struct {
	Stage string // "walk", "hash", "extract"
	Done  int
	Total int
}

// Result is everything a run produced.
type Result struct {
	Root        string
	Volume      scan.Volume
	Entries     []scan.Entry
	Clips       []clips.Clip
	Extractors  []extract.Status
	Started     time.Time
	WalkTime    time.Duration
	HashTime    time.Duration
	ExtractTime time.Duration
	opts        Options
}

// Run scans root. progress may be nil; it is called from several goroutines.
func Run(ctx context.Context, root string, opts Options, progress func(Progress)) (*Result, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	report := func(p Progress) {
		if progress != nil {
			progress(p)
		}
	}
	res := &Result{Root: abs, Started: time.Now(), opts: opts}

	vol, err := scan.VolumeInfo(abs)
	if err != nil {
		return nil, fmt.Errorf("volume info: %w", err)
	}
	res.Volume = vol

	so := scan.DefaultOptions()
	so.Fingerprint = opts.Fingerprint
	so.FullHash = opts.FullHash
	so.DescendPackages = opts.DescendPackages
	so.Skip = append(so.Skip, opts.Skip...)
	so.Workers = opts.Workers

	t0 := time.Now()
	entries, err := scan.Walk(ctx, abs, so)
	if err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}
	res.WalkTime = time.Since(t0)
	report(Progress{Stage: "walk", Done: len(entries), Total: len(entries)})

	if so.Fingerprint || so.FullHash {
		t1 := time.Now()
		if err := scan.Hash(ctx, abs, entries, so, func(done, total int) { report(Progress{Stage: "hash", Done: done, Total: total}) }); err != nil {
			return nil, fmt.Errorf("hash: %w", err)
		}
		res.HashTime = time.Since(t1)
	}

	res.Entries, res.Clips = clips.Group(entries, clips.Options{KeepFrames: opts.KeepFrames})

	if !opts.Fast {
		runner := extract.NewRunner(ctx, registry.All(abs, res.Entries, opts.Tools))
		res.Extractors = runner.Statuses()
		t2 := time.Now()
		if err := runner.Run(ctx, abs, res.Clips, extract.Options{Workers: opts.ExtractWorkers, ClipTimeout: opts.ClipTimeout},
			func(done, total int) { report(Progress{Stage: "extract", Done: done, Total: total}) }); err != nil {
			return nil, fmt.Errorf("extract: %w", err)
		}
		res.ExtractTime = time.Since(t2)
	}
	return res, nil
}

// Manifest builds the bundle manifest for the result.
func (r *Result) Manifest() bundle.Manifest {
	skip := append([]string(nil), scan.DefaultSkip...)
	skip = append(skip, r.opts.Skip...)
	ext := make([]bundle.ExtractorInfo, 0, len(r.Extractors))
	for _, s := range r.Extractors {
		ext = append(ext, bundle.ExtractorInfo{Name: s.Name, Version: s.Version, Available: s.Available})
	}
	return bundle.Manifest{
		BundleVersion:  bundle.Version,
		ScannerVersion: r.opts.Version,
		ScannedAt:      r.Started.UTC(),
		Root:           r.Root,
		Volume:         r.Volume,
		Options: bundle.Options{
			Fast: r.opts.Fast, Fingerprint: r.opts.Fingerprint, FullHash: r.opts.FullHash,
			DescendPackages: r.opts.DescendPackages, KeepFrames: r.opts.KeepFrames, Skip: skip,
		},
		Extractors: ext,
		Summary:    scan.Summarize(r.Entries),
		ClipCount:  len(r.Clips),
		DurationMS: time.Since(r.Started).Milliseconds(),
	}
}

// Bundle returns the in-memory bundle, for direct ingest.
func (r *Result) Bundle() *bundle.Bundle {
	return &bundle.Bundle{Manifest: r.Manifest(), Entries: r.Entries, Clips: r.Clips}
}

// MetaStats counts clips with metadata and with errors.
func (r *Result) MetaStats() (withMeta, withErrors int) {
	for _, c := range r.Clips {
		if c.Meta != nil && len(c.Sources) > 0 {
			withMeta++
		}
		if len(c.Errors) > 0 {
			withErrors++
		}
	}
	return
}
