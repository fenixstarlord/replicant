// Package clips groups scanned entries into clips: the unit that metadata
// extraction and search operate on. A clip is one media file, or a
// multi-file structure such as a RED .RDC folder, an ARRIRAW frame
// sequence, or a Sony card clip with its XML sidecar.
package clips

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"

	"github.com/fenixstarlord/indexserver/internal/scan"
)

// Kind says what structure a clip was built from.
type Kind string

// Clip kinds.
const (
	KindFile    Kind = "file"    // a single media file, possibly with sidecars
	KindR3D     Kind = "r3d"     // a .RDC folder of R3D segments
	KindARRIRAW Kind = "arriraw" // a sequence of .ari/.arx frames
	KindSony    Kind = "sony"    // an XDROOT/M4ROOT card clip with its XML
	KindBRAW    Kind = "braw"    // a .braw file with its .sidecar
)

// Clip is a group of one or more entries treated as a single item.
type Clip struct {
	ID        string    `json:"id"`
	Kind      Kind      `json:"kind"`
	Name      string    `json:"name"`
	RootPath  string    `json:"root_path"`
	Files     []string  `json:"files"`
	Sidecars  []string  `json:"sidecars,omitempty"`
	FileCount int       `json:"file_count"`
	TotalSize int64     `json:"total_size"`
	ModTime   time.Time `json:"mtime"`
	// Frame sequence details (ARRIRAW only).
	FrameCount int    `json:"frame_count,omitempty"`
	FirstFrame int    `json:"first_frame,omitempty"`
	LastFrame  int    `json:"last_frame,omitempty"`
	FramePath  string `json:"frame_path,omitempty"` // path of the first frame
}

// Options controls grouping.
type Options struct {
	// KeepFrames keeps individual ARRIRAW frame entries in the entry list.
	// By default they are dropped and represented by the clip only.
	KeepFrames bool
}

// sidecarExts are extensions attached to a media file with the same stem.
var sidecarExts = map[string]bool{"sidecar": true, "xml": true, "rmd": true, "xmp": true, "cdl": true}

var frameRe = regexp.MustCompile(`^(.*?)[._-]?(\d+)\.(?i:ari|arx)$`)

// Group builds clips from entries and tags member entries with ClipID.
// It returns the (possibly filtered) entries and the clips, both in
// deterministic order.
func Group(entries []scan.Entry, opts Options) ([]scan.Entry, []Clip) {
	byDir := map[string][]int{} // dir -> indexes of file entries
	dirs := []string{}
	for i, e := range entries {
		if e.IsDir || e.IsSymlink || e.Error != "" {
			continue
		}
		d := e.Dir()
		if _, ok := byDir[d]; !ok {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], i)
	}
	sort.Strings(dirs)

	var clips []Clip
	claimed := map[int]string{} // entry index -> clip id
	dropped := map[int]bool{}

	for _, d := range dirs {
		idx := byDir[d]
		base := path.Base(d)

		// RED: one .RDC folder = one clip, if it holds any R3D segment.
		if strings.EqualFold(path.Ext(base), ".rdc") {
			var files, sidecars []int
			for _, i := range idx {
				switch entries[i].Ext {
				case "r3d":
					files = append(files, i)
				case "rmd":
					sidecars = append(sidecars, i)
				}
			}
			if len(files) > 0 {
				c := newClip(KindR3D, strings.TrimSuffix(base, path.Ext(base)), d, entries, files, sidecars)
				clips = append(clips, c)
				for _, i := range append(files, sidecars...) {
					claimed[i] = c.ID
				}
				continue
			}
		}

		// ARRIRAW: numbered .ari/.arx frames with a common prefix.
		seqs := map[string][]int{}
		var seqOrder []string
		for _, i := range idx {
			if entries[i].Ext != "ari" && entries[i].Ext != "arx" {
				continue
			}
			m := frameRe.FindStringSubmatch(entries[i].Name)
			if m == nil {
				continue
			}
			key := m[1] + "." + entries[i].Ext
			if _, ok := seqs[key]; !ok {
				seqOrder = append(seqOrder, key)
			}
			seqs[key] = append(seqs[key], i)
		}
		sort.Strings(seqOrder)
		for _, key := range seqOrder {
			frames := seqs[key]
			sort.Slice(frames, func(a, b int) bool { return entries[frames[a]].Name < entries[frames[b]].Name })
			prefix := strings.TrimRight(strings.TrimSuffix(key, path.Ext(key)), "._-")
			if prefix == "" {
				prefix = base
			}
			c := newClip(KindARRIRAW, prefix, d, entries, frames, nil)
			c.FrameCount = len(frames)
			c.FirstFrame = frameNumber(entries[frames[0]].Name)
			c.LastFrame = frameNumber(entries[frames[len(frames)-1]].Name)
			c.FramePath = entries[frames[0]].Path
			if !opts.KeepFrames {
				c.Files = nil
				for _, i := range frames {
					dropped[i] = true
				}
			}
			clips = append(clips, c)
			for _, i := range frames {
				claimed[i] = c.ID
			}
		}

		// Sony card clips, BRAW, and plain media files with same-stem sidecars.
		sony := isSonyClipDir(d)
		byStem := map[string][]int{}
		for _, i := range idx {
			if _, taken := claimed[i]; taken {
				continue
			}
			byStem[strings.ToLower(entries[i].Stem())] = append(byStem[strings.ToLower(entries[i].Stem())], i)
		}
		var media []int
		for _, i := range idx {
			if _, taken := claimed[i]; taken {
				continue
			}
			if k := entries[i].Kind; k == scan.KindVideo || k == scan.KindAudio {
				media = append(media, i)
			}
		}
		for _, i := range media {
			e := entries[i]
			stem := strings.ToLower(e.Stem())
			var sidecars []int
			for _, j := range byStem[stem] {
				if j != i && sidecarExts[entries[j].Ext] {
					if _, taken := claimed[j]; !taken {
						sidecars = append(sidecars, j)
					}
				}
			}
			// Sony names its XML <stem>M01.XML.
			for _, j := range byStem[stem+"m01"] {
				if entries[j].Ext == "xml" {
					if _, taken := claimed[j]; !taken {
						sidecars = append(sidecars, j)
					}
				}
			}
			kind := KindFile
			switch {
			case e.Ext == "braw":
				kind = KindBRAW
			case sony && (e.Ext == "mxf" || e.Ext == "mp4"):
				kind = KindSony
			}
			c := newClip(kind, e.Stem(), e.Path, entries, []int{i}, sidecars)
			clips = append(clips, c)
			claimed[i] = c.ID
			for _, j := range sidecars {
				claimed[j] = c.ID
			}
		}
	}

	out := make([]scan.Entry, 0, len(entries))
	for i, e := range entries {
		if dropped[i] {
			continue
		}
		if id, ok := claimed[i]; ok {
			e.ClipID = id
		} else if e.IsDir && strings.EqualFold(path.Ext(e.Name), ".rdc") {
			// Tag the .RDC folder itself so browse can show it as a clip row.
			for _, c := range clips {
				if c.Kind == KindR3D && c.RootPath == e.Path {
					e.ClipID = c.ID
					break
				}
			}
		}
		out = append(out, e)
	}
	sort.Slice(clips, func(a, b int) bool { return clips[a].RootPath < clips[b].RootPath })
	return out, clips
}

func newClip(kind Kind, name, root string, entries []scan.Entry, files, sidecars []int) Clip {
	c := Clip{
		ID:       clipID(kind, root),
		Kind:     kind,
		Name:     name,
		RootPath: root,
	}
	for _, i := range files {
		e := entries[i]
		c.Files = append(c.Files, e.Path)
		c.TotalSize += e.Size
		if c.ModTime.IsZero() || e.ModTime.Before(c.ModTime) {
			c.ModTime = e.ModTime
		}
	}
	for _, i := range sidecars {
		e := entries[i]
		c.Sidecars = append(c.Sidecars, e.Path)
		c.TotalSize += e.Size
	}
	sort.Strings(c.Sidecars)
	c.FileCount = len(files) + len(sidecars)
	return c
}

// clipID is a stable identifier derived from the clip's kind and root
// path, so the same clip gets the same ID on every scan of the drive.
func clipID(kind Kind, root string) string {
	return fmt.Sprintf("%016x", xxhash.Sum64String(string(kind)+":"+root))
}

func frameNumber(name string) int {
	m := frameRe.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

// isSonyClipDir reports whether dir is a Sony card clip folder:
// .../XDROOT/Clip or .../PRIVATE/M4ROOT/CLIP (case-insensitive).
func isSonyClipDir(dir string) bool {
	parts := strings.Split(dir, "/")
	if len(parts) < 2 {
		return false
	}
	last, parent := strings.ToUpper(parts[len(parts)-1]), strings.ToUpper(parts[len(parts)-2])
	return last == "CLIP" && (parent == "XDROOT" || parent == "M4ROOT")
}

// Summary counts clips by kind.
func Summary(clips []Clip) map[Kind]int {
	m := map[Kind]int{}
	for _, c := range clips {
		m[c.Kind]++
	}
	return m
}
