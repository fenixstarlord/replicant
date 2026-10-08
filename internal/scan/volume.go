package scan

import (
	"golang.org/x/sys/unix"
)

// Volume identifies the filesystem that contains a scanned path.
type Volume struct {
	MountPoint string `json:"mount_point"`
	UUID       string `json:"uuid,omitempty"`
	Name       string `json:"name,omitempty"`
	FSType     string `json:"fs_type,omitempty"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	Device     string `json:"device,omitempty"`
	MediaName  string `json:"media_name,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

// VolumeInfo describes the volume containing path. Mount point, filesystem
// type, and capacity come from statfs and are always filled. On macOS the
// UUID, volume name, and device details come from diskutil; if diskutil is
// unavailable those fields stay empty and no error is returned.
func VolumeInfo(path string) (Volume, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Volume{}, err
	}
	v := Volume{
		MountPoint: cstr(st.Mntonname[:]),
		FSType:     cstr(st.Fstypename[:]),
		TotalBytes: int64(st.Blocks) * int64(st.Bsize),
		FreeBytes:  int64(st.Bavail) * int64(st.Bsize),
	}
	fillVolumeDetails(&v)
	return v, nil
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
