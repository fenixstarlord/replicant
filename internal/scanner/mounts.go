package scanner

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Mount is a candidate scan root on this machine.
type Mount struct {
	Path   string `json:"path"`
	FSType string `json:"fs_type"`
}

// Mounts lists mounted volumes that look like data drives: /Volumes/* on
// macOS, and on Linux the entries of /proc/self/mounts with a real
// filesystem type, excluding system paths.
func Mounts() []Mount {
	var out []Mount
	if runtime.GOOS == "darwin" {
		entries, _ := os.ReadDir("/Volumes")
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join("/Volumes", e.Name())
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				out = append(out, Mount{Path: p})
			}
		}
		return out
	}
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()
	real := map[string]bool{"ext4": true, "ext3": true, "xfs": true, "btrfs": true, "zfs": true, "exfat": true, "vfat": true,
		"ntfs": true, "ntfs3": true, "fuseblk": true, "hfsplus": true, "apfs": true, "nfs": true, "nfs4": true, "cifs": true, "smb3": true, "f2fs": true}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || !real[fields[2]] {
			continue
		}
		mp := strings.ReplaceAll(fields[1], `\040`, " ")
		switch {
		case mp == "/", strings.HasPrefix(mp, "/boot"), strings.HasPrefix(mp, "/var/lib/docker"), strings.HasPrefix(mp, "/snap"):
			continue
		}
		out = append(out, Mount{Path: mp, FSType: fields[2]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
