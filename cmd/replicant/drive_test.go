package main

import (
	"testing"

	"github.com/fenixstarlord/replicant/internal/store"
)

func TestDriveInfoLookup(t *testing.T) {
	drives := []store.Drive{
		{ID: 1, VolumeUUID: "AAAA", Name: "Card A"},
		{ID: 2, VolumeUUID: "name:TAPE|ltfs", Name: "Tape 7", Renamed: true},
	}
	for _, tc := range []struct {
		key     string
		known   bool
		name    string
		renamed bool
	}{
		{"AAAA", true, "Card A", false},
		{"name:TAPE|ltfs", true, "Tape 7", true},
		{"BBBB", false, "", false},
	} {
		d := driveInfo{Key: tc.key}
		d.lookup(drives)
		if d.Known != tc.known || d.Name != tc.name || d.Renamed != tc.renamed {
			t.Errorf("%s: %+v", tc.key, d)
		}
	}
}
