// Package scan walks a mounted volume and describes what it finds.
//
// Everything in this package is strictly read-only with respect to the
// scanned tree: it opens files for reading and stats them, nothing else.
package scan

import (
	"path/filepath"
	"strings"
	"time"
)

// Kind is a coarse classification of an entry by its extension.
type Kind string

// Entry kinds. Directories are KindDir; files that match no known
// extension are KindOther.
const (
	KindDir     Kind = "dir"
	KindVideo   Kind = "video"
	KindAudio   Kind = "audio"
	KindSidecar Kind = "sidecar"
	KindImage   Kind = "image"
	KindProject Kind = "project"
	KindOther   Kind = "other"
)

// Entry is one file, directory, package, or symlink found on a volume.
// Path is relative to the scan root and always uses forward slashes.
type Entry struct {
	Path        string     `json:"path"`
	Name        string     `json:"name"`
	Ext         string     `json:"ext,omitempty"`
	Kind        Kind       `json:"kind"`
	IsDir       bool       `json:"is_dir"`
	IsPackage   bool       `json:"is_package,omitempty"`
	IsSymlink   bool       `json:"is_symlink,omitempty"`
	Size        int64      `json:"size"`
	ModTime     time.Time  `json:"mtime"`
	BirthTime   *time.Time `json:"btime,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	FullHash    string     `json:"full_hash,omitempty"`
	Error       string     `json:"error,omitempty"`
	// ClipID links the entry to a clip after grouping (see package clips).
	ClipID string `json:"clip_id,omitempty"`
}

// Dir returns the parent directory of the entry's path ("" at the root).
func (e Entry) Dir() string {
	if i := strings.LastIndexByte(e.Path, '/'); i >= 0 {
		return e.Path[:i]
	}
	return ""
}

// Stem returns the file name without its extension.
func (e Entry) Stem() string {
	return strings.TrimSuffix(e.Name, filepath.Ext(e.Name))
}

var kindByExt = map[string]Kind{
	// video and camera raw
	"mov": KindVideo, "mp4": KindVideo, "m4v": KindVideo, "mxf": KindVideo, "avi": KindVideo,
	"mkv": KindVideo, "webm": KindVideo, "mts": KindVideo, "m2ts": KindVideo, "mpg": KindVideo,
	"mpeg": KindVideo, "r3d": KindVideo, "braw": KindVideo, "ari": KindVideo, "arx": KindVideo,
	"crm": KindVideo, "cine": KindVideo,
	// audio
	"wav": KindAudio, "bwf": KindAudio, "aif": KindAudio, "aiff": KindAudio, "flac": KindAudio,
	"mp3": KindAudio, "aac": KindAudio, "m4a": KindAudio, "caf": KindAudio, "ogg": KindAudio,
	// sidecars and metadata
	"ale": KindSidecar, "sidecar": KindSidecar, "xml": KindSidecar, "rmd": KindSidecar,
	"mhl": KindSidecar, "md5": KindSidecar, "xmp": KindSidecar, "cdl": KindSidecar,
	"cube": KindSidecar, "bim": KindSidecar, "smi": KindSidecar, "ppn": KindSidecar,
	"mif": KindSidecar, "txt": KindSidecar, "csv": KindSidecar, "json": KindSidecar,
	// images and image sequences
	"jpg": KindImage, "jpeg": KindImage, "png": KindImage, "tif": KindImage, "tiff": KindImage,
	"dpx": KindImage, "exr": KindImage, "dng": KindImage, "cr2": KindImage, "cr3": KindImage,
	"arw": KindImage, "raf": KindImage, "nef": KindImage, "heic": KindImage, "gif": KindImage,
	"psd": KindImage, "bmp": KindImage,
	// editing and grading projects
	"drp": KindProject, "drt": KindProject, "fcpxml": KindProject, "fcpbundle": KindProject,
	"prproj": KindProject, "aep": KindProject, "edl": KindProject, "aaf": KindProject,
	"otio": KindProject, "dra": KindProject,
}

// KindOf classifies a file by its extension.
func KindOf(name string) Kind {
	if k, ok := kindByExt[extOf(name)]; ok {
		return k
	}
	return KindOther
}

// extOf returns the lowercase extension of name without the leading dot.
func extOf(name string) string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
}

// Summary aggregates counts over a set of entries.
type Summary struct {
	Files    int          `json:"files"`
	Dirs     int          `json:"dirs"`
	Packages int          `json:"packages"`
	Symlinks int          `json:"symlinks"`
	Errors   int          `json:"errors"`
	Bytes    int64        `json:"bytes"`
	ByKind   map[Kind]int `json:"by_kind"`
}

// Summarize counts entries by type and kind.
func Summarize(entries []Entry) Summary {
	s := Summary{ByKind: map[Kind]int{}}
	for _, e := range entries {
		if e.Error != "" {
			s.Errors++
		}
		switch {
		case e.IsSymlink:
			s.Symlinks++
		case e.IsPackage:
			s.Packages++
			s.Bytes += e.Size
		case e.IsDir:
			s.Dirs++
		default:
			s.Files++
			s.Bytes += e.Size
			s.ByKind[e.Kind]++
		}
	}
	return s
}
