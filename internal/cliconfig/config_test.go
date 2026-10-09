package cliconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cfg", "config.toml")
	t.Setenv("REPLICANT_CONFIG", p)
	c, err := Load()
	if err != nil || c.Server != "" {
		t.Fatalf("missing file should load empty: %+v %v", c, err)
	}
	want := Config{Server: "http://x:8080", Token: "replicant_abc", Tools: Tools{FFprobe: "/opt/ffprobe"}}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 600", fi.Mode().Perm())
	}
	got, err := Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v (%v), want %+v", got, err, want)
	}
}
