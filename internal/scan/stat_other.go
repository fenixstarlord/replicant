//go:build !darwin

package scan

import (
	"io/fs"
	"time"
)

// birthTime is only available on macOS; other platforms report nothing.
func birthTime(fs.FileInfo) *time.Time { return nil }
