//go:build darwin

package scan

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
	"howett.net/plist"
)

func statVolume(path string) (Volume, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Volume{}, err
	}
	return Volume{
		MountPoint: cstr(st.Mntonname[:]),
		FSType:     cstr(st.Fstypename[:]),
		TotalBytes: int64(st.Blocks) * int64(st.Bsize),
		FreeBytes:  int64(st.Bavail) * int64(st.Bsize),
	}, nil
}

// diskutilInfo is the subset of `diskutil info -plist` we use.
type diskutilInfo struct {
	VolumeUUID       string `plist:"VolumeUUID"`
	VolumeName       string `plist:"VolumeName"`
	FilesystemName   string `plist:"FilesystemName"`
	DeviceNode       string `plist:"DeviceNode"`
	DeviceIdentifier string `plist:"DeviceIdentifier"`
	MediaName        string `plist:"MediaName"`
	BusProtocol      string `plist:"BusProtocol"`
}

// fillVolumeDetails adds UUID, name, and device details from diskutil.
func fillVolumeDetails(v *Volume) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "diskutil", "info", "-plist", v.MountPoint).Output()
	if err != nil {
		return
	}
	var info diskutilInfo
	if _, err := plist.Unmarshal(bytes.TrimSpace(out), &info); err != nil {
		return
	}
	v.UUID = info.VolumeUUID
	v.Name = info.VolumeName
	if info.FilesystemName != "" {
		v.FSType = info.FilesystemName
	}
	if info.DeviceNode != "" {
		v.Device = info.DeviceNode
	} else {
		v.Device = info.DeviceIdentifier
	}
	v.MediaName = info.MediaName
	v.Protocol = info.BusProtocol
}
