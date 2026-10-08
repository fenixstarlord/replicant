//go:build darwin

package scan

import (
	"io/fs"
	"syscall"
	"time"
)

// birthTime returns the file creation time if the filesystem records one.
func birthTime(fi fs.FileInfo) *time.Time {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	t := time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec).UTC()
	if unixZero(t) {
		return nil
	}
	return &t
}
