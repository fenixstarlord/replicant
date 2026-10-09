//go:build !darwin

package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// statVolume uses statfs for capacity and /proc/self/mounts for the mount
// point and filesystem type (longest matching mount prefix).
func statVolume(path string) (Volume, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Volume{}, err
	}
	v := Volume{
		TotalBytes: int64(st.Blocks) * int64(st.Bsize),
		FreeBytes:  int64(st.Bavail) * int64(st.Bsize),
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return v, nil
	}
	if f, err := os.Open("/proc/self/mounts"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 3 {
				continue
			}
			mp := strings.ReplaceAll(fields[1], `\040`, " ")
			if (abs == mp || strings.HasPrefix(abs, strings.TrimRight(mp, "/")+"/")) && len(mp) >= len(v.MountPoint) {
				v.MountPoint, v.FSType, v.Device = mp, fields[2], fields[0]
			}
		}
	}
	if v.MountPoint == "" {
		v.MountPoint = "/"
	}
	v.Name = filepath.Base(v.MountPoint)
	return v, nil
}

// fillVolumeDetails has nothing to add outside macOS.
func fillVolumeDetails(*Volume) {}
