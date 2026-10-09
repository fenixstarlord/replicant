package sched

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fenixstarlord/indexserver/internal/store"
)

func TestRunJobScansAndIngests(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "A001"), 0o755)
	os.WriteFile(filepath.Join(root, "A001", "A001C001.mov"), []byte("x"), 0o644)
	ctx := context.Background()
	id, err := st.CreateScanJob(ctx, store.ScanJob{Path: root, Label: "test", IntervalMin: 60, Fingerprint: true, Extract: false, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, "test")
	res, err := s.RunJob(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 1 || res.Clips != 1 || res.ScanID == 0 {
		t.Errorf("ingest result wrong: %+v", res)
	}
	j, _ := st.GetScanJob(ctx, id)
	if j.LastStatus != "ok" || j.LastScanID != res.ScanID || j.NextRunAt == nil || j.LastDuration <= 0 {
		t.Errorf("job state wrong: %+v", j)
	}
	drives, _ := st.ListDrives(ctx)
	if len(drives) != 1 || drives[0].FileCount != 1 {
		t.Errorf("drive not created: %+v", drives)
	}
	if _, _, _, _, running := s.Running(id); running {
		t.Error("job still marked running")
	}

	// A missing path records an error without crashing.
	bad, _ := st.CreateScanJob(ctx, store.ScanJob{Path: filepath.Join(root, "nope"), Enabled: true})
	if _, err := s.RunJob(ctx, bad); err == nil {
		t.Error("expected error for missing path")
	}
	if j, _ := st.GetScanJob(ctx, bad); j.LastStatus != "error" || j.LastError == "" {
		t.Errorf("error not recorded: %+v", j)
	}

	// The loop picks up due jobs.
	cctx, cancel := context.WithCancel(ctx)
	s.Start(cctx)
	s.Kick()
	time.Sleep(300 * time.Millisecond)
	cancel()
}
