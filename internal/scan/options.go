package scan

import (
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultSkip lists macOS housekeeping entries that are never indexed.
// Patterns use filepath.Match syntax and are matched against the base name.
var DefaultSkip = []string{
	".Spotlight-V100",
	".fseventsd",
	".Trashes",
	".TemporaryItems",
	".DocumentRevisions-V100",
	".DS_Store",
	"._*",
}

// DefaultPackageExts lists directory extensions treated as a single item
// instead of being descended into.
var DefaultPackageExts = []string{
	".fcpbundle",
	".photoslibrary",
	".app",
	".bundle",
	".framework",
	".drp",
}

// Options controls a scan.
type Options struct {
	// Skip holds names or filepath.Match patterns to leave out entirely.
	Skip []string
	// PackageExts lists directory extensions treated as single items.
	PackageExts []string
	// DescendPackages walks into package directories instead of
	// recording them as one entry.
	DescendPackages bool
	// Fingerprint computes the partial xxHash64 fingerprint of each file.
	Fingerprint bool
	// FullHash computes xxHash64 over the entire contents of each file.
	FullHash bool
	// Workers bounds concurrent hashing. Zero means runtime.NumCPU().
	Workers int
}

// DefaultOptions returns the options used by a normal scan.
func DefaultOptions() Options {
	return Options{
		Skip:        append([]string(nil), DefaultSkip...),
		PackageExts: append([]string(nil), DefaultPackageExts...),
		Fingerprint: true,
	}
}

func (o Options) skip(name string) bool {
	for _, p := range o.Skip {
		if strings.ContainsAny(p, "*?[") {
			if ok, _ := filepath.Match(p, name); ok {
				return true
			}
			continue
		}
		if p == name {
			return true
		}
	}
	return false
}

func (o Options) isPackage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return false
	}
	for _, p := range o.PackageExts {
		if strings.EqualFold(p, ext) {
			return true
		}
	}
	return false
}

func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}
	return runtime.NumCPU()
}
