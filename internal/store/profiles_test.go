package store

import (
	"errors"
	"testing"
	"time"

	"github.com/fenixstarlord/replicant/internal/scanprofile"
)

func TestDriveProfileBeforeAndAfterIngest(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	u := ProfileUpdate{Profile: scanprofile.Profile{Key: "UUID-X", Name: "EGG09", VolumeName: "1097007720",
		Settings: scanprofile.Settings{Extractors: []string{"ale", "bwf"}}}, NewClient: "Client A", NewGroup: "Shelf 2"}
	p, err := s.SaveDriveProfile(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Configured || p.Indexed || p.Revision != 1 || p.GroupID == 0 || p.ClientID == 0 {
		t.Fatalf("saved profile: %+v", p)
	}
	drives, err := s.ListDrives(ctx)
	if err != nil || len(drives) != 0 {
		t.Fatalf("saving settings created a catalog drive: %+v, %v", drives, err)
	}
	// An independent read (another Mac) gets the same settings before any scan.
	other, err := s.DriveProfile(ctx, u.Key)
	if err != nil || other.Name != "EGG09" || other.Fingerprint || len(other.Extractors) != 2 {
		t.Fatalf("other client: %+v %v", other, err)
	}
	b := makeBundle(t, t.TempDir(), time.Now())
	b.Manifest.Volume.Name = "1097007720"
	b.Manifest.DriveName = "stale client name"
	r, err := s.Ingest(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.DriveProfile(ctx, u.Key)
	if err != nil || !p.Indexed || p.Name != "EGG09" || p.GroupID != other.GroupID || p.ClientID != other.ClientID {
		t.Fatalf("ingested profile: %+v %v", p, err)
	}
	if err := s.RenameDrive(ctx, r.DriveID, "Edited on web"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDriveGroupID(ctx, ByGroup, r.DriveID, 0); err != nil {
		t.Fatal(err)
	}
	b.Manifest.ScannedAt = time.Now().Add(time.Minute)
	if _, err := s.Ingest(ctx, b); err != nil {
		t.Fatal(err)
	}
	p, err = s.DriveProfile(ctx, u.Key)
	if err != nil || p.Name != "Edited on web" || p.GroupID != 0 {
		t.Fatalf("rescan overwrote web edits: %+v %v", p, err)
	}
	// Profile edits also immediately update the drive and can disable all tools.
	p.Name = "Next name"
	p.Extractors = []string{}
	p, err = s.SaveDriveProfile(ctx, ProfileUpdate{Profile: p})
	if err != nil || p.Name != "Next name" || len(p.Extractors) != 0 || p.Revision != 2 {
		t.Fatalf("edit: %+v %v", p, err)
	}
	if _, err := s.SaveDriveProfile(ctx, ProfileUpdate{Profile: other}); !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("stale edit: %v", err)
	}
}

func TestDriveProfileValidationAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*ProfileUpdate)
	}{
		{"empty key", func(u *ProfileUpdate) { u.Key = "" }},
		{"unknown tool", func(u *ProfileUpdate) { u.Extractors = []string{"typo"} }},
		{"unknown client", func(u *ProfileUpdate) { u.ClientID = 999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			u := ProfileUpdate{Profile: scanprofile.Profile{Key: "K", Settings: scanprofile.Default()}, NewGroup: "Must roll back"}
			tc.modify(&u)
			if _, err := s.SaveDriveProfile(t.Context(), u); err == nil {
				t.Fatal("accepted invalid profile")
			}
			profiles, err := s.ListDriveProfiles(t.Context())
			if err != nil || len(profiles) != 0 {
				t.Fatalf("profile leaked: %v %v", profiles, err)
			}
			groups, err := s.ListGroups(t.Context(), ByGroup)
			if err != nil || len(groups) != 0 {
				t.Fatalf("group leaked: %v %v", groups, err)
			}
		})
	}
}
