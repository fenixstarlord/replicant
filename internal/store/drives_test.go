package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDriveNames(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	write(t, root, "A001/A001C001.mov", "aaaa")
	t0 := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

	ingest := func(volName, chosen string, at time.Time) IngestResult {
		t.Helper()
		b := makeBundle(t, root, at)
		b.Manifest.Volume.Name = volName
		b.Manifest.DriveName = chosen
		res, err := s.Ingest(ctx, b)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	drive := func(id int64) Drive {
		t.Helper()
		d, err := s.GetDrive(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	check := func(step string, d Drive, name, volName string, renamed bool) {
		t.Helper()
		if d.Name != name || d.VolumeName != volName || d.Renamed != renamed {
			t.Errorf("%s: name %q volume %q renamed %v, want %q %q %v", step, d.Name, d.VolumeName, d.Renamed, name, volName, renamed)
		}
	}

	// A new drive takes the volume's name, and follows it while not renamed.
	res := ingest("CARD_A", "", t0)
	check("first scan", drive(res.DriveID), "CARD_A", "CARD_A", false)
	if res.DriveName != "CARD_A" {
		t.Errorf("ingest result name %q", res.DriveName)
	}
	res = ingest("CARD_A2", "", t0.Add(time.Hour))
	check("volume renamed on disk", drive(res.DriveID), "CARD_A2", "CARD_A2", false)

	// Renamed on the server: later scans keep the catalog name.
	if err := s.RenameDrive(ctx, res.DriveID, "  Day 1 · A cam  "); err != nil {
		t.Fatal(err)
	}
	check("renamed", drive(res.DriveID), "Day 1 · A cam", "CARD_A2", true)
	res = ingest("CARD_A3", "", t0.Add(2*time.Hour))
	check("scan after rename", drive(res.DriveID), "Day 1 · A cam", "CARD_A3", true)
	if res.DriveName != "Day 1 · A cam" {
		t.Errorf("ingest result name %q, want the catalog name", res.DriveName)
	}

	// A name chosen in the scanner wins over both.
	res = ingest("CARD_A3", "A cam, day 1", t0.Add(3*time.Hour))
	check("named by the scanner", drive(res.DriveID), "A cam, day 1", "CARD_A3", true)

	// Choosing the volume's own name at scan time means "follow the volume".
	res = ingest("CARD_A3", "CARD_A3", t0.Add(4*time.Hour))
	check("named after the volume", drive(res.DriveID), "CARD_A3", "CARD_A3", false)
	res = ingest("CARD_A4", "", t0.Add(5*time.Hour))
	check("follows the volume again", drive(res.DriveID), "CARD_A4", "CARD_A4", false)

	// A bundle older than the newest scan, ingested late, changes nothing
	// about the drive: not its name, volume name, rename, or capacity.
	if err := s.RenameDrive(ctx, res.DriveID, "Server name"); err != nil {
		t.Fatal(err)
	}
	late := makeBundle(t, root, t0.Add(-48*time.Hour))
	late.Manifest.Volume.Name = "CARD_OLD"
	late.Manifest.Volume.TotalBytes = 1
	late.Manifest.DriveName = "Old scanner name"
	if r, err := s.Ingest(ctx, late); err != nil || r.IsLatest || r.DriveName != "Server name" {
		t.Fatalf("late ingest = %+v, %v", r, err)
	}
	d := drive(res.DriveID)
	check("late bundle", d, "Server name", "CARD_A4", true)
	if d.CapacityBytes != 1000 {
		t.Errorf("late bundle changed capacity to %d", d.CapacityBytes)
	}

	// Clearing the name goes back to the volume's name.
	if err := s.RenameDrive(ctx, res.DriveID, ""); err != nil {
		t.Fatal(err)
	}
	check("name cleared", drive(res.DriveID), "CARD_A4", "CARD_A4", false)

	if err := s.RenameDrive(ctx, 9999, "x"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("unknown drive: %v", err)
	}
}

// A scan reported through an activity shows in the history under the
// drive's catalog name.
func TestIngestNamesActivity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	write(t, root, "a.mov", "a")
	b := makeBundle(t, root, time.Now())
	b.Manifest.DriveName = "Shelf copy"
	id, err := s.StartActivity(ctx, Activity{Host: "mac", DriveName: "X", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestFrom(ctx, b, "test", id); err != nil {
		t.Fatal(err)
	}
	var name, status string
	if err := s.DB.QueryRow(`SELECT drive_name, status FROM activity WHERE id = ?`, id).Scan(&name, &status); err != nil {
		t.Fatal(err)
	}
	if name != "Shelf copy" || status != "done" {
		t.Errorf("activity = %q %q", name, status)
	}

	// History follows a later rename of the drive.
	acts, err := s.queryActivity(ctx, "WHERE a.id = ?", id)
	if err != nil || len(acts) != 1 {
		t.Fatalf("activity query: %v %d", err, len(acts))
	}
	if err := s.RenameDrive(ctx, *acts[0].DriveID, "Renamed later"); err != nil {
		t.Fatal(err)
	}
	if acts, _ = s.queryActivity(ctx, "WHERE a.id = ?", id); acts[0].DriveName != "Renamed later" {
		t.Errorf("history shows %q after a rename", acts[0].DriveName)
	}
}
