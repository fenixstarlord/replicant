// Package scanprofile defines the portable, per-volume scan settings shared
// by the catalog, CLI and macOS clients.
package scanprofile

import (
	"fmt"
	"slices"
)

// ExtractorNames are stable identifiers, also used by replicant doctor.
var ExtractorNames = []string{"art-cmd", "redline", "bwf", "sony-xml", "braw-sidecar", "ale", "ffprobe"}

type Settings struct {
	Fingerprint bool     `json:"fingerprint"`
	Extractors  []string `json:"extractors"` // explicit allowlist; empty means no extraction
}

func Default() Settings {
	return Settings{Fingerprint: true, Extractors: slices.Clone(ExtractorNames)}
}

func (s Settings) Validate() error {
	seen := map[string]bool{}
	for _, name := range s.Extractors {
		if !slices.Contains(ExtractorNames, name) {
			return fmt.Errorf("unknown extractor %q", name)
		}
		if seen[name] {
			return fmt.Errorf("duplicate extractor %q", name)
		}
		seen[name] = true
	}
	return nil
}

// Profile may exist before the first scan. Indexed is only true after ingest.
// Name and taxonomy are read from the drive once it is in the catalog, keeping
// edits made in the web UI authoritative too.
type Profile struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	VolumeName string `json:"volume_name"`
	GroupID    int64  `json:"group_id"`
	ClientID   int64  `json:"client_id"`
	Settings
	Configured bool  `json:"configured"`
	Indexed    bool  `json:"indexed"`
	Revision   int64 `json:"revision"`
}
