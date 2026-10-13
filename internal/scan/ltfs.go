package scan

import (
	"errors"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// LTFS tapes keep their whole index in memory once mounted and expose parts
// of it as virtual extended attributes (LTFS Format 2.4, Annex C). Reading
// them never moves the tape. They are never returned by listxattr, so they
// are read by exact name; ltfsAttrPrefix adds the platform prefix.

// LTFSVolume identifies an LTFS tape from the virtual attributes of any
// path on it.
type LTFSVolume struct {
	UUID string
	Name string
}

// DetectLTFS reports whether path is on an LTFS volume, by reading the
// ltfs.volumeUUID attribute.
func DetectLTFS(path string) (LTFSVolume, bool) {
	uuid, err := ltfsAttr(path, "volumeUUID")
	if err != nil || uuid == "" {
		return LTFSVolume{}, false
	}
	name, _ := ltfsAttr(path, "volumeName")
	return LTFSVolume{UUID: uuid, Name: name}, true
}

// TapePosition is where a file's first extent starts on tape. Reading files
// in (Partition, Block) order makes the drive move forward only.
type TapePosition struct {
	Partition string `json:"partition"` // LTFS partition letter, "a" (index) or "b" (data)
	Block     int64  `json:"block"`
}

// Less orders positions by partition, then block.
func (p TapePosition) Less(q TapePosition) bool {
	if p.Partition != q.Partition {
		return p.Partition < q.Partition
	}
	return p.Block < q.Block
}

// ReadTapePosition returns the tape position of the file at path from the
// ltfs.partition and ltfs.startblock attributes. It reports false for
// empty files (which have no extents) and on any filesystem that is not
// LTFS.
func ReadTapePosition(path string) (TapePosition, bool) {
	part, err := ltfsAttr(path, "partition")
	if err != nil || part == "" {
		return TapePosition{}, false
	}
	blk, err := ltfsAttr(path, "startblock")
	if err != nil {
		return TapePosition{}, false
	}
	n, err := strconv.ParseInt(blk, 10, 64)
	if err != nil {
		return TapePosition{}, false
	}
	return TapePosition{Partition: part, Block: n}, true
}

// ltfsAttr reads the virtual attribute ltfs.<name> of path.
func ltfsAttr(path, name string) (string, error) {
	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, ltfsAttrPrefix+"ltfs."+name, buf)
	if errors.Is(err, unix.ERANGE) {
		if n, err = unix.Getxattr(path, ltfsAttrPrefix+"ltfs."+name, nil); err == nil {
			buf = make([]byte, n)
			n, err = unix.Getxattr(path, ltfsAttrPrefix+"ltfs."+name, buf)
		}
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(buf[:n]), "\x00 \n"), nil
}
