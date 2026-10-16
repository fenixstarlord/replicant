package scanner

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/bundle"
	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/extract"
	"github.com/fenixstarlord/replicant/internal/extract/ale"
	"github.com/fenixstarlord/replicant/internal/extract/ffprobe"
	"github.com/fenixstarlord/replicant/internal/extract/registry"
	"github.com/fenixstarlord/replicant/internal/scan"
)

// Tape mode reads an LTFS tape the way a tape drive wants to be read: one
// file at a time, in the order the files lie on tape, each clip visited
// once. Listing the volume costs nothing on LTFS (the index is in memory);
// every data read may move the tape, and interleaved or out-of-order reads
// make it locate back and forth. See docs/design/tape-mode.md.

// TapeMode selects how a scan reads file data.
type TapeMode string

// Tape modes. The zero value behaves like TapeAuto.
const (
	TapeAuto TapeMode = "auto" // tape mode when the volume is LTFS
	TapeOn   TapeMode = "on"   // always (any volume; tape order needs LTFS)
	TapeOff  TapeMode = "off"  // never
)

// DefaultTapeStreamMB is scan.DefaultStreamLimit in MiB, the CLI default.
const DefaultTapeStreamMB = scan.DefaultStreamLimit >> 20

// TapeClipTimeout is the default per-extractor clip timeout in tape mode.
// A first read can wait for a locate of a minute or more, and extractors
// such as ffprobe on MXF jump between the head and tail of large files.
const TapeClipTimeout = 20 * time.Minute

// ParseTapeMode reads a tape mode name.
func ParseTapeMode(s string) (TapeMode, error) {
	switch m := TapeMode(strings.ToLower(strings.TrimSpace(s))); m {
	case "", TapeAuto:
		return TapeAuto, nil
	case TapeOn, TapeOff:
		return m, nil
	}
	return "", fmt.Errorf("tape mode %q: want auto, on or off", s)
}

func (m TapeMode) enabled(ltfs bool) bool {
	switch m {
	case TapeOn:
		return true
	case TapeOff:
		return false
	}
	return ltfs
}

// readTapePosition is replaced in tests.
var readTapePosition = scan.ReadTapePosition

// traceTapeRead, when set by a test, sees every extractor run ("extract")
// and hash ("hash") of a tape scan with the file it reads, in order.
var traceTapeRead func(what, rel string)

// tapeFile is one file a tape scan reads and where it lies on tape.
type tapeFile struct {
	entry  int // index into the entry list
	pos    scan.TapePosition
	hasPos bool
	hash   bool // hash it (otherwise it is only an extractor input)
}

// tapeUnit is one stop of a tape scan: a clip, or a single file outside
// any clip.
type tapeUnit struct {
	clip   int        // index into the clip list, or -1
	files  []tapeFile // files of the unit, in tape order
	pos    scan.TapePosition
	hasPos bool   // pos is the earliest known position of the unit
	path   string // orders units with no known position
}

// planTape builds the stops of a tape scan in tape order. Units with a
// known position come first, by partition and start block; the rest follow
// in path order. A clip's member files are part of its unit when they are
// hashed or when the clip is extracted (its extractors read them).
// position looks up a file by its path relative to the root.
func planTape(entries []scan.Entry, cl []clips.Clip, hashing, extracting bool, position func(rel string) (scan.TapePosition, bool)) []tapeUnit {
	var units []tapeUnit
	unitOf := map[int]int{} // clip index -> unit index
	addClip := func(ci int) int {
		if u, ok := unitOf[ci]; ok {
			return u
		}
		unitOf[ci] = len(units)
		units = append(units, tapeUnit{clip: ci, path: cl[ci].RootPath})
		return unitOf[ci]
	}
	if extracting {
		for ci := range cl {
			addClip(ci)
		}
	}
	if hashing || extracting {
		clipIndex := make(map[string]int, len(cl))
		for ci := range cl {
			clipIndex[cl[ci].ID] = ci
		}
		for i := range entries {
			e := &entries[i]
			if !scan.NeedsHash(e) {
				continue
			}
			ci, inClip := clipIndex[e.ClipID]
			inClip = inClip && e.ClipID != ""
			if !hashing && !inClip {
				continue
			}
			p, ok := position(e.Path)
			f := tapeFile{entry: i, pos: p, hasPos: ok, hash: hashing}
			if inClip {
				u := addClip(ci)
				units[u].files = append(units[u].files, f)
				continue
			}
			units = append(units, tapeUnit{clip: -1, files: []tapeFile{f}, path: e.Path})
		}
	}
	for k := range units {
		u := &units[k]
		sort.SliceStable(u.files, func(a, b int) bool {
			return tapeLess(u.files[a].pos, u.files[a].hasPos, entries[u.files[a].entry].Path,
				u.files[b].pos, u.files[b].hasPos, entries[u.files[b].entry].Path)
		})
		for _, f := range u.files {
			if f.hasPos && (!u.hasPos || f.pos.Less(u.pos)) {
				u.pos, u.hasPos = f.pos, true
			}
		}
		if extracting && u.clip >= 0 {
			if p, ok := position(cl[u.clip].PrimaryFile()); ok && (!u.hasPos || p.Less(u.pos)) {
				u.pos, u.hasPos = p, true
			}
		}
	}
	sort.SliceStable(units, func(a, b int) bool {
		return tapeLess(units[a].pos, units[a].hasPos, units[a].path, units[b].pos, units[b].hasPos, units[b].path)
	})
	return units
}

// tapeLess orders by known position first, then by path.
func tapeLess(p scan.TapePosition, hasP bool, pathP string, q scan.TapePosition, hasQ bool, pathQ string) bool {
	switch {
	case hasP != hasQ:
		return hasP
	case hasP && p != q:
		return p.Less(q)
	}
	return pathP < pathQ
}

// runTape groups, then visits every clip and loose file once in tape order
// with a single reader. Within a clip, each file is visited in tape order:
// the extractors that read it run, then it is hashed. Extractors such as
// ffprobe finish near the head of a file, so hashing afterwards leaves the
// drive at the end of the file, ready for the next one. Extractors that
// read no file of the unit (an ALE index, the first frame of an ARRIRAW
// sequence whose frames were dropped) run when the clip is reached.
func runTape(ctx context.Context, abs string, so scan.Options, entries []scan.Entry, res *Result, opts Options, report func(Progress)) error {
	res.Entries, res.Clips = clips.Group(entries, clips.Options{KeepFrames: opts.KeepFrames})
	hashing := so.Fingerprint || so.FullHash
	var runner *extract.Runner
	var aleIndex *ale.Extractor
	if !opts.Fast {
		extractors := registry.Selected(abs, res.Entries, opts.Tools, opts.Extractors)
		for _, e := range extractors {
			if probe, ok := e.(*ffprobe.Extractor); ok {
				probe.Tape = true
			}
			if index, ok := e.(*ale.Extractor); ok && len(res.Clips) > 0 {
				aleIndex = index
			}
		}
		runner = extract.NewRunner(ctx, extractors)
		res.Extractors = runner.Statuses()
	}

	limit := opts.StreamLimit
	if limit == 0 {
		limit = scan.DefaultStreamLimit
	}
	timeout := opts.ClipTimeout
	if timeout == 0 {
		timeout = TapeClipTimeout
	}
	stats := &bundle.TapeStats{StreamLimit: limit, ClipTimeoutS: -1}
	eopts := extract.Options{ClipTimeout: -1}
	if timeout > 0 {
		stats.ClipTimeoutS = int64(timeout / time.Second)
		eopts.ClipTimeout = timeout
	}

	t0 := time.Now()
	type lookup struct {
		pos scan.TapePosition
		ok  bool
	}
	seen := map[string]lookup{}
	looked, found := 0, 0
	position := func(rel string) (scan.TapePosition, bool) {
		if rel == "" || ctx.Err() != nil {
			return scan.TapePosition{}, false
		}
		if l, ok := seen[rel]; ok {
			return l.pos, l.ok
		}
		p, ok := readTapePosition(filepath.Join(abs, filepath.FromSlash(rel)))
		seen[rel] = lookup{p, ok}
		looked++
		if ok {
			found++
		}
		return p, ok
	}
	// ALEs are shared indexes, not clip members. Include their positions even
	// when fingerprinting is disabled, before the explicit preload pass.
	if aleIndex != nil {
		for _, e := range res.Entries {
			if e.Ext == "ale" && !e.IsDir && !e.IsSymlink && e.Error == "" {
				position(e.Path)
			}
		}
	}
	units := planTape(res.Entries, res.Clips, hashing, runner != nil, position)
	if err := ctx.Err(); err != nil {
		return err
	}
	stats.PositionMS = time.Since(t0).Milliseconds()
	for _, u := range units {
		for _, f := range u.files {
			if f.hash {
				stats.Files++
				if f.hasPos {
					stats.Positioned++
				}
			}
		}
	}
	report(Progress{Stage: "tape", Done: found, Total: looked})

	var last scan.TapePosition
	haveLast := false
	visit := func(p scan.TapePosition, ok bool) {
		if !ok {
			return
		}
		if haveLast && p.Less(last) {
			stats.Backward++
		}
		last, haveLast = p, true
	}
	if aleIndex != nil {
		start := time.Now()
		err := aleIndex.PrepareInOrder(ctx, func(a, b string) bool {
			pa, oka := position(a)
			pb, okb := position(b)
			return tapeLess(pa, oka, a, pb, okb, b)
		}, func(p ale.LoadProgress) {
			event, detail := "start", ""
			if p.Finished {
				event = "finish"
			} else {
				pos, ok := position(p.Path)
				visit(pos, ok)
				if traceTapeRead != nil {
					traceTapeRead("ale", p.Path)
				}
			}
			if p.Err != nil {
				detail = p.Err.Error()
			}
			report(Progress{
				Stage: "ale", Done: p.Done, Total: p.Total, Path: p.Path, Extractor: "ale index",
				Event: event, Elapsed: p.Elapsed, Detail: detail,
			})
		})
		res.ExtractTime += time.Since(start)
		if err != nil {
			return err
		}
	}
	extractInput := func(job *extract.ClipJob, input string, p scan.TapePosition, ok bool) bool {
		t := time.Now()
		ran := job.RunInput(ctx, input) > 0
		res.ExtractTime += time.Since(t)
		if ran {
			visit(p, ok)
			if traceTapeRead != nil {
				traceTapeRead("extract", input)
			}
		}
		return ran
	}

	h := scan.TapeHasher{Fingerprint: so.Fingerprint, FullHash: so.FullHash, StreamLimit: opts.StreamLimit}
	hashed, extracted := 0, 0
	var clipPath string
	eopts.Progress = func(p extract.Progress) {
		input := p.Input
		if input == "" {
			input = clipPath
		}
		report(Progress{
			Stage: "extract", Done: extracted, Total: len(res.Clips),
			Path: input, Extractor: p.Extractor, Event: p.Event, Elapsed: p.Elapsed, Detail: p.Detail,
		})
	}
	for _, u := range units {
		if err := ctx.Err(); err != nil {
			return err
		}
		var job *extract.ClipJob
		if u.clip >= 0 && runner != nil {
			clipPath = res.Clips[u.clip].RootPath
			var err error
			job, err = runner.StartClip(ctx, abs, &res.Clips[u.clip], eopts)
			if err != nil {
				return err
			}
			inUnit := make(map[string]bool, len(u.files))
			for _, f := range u.files {
				inUnit[res.Entries[f.entry].Path] = true
			}
			for _, in := range job.Inputs() {
				if !inUnit[in] {
					p, ok := position(in)
					extractInput(job, in, p, ok)
				}
			}
		}
		for _, f := range u.files {
			if err := ctx.Err(); err != nil {
				return err
			}
			e := &res.Entries[f.entry]
			read := false
			if job != nil {
				read = extractInput(job, e.Path, f.pos, f.hasPos)
			}
			if !f.hash {
				continue
			}
			if !read {
				visit(f.pos, f.hasPos)
			}
			if traceTapeRead != nil {
				traceTapeRead("hash", e.Path)
			}
			t := time.Now()
			report(Progress{Stage: "hash", Done: hashed, Total: stats.Files, Path: e.Path, Event: "start"})
			r := h.Hash(ctx, filepath.Join(abs, filepath.FromSlash(e.Path)), e)
			res.HashTime += time.Since(t)
			stats.BytesRead += r.Bytes
			switch {
			case e.Error != "":
				stats.Failed++
			case r.Streamed:
				stats.Streamed++
			default:
				stats.HeadTail++
			}
			hashed++
			report(Progress{
				Stage: "hash", Done: hashed, Total: stats.Files, Path: e.Path,
				Event: "finish", Elapsed: time.Since(t), Detail: e.Error,
			})
		}
		if job != nil {
			t := time.Now()
			job.Finish(ctx)
			res.ExtractTime += time.Since(t)
			extracted++
			report(Progress{Stage: "extract", Done: extracted, Total: len(res.Clips)})
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stats.HashMS = res.HashTime.Milliseconds()
	stats.ExtractMS = res.ExtractTime.Milliseconds()
	res.Tape = stats
	if runner != nil {
		res.ExtractorTimes = runner.Timings()
	}
	return nil
}
