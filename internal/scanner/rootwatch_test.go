package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatchRootNoticesRemoval(t *testing.T) {
	old := rootWatchInterval
	rootWatchInterval = 10 * time.Millisecond
	t.Cleanup(func() { rootWatchInterval = old })

	root := filepath.Join(t.TempDir(), "CARD")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, gone, stop, err := watchRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if gone() {
		t.Fatal("root reported gone while it is there")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("removal not noticed")
	}
	if !errors.Is(context.Cause(ctx), ErrRootGone) || !gone() {
		t.Errorf("cause = %v", context.Cause(ctx))
	}
	if _, _, _, err := watchRoot(context.Background(), root); err == nil {
		t.Error("watching a missing root should fail")
	}
}

// A drive pulled out mid-scan must fail the scan, not produce a bundle in
// which every file is missing.
func TestRunFailsWhenRootGoesAway(t *testing.T) {
	old := rootWatchInterval
	rootWatchInterval = 10 * time.Millisecond
	t.Cleanup(func() { rootWatchInterval = old })

	for _, tape := range []TapeMode{TapeOff, TapeOn} {
		root := buildMediaTree(t)
		_, err := Run(context.Background(), root, Options{Fingerprint: true, Tape: tape}, func(p Progress) {
			if p.Stage == "walk" {
				if err := os.RemoveAll(root); err != nil {
					t.Error(err)
				}
			}
		})
		if !errors.Is(err, ErrRootGone) {
			t.Errorf("tape=%s: err = %v, want ErrRootGone", tape, err)
		}
	}

	// A scan whose root stays put is unaffected.
	if _, err := Run(context.Background(), buildMediaTree(t), Options{Fast: true, Fingerprint: true}, nil); err != nil {
		t.Errorf("normal scan: %v", err)
	}
}

// A drive pulled out within the last check interval is still caught by the
// final look, whether the scan then fails or finishes.
func TestRunChecksRootAtTheEnd(t *testing.T) {
	old := rootWatchInterval
	rootWatchInterval = time.Hour // only the final look can notice
	t.Cleanup(func() { rootWatchInterval = old })
	root := buildMediaTree(t)
	_, err := Run(context.Background(), root, Options{Fast: true, Fingerprint: true}, func(p Progress) {
		if p.Stage == "walk" {
			os.RemoveAll(root)
		}
	})
	if !errors.Is(err, ErrRootGone) {
		t.Errorf("err = %v, want ErrRootGone", err)
	}
}

// An error that persists (here: the root's folder made unreadable) counts
// as gone after a few checks; a cancelled scan is not reported as gone.
func TestWatchRootPersistentErrorAndCancel(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	old := rootWatchInterval
	rootWatchInterval = 10 * time.Millisecond
	t.Cleanup(func() { rootWatchInterval = old })

	parent := t.TempDir()
	root := filepath.Join(parent, "CARD")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, gone, stop, err := watchRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })
	if gone() {
		t.Error("an unreadable root is not certainly gone")
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a persistent error was not treated as gone")
	}
	if !errors.Is(context.Cause(ctx), ErrRootGone) {
		t.Errorf("cause = %v", context.Cause(ctx))
	}
	os.Chmod(parent, 0o755)

	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(cctx, buildMediaTree(t), Options{Fast: true}, nil); errors.Is(err, ErrRootGone) || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled scan: %v", err)
	}
}
