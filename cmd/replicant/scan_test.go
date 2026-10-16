package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fenixstarlord/replicant/internal/scanner"
)

func TestTapeProgressOnSmallScan(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "clip.mxf")
	content := []byte("synthetic media placeholder")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REPLICANT_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	cmd := newScanCmd()
	var out bytes.Buffer
	cmd.SetErr(&out)
	f := &scanFlags{fast: true, tape: tapeFlag{mode: scanner.TapeOn}}
	res, err := runScan(t.Context(), cmd, root, f, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`tape hash 0/1: hash "clip.mxf" — start`,
		`tape hash 1/1: hash "clip.mxf" — finish`,
		finishedReadingLine,
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in progress:\n%s", want, &out)
		}
	}
	if res.Entries[0].Fingerprint == "" {
		t.Error("fast scan did not fingerprint")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("scanned file changed: %q, %v", got, err)
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 1 {
		t.Fatalf("scan wrote to volume: %v, %v", files, err)
	}
}
