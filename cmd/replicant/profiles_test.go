package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/fenixstarlord/replicant/internal/bundle"
	"github.com/fenixstarlord/replicant/internal/cliconfig"
	"github.com/fenixstarlord/replicant/internal/client"
	"github.com/fenixstarlord/replicant/internal/scanprofile"
)

func TestScanSharedProfileAndExplicitOverrides(t *testing.T) {
	for _, override := range []bool{false, true} {
		name := "server settings"
		if override {
			name = "explicit override"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("REPLICANT_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/drive-profiles" || r.URL.Query().Get("key") == "" {
					t.Errorf("unexpected request: %s", r.URL)
				}
				calls.Add(1)
				json.NewEncoder(w).Encode(scanprofile.Profile{Key: r.URL.Query().Get("key"), Configured: true,
					Settings: scanprofile.Settings{Extractors: []string{"ale"}}})
			}))
			defer srv.Close()
			if err := cliconfig.Save(cliconfig.Config{Server: srv.URL}); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "clip.mov"), []byte("test placeholder"), 0o600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "test.replicant")
			args := []string{"--profile", "-o", output, root}
			if override {
				args = append([]string{"--no-fingerprint=false", "--extractors="}, args...)
			}
			cmd := newScanCmd()
			var log bytes.Buffer
			cmd.SetOut(&log)
			cmd.SetErr(&log)
			cmd.SetArgs(args)
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("%v: %s", err, &log)
			}
			f, err := os.Open(output)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			st, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			b, err := bundle.Read(f, st.Size())
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || b.Manifest.Options.Fingerprint != override {
				t.Fatalf("profile not applied: %+v calls=%d", b.Manifest.Options, calls.Load())
			}
			if (b.Entries[0].Fingerprint != "") != override {
				t.Fatal("fingerprint selection not applied")
			}
			if override && (b.Manifest.Options.Extractors == nil || len(b.Manifest.Extractors) != 0) {
				t.Fatal("explicit empty extractor list was lost")
			}
		})
	}
}

func TestProfileCacheIsServerScoped(t *testing.T) {
	t.Setenv("REPLICANT_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	c, err := client.New(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	p := scanprofile.Profile{Key: "K", Configured: true, Settings: scanprofile.Settings{Extractors: []string{}}}
	cacheProfile(c.Server, p)
	var log bytes.Buffer
	got, err := resolveProfile(t.Context(), c, "K", &log)
	if err != nil || got.Fingerprint || len(got.Extractors) != 0 || log.Len() == 0 {
		t.Fatalf("cached settings: %+v, %v", got, err)
	}
	if _, err := resolveProfile(t.Context(), c, "different-drive", &log); err == nil {
		t.Fatal("used another drive's cached settings")
	}
	if len(cachedProfiles("https://another-server").Profiles) != 0 {
		t.Fatal("cache crossed servers")
	}
}
