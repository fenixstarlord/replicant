// Package bundle defines the .replicant scan bundle: a zip file holding a
// manifest, the scanned entries, and the clips. The CLI writes it and the
// server reads it, whether the bundle travels as a file or an HTTP body.
package bundle

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/scan"
)

// Version is the bundle format version written into the manifest.
const Version = 1

// File names inside the zip.
const (
	ManifestName = "manifest.json"
	EntriesName  = "entries.jsonl"
	ClipsName    = "clips.jsonl"
)

// Manifest describes a scan.
type Manifest struct {
	BundleVersion  int             `json:"bundle_version"`
	ScannerVersion string          `json:"scanner_version"`
	ScannedAt      time.Time       `json:"scanned_at"`
	Root           string          `json:"root"` // absolute path that was scanned
	Volume         scan.Volume     `json:"volume"`
	Options        Options         `json:"options"`
	Extractors     []ExtractorInfo `json:"extractors"`
	Summary        scan.Summary    `json:"summary"`
	ClipCount      int             `json:"clip_count"`
	DurationMS     int64           `json:"duration_ms"`
}

// Options records how the scan was run.
type Options struct {
	Fast            bool     `json:"fast"`
	Fingerprint     bool     `json:"fingerprint"`
	FullHash        bool     `json:"full_hash"`
	DescendPackages bool     `json:"descend_packages"`
	KeepFrames      bool     `json:"keep_frames"`
	Skip            []string `json:"skip"`
}

// ExtractorInfo records an extractor's availability at scan time.
type ExtractorInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Available bool   `json:"available"`
}

// Bundle is a fully read scan bundle.
type Bundle struct {
	Manifest Manifest
	Entries  []scan.Entry
	Clips    []clips.Clip
}

// Write streams a bundle to w as a zip archive.
func Write(w io.Writer, m Manifest, entries []scan.Entry, cl []clips.Clip) error {
	m.BundleVersion = Version
	m.ClipCount = len(cl)
	zw := zip.NewWriter(w)

	mf, err := zw.Create(ManifestName)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(mf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}

	if err := writeLines(zw, EntriesName, len(entries), func(i int) any { return &entries[i] }); err != nil {
		return err
	}
	if err := writeLines(zw, ClipsName, len(cl), func(i int) any { return &cl[i] }); err != nil {
		return err
	}
	return zw.Close()
}

func writeLines(zw *zip.Writer, name string, n int, at func(i int) any) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 1<<16)
	enc := json.NewEncoder(bw)
	for i := 0; i < n; i++ {
		if err := enc.Encode(at(i)); err != nil {
			return fmt.Errorf("%s line %d: %w", name, i, err)
		}
	}
	return bw.Flush()
}

// Read parses a whole bundle from r.
func Read(r io.ReaderAt, size int64) (*Bundle, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a zip bundle: %w", err)
	}
	b := &Bundle{}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	for _, name := range []string{ManifestName, EntriesName, ClipsName} {
		if files[name] == nil {
			return nil, fmt.Errorf("bundle is missing %s", name)
		}
	}

	if err := readJSON(files[ManifestName], &b.Manifest); err != nil {
		return nil, err
	}
	if b.Manifest.BundleVersion != Version {
		return nil, fmt.Errorf("unsupported bundle version %d (want %d)", b.Manifest.BundleVersion, Version)
	}
	if err := readLines(files[EntriesName], func(dec *json.Decoder) error {
		var e scan.Entry
		if err := dec.Decode(&e); err != nil {
			return err
		}
		b.Entries = append(b.Entries, e)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := readLines(files[ClipsName], func(dec *json.Decoder) error {
		var c clips.Clip
		if err := dec.Decode(&c); err != nil {
			return err
		}
		b.Clips = append(b.Clips, c)
		return nil
	}); err != nil {
		return nil, err
	}
	return b, nil
}

func readJSON(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := json.NewDecoder(rc).Decode(v); err != nil {
		return fmt.Errorf("%s: %w", f.Name, err)
	}
	return nil
}

func readLines(f *zip.File, each func(*json.Decoder) error) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := json.NewDecoder(bufio.NewReaderSize(rc, 1<<16))
	for dec.More() {
		if err := each(dec); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}
