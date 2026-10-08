package scan

import (
	"runtime"
	"testing"
)

func TestVolumeInfo(t *testing.T) {
	v, err := VolumeInfo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if v.MountPoint == "" || v.FSType == "" || v.TotalBytes <= 0 || v.FreeBytes < 0 || v.FreeBytes > v.TotalBytes {
		t.Errorf("implausible volume: %+v", v)
	}
	if runtime.GOOS == "darwin" && (v.UUID == "" || v.Name == "") {
		t.Errorf("expected diskutil to fill UUID and name on macOS: %+v", v)
	}
	if _, err := VolumeInfo("/definitely/not/here"); err == nil {
		t.Error("expected error for missing path")
	}
}
