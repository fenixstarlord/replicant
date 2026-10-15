package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fenixstarlord/replicant/internal/scanner"
)

func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errors.New("upload failed"), exitFailed},
		{fmt.Errorf("extract: %w", context.Canceled), exitCancelled},
		{fmt.Errorf("%w: /Volumes/CARD", scanner.ErrRootGone), exitDriveGone},
	} {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func TestCancelWhenOrphaned(t *testing.T) {
	var ppid atomic.Int64
	ppid.Store(4242)
	ctx := cancelWhenOrphaned(context.Background(), func() int { return int(ppid.Load()) }, 5*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("cancelled while the parent is alive")
	}
	ppid.Store(1) // re-parented to launchd: the app is gone
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("not cancelled after the parent exited")
	}
}
