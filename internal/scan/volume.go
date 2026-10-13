package scan

import "path/filepath"

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
	// LTFS is set when the volume is an LTFS tape.
	LTFS bool `json:"ltfs,omitempty"`
}

// Key identifies the drive in the catalog: the volume UUID when there is
// one, otherwise a fallback built from the volume name and filesystem.
func (v Volume) Key() string {
	if v.UUID != "" {
		return v.UUID
	}
	return "name:" + v.Name + "|" + v.FSType
}

// VolumeInfo describes the volume containing path. Mount point,
// filesystem type, and capacity come from the OS; on macOS the UUID,
// volume name, and device details come from diskutil. On an LTFS tape the
// tape's own UUID and volume name are used instead, since they stay the
// same across mounts and machines. Missing details are left empty rather
// than failing.
func VolumeInfo(path string) (Volume, error) {
	v, err := statVolume(path)
	if err != nil {
		return Volume{}, err
	}
	fillVolumeDetails(&v)
	if v.Name == "" && v.MountPoint != "" && v.MountPoint != "/" {
		// diskutil knows nothing about network shares and FUSE mounts;
		// name them after their mount point, as on Linux, so they do not
		// all share one catalog key.
		v.Name = filepath.Base(v.MountPoint)
	}
	tape, ok := DetectLTFS(path)
	if !ok && v.MountPoint != "" {
		tape, ok = DetectLTFS(v.MountPoint)
	}
	if ok {
		v.LTFS = true
		v.FSType = "ltfs"
		v.UUID = tape.UUID
		if tape.Name != "" {
			v.Name = tape.Name
		}
	}
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
