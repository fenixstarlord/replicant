package scanner

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"time"
)

// ErrRootGone means the scanned folder went away during the scan, usually
// because its drive was ejected or unplugged. The scan is abandoned
// rather than stored with every file missing.
var ErrRootGone = errors.New("the scanned drive or folder went away during the scan")

// rootWatchInterval is how often a running scan checks its root; tests
// shorten it.
var rootWatchInterval = time.Second

// rootErrorChecks is how many checks in a row must fail with an error
// other than "does not exist" before the root counts as gone, so a
// network or FUSE hiccup does not abandon a scan.
const rootErrorChecks = 3

// watchRoot returns a context that is cancelled with ErrRootGone once root
// is no longer the directory it was when the scan started: removed, or
// left as an empty mount point on another filesystem after an unmount, or
// unreadable for several checks in a row. gone reports a removal or a
// different filesystem on demand; stop ends the watch.
func watchRoot(parent context.Context, root string) (ctx context.Context, gone func() bool, stop func(), err error) {
	dev, err := deviceOf(root)
	if err != nil {
		return nil, nil, nil, err
	}
	// state: 0 still there, 1 certainly gone, 2 could not tell.
	state := func() int {
		d, err := deviceOf(root)
		switch {
		case err == nil && d == dev:
			return 0
		case err == nil, errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			return 1
		}
		return 2
	}
	ctx, cancel := context.WithCancelCause(parent)
	every := rootWatchInterval
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		failed := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				switch state() {
				case 0:
					failed = 0
				case 1:
					cancel(ErrRootGone)
					return
				case 2:
					if failed++; failed >= rootErrorChecks {
						cancel(ErrRootGone)
						return
					}
				}
			}
		}
	}()
	return ctx, func() bool { return state() == 1 }, func() { cancel(nil) }, nil
}

// deviceOf returns the ID of the filesystem holding path.
func deviceOf(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), nil // int32 on darwin; only ever compared for equality
	}
	return 0, nil
}
