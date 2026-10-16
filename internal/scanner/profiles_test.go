package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fenixstarlord/replicant/internal/cliconfig"
	"github.com/fenixstarlord/replicant/internal/scanprofile"
)

func TestProfileDisablesDataReadsWithoutDroppingFiles(t *testing.T) {
	for _, mode := range []TapeMode{TapeOff, TapeOn} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"sound.wav", "camera.mov", "notes.txt", "card.ale"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("placeholder"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			called := false
			res, err := Run(t.Context(), root, Options{Fingerprint: true, Tape: mode,
				Profile: func(_ context.Context, key string) (*scanprofile.Settings, error) {
					called = true
					if key == "" {
						t.Fatal("missing volume identity")
					}
					return &scanprofile.Settings{Extractors: []string{}}, nil
				}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !called || len(res.Entries) < 4 || len(res.Clips) != 2 || len(res.Extractors) != 0 {
				t.Fatalf("files or clips lost: %+v", res)
			}
			for _, e := range res.Entries {
				if e.Fingerprint != "" {
					t.Fatal("fingerprinted despite disabled profile")
				}
			}
			m := res.Manifest()
			if m.Options.Fingerprint || m.Options.Extractors == nil || len(m.Options.Extractors) != 0 {
				t.Fatalf("manifest did not record explicit none: %+v", m.Options)
			}
		})
	}
}

func TestSelectedALESkipsExternalTools(t *testing.T) {
	for _, mode := range []TapeMode{TapeOff, TapeOn} {
		t.Run(string(mode), func(t *testing.T) {
			root, tools := t.TempDir(), t.TempDir()
			raw, err := os.ReadFile("../../testdata/sidecars/arri_alexa35_arricore.ale")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "card.ale"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "A_0001C001_260930_084650_c1BY9.mxf"), []byte("placeholder"), 0o600); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(tools, "called")
			tool := filepath.Join(tools, "probe")
			if err := os.WriteFile(tool, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			res, err := Run(t.Context(), root, Options{Tape: mode, Extractors: []string{"ale"}, Tools: cliconfig.Tools{FFprobe: tool, ArtCmd: tool, REDline: tool}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Clips) != 1 || res.Clips[0].Meta.DurationS == nil || res.Clips[0].Sources["duration_s"] != "ale" {
				t.Fatal("selected ALE metadata was not extracted")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("disabled tool was launched: %v", err)
			}
		})
	}
}

func TestExpectedVolumeRejectsReplacedDrive(t *testing.T) {
	called := false
	_, err := Run(t.Context(), t.TempDir(), Options{ExpectedVolume: "wrong-volume", Profile: func(context.Context, string) (*scanprofile.Settings, error) {
		called = true
		return nil, nil
	}}, nil)
	if err == nil || called {
		t.Fatalf("did not reject changed volume before profile/read: %v", err)
	}
}
