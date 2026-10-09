package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunProducesBundle(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "A001"), 0o755)
	os.WriteFile(filepath.Join(root, "A001", "A001C001.mov"), []byte("not really a movie"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("n"), 0o644)
	var stages []string
	res, err := Run(context.Background(), root, Options{Fingerprint: true, Version: "test"}, func(p Progress) { stages = append(stages, p.Stage) })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 || len(res.Clips) != 1 || res.Volume.MountPoint == "" || res.Root != root {
		t.Errorf("result wrong: %d entries %d clips vol=%+v", len(res.Entries), len(res.Clips), res.Volume)
	}
	if len(res.Extractors) == 0 {
		t.Errorf("extractors not reported")
	}
	b := res.Bundle()
	if b.Manifest.ScannerVersion != "test" || b.Manifest.Root != root || !b.Manifest.Options.Fingerprint || b.Manifest.ClipCount != 1 || b.Manifest.Summary.Files != 2 {
		t.Errorf("manifest wrong: %+v", b.Manifest)
	}
	// ffprobe fails on the fake movie, so the clip carries an error but survives.
	if c := res.Clips[0]; c.Name != "A001C001" {
		t.Errorf("clip wrong: %+v", c)
	}
	if len(stages) == 0 {
		t.Errorf("no progress reported")
	}
	fast, err := Run(context.Background(), root, Options{Fast: true}, nil)
	if err != nil || fast.Extractors != nil || fast.Clips[0].Meta != nil {
		t.Errorf("fast run should skip extraction: %v %+v", err, fast.Clips[0])
	}
}

func TestMountsDoesNotPanic(t *testing.T) {
	_ = Mounts()
}
