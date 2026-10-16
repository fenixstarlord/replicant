// Package extract runs metadata extractors over clips. Each extractor
// wraps one source (an external tool, a sidecar parser, or a container
// probe); the framework handles matching, timeouts, a bounded worker
// pool, merge priority, and never lets a missing tool fail a scan.
package extract

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/meta"
)

// Extractor produces metadata for clips it recognises.
type Extractor interface {
	// Name is the source label stored with every field and raw output.
	Name() string
	// Priority orders results when merging: lower wins. Vendor tools
	// are 10, sidecars 20, container probes 30.
	Priority() int
	// Available reports whether the extractor can run (tool installed).
	Available(ctx context.Context) (ok bool, version string)
	// Matches reports whether the extractor applies to the clip.
	Matches(c *clips.Clip) bool
	// Extract runs on one clip. root is the absolute scan root; clip
	// paths are relative to it. The extractor must not write under root.
	Extract(ctx context.Context, root string, c *clips.Clip) (*meta.Result, error)
}

// Priorities for Extractor.Priority.
const (
	PriorityVendor  = 10
	PrioritySidecar = 20
	PriorityProbe   = 30
)

// Status is an extractor's availability, for `replicant doctor` and the manifest.
type Status struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Available bool   `json:"available"`
	Priority  int    `json:"priority"`
	Path      string `json:"path,omitempty"` // resolved tool binary, for external tools
}

// Located is implemented by extractors that wrap an external binary.
type Located interface {
	BinPath() string
}

// Options controls a run.
type Options struct {
	Workers     int            // concurrent clips; 0 = NumCPU
	ClipTimeout time.Duration  // per extractor per clip; 0 = DefaultClipTimeout, negative = no limit
	Progress    func(Progress) // StartClip events; called synchronously
}

// Progress describes one extractor operation, including deliberate skips.
type Progress struct {
	Extractor string
	Input     string
	Event     string // start, finish, skip
	Elapsed   time.Duration
	Detail    string // error or reason for skipping
}

// Skipper can avoid redundant I/O using successful results already collected
// for this clip. Results are supplied in registration order.
type Skipper interface {
	SkipReason(c *clips.Clip, previous []meta.Result) string
}

// Preparer loads shared metadata before Matches is called. Matches must not
// perform filesystem I/O; preparation receives the scan's cancellation context.
type Preparer interface {
	Prepare(context.Context) error
}

func (r *Runner) prepare(ctx context.Context) error {
	for i, e := range r.extractors {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p, ok := e.(Preparer); ok && r.status[i].Available {
			if err := p.Prepare(ctx); err != nil {
				return fmt.Errorf("prepare %s: %w", e.Name(), err)
			}
		}
	}
	return nil
}

// DefaultClipTimeout bounds one extractor on one clip when Options.ClipTimeout is zero.
const DefaultClipTimeout = 60 * time.Second

// clipTimeout resolves ClipTimeout; zero means no limit.
func (o Options) clipTimeout() time.Duration {
	switch {
	case o.ClipTimeout == 0:
		return DefaultClipTimeout
	case o.ClipTimeout < 0:
		return 0
	}
	return o.ClipTimeout
}

// Timing is how long one extractor spent across a run.
type Timing struct {
	Name     string
	Runs     int // clips the extractor ran on
	Errors   int // runs that returned an error, timeouts included
	Timeouts int
	Total    time.Duration
}

// Runner holds a set of extractors and their resolved availability.
type Runner struct {
	extractors []Extractor
	status     []Status

	mu      sync.Mutex
	timings []Timing // parallel to extractors
}

// NewRunner checks every extractor's availability once.
func NewRunner(ctx context.Context, extractors []Extractor) *Runner {
	r := &Runner{}
	for _, e := range extractors {
		ok, ver := e.Available(ctx)
		st := Status{Name: e.Name(), Version: ver, Available: ok, Priority: e.Priority()}
		if l, isLocated := e.(Located); isLocated {
			st.Path = l.BinPath()
		}
		r.extractors = append(r.extractors, e)
		r.status = append(r.status, st)
		r.timings = append(r.timings, Timing{Name: e.Name()})
	}
	return r
}

// Statuses returns availability for every registered extractor.
func (r *Runner) Statuses() []Status { return append([]Status(nil), r.status...) }

// Timings returns per-extractor run counts and time spent so far.
func (r *Runner) Timings() []Timing {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Timing(nil), r.timings...)
}

// Run extracts metadata for every clip, in place: it fills c.Meta,
// c.Sources, c.Raw, and c.Errors. A clip that fails is still returned
// with its errors recorded. progress, if non-nil, is called from worker
// goroutines.
func (r *Runner) Run(ctx context.Context, root string, cl []clips.Clip, opts Options, progress func(done, total int)) error {
	if len(cl) == 0 {
		return ctx.Err()
	}
	if err := r.prepare(ctx); err != nil {
		return err
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers()
	}
	timeout := opts.clipTimeout()
	root = filepath.Clean(root)
	parent := ctx
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(workers)
	var done atomic.Int64
	for i := range cl {
		c := &cl[i]
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			r.runClip(ctx, root, c, timeout)
			if progress != nil {
				progress(int(done.Add(1)), len(cl))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	// A cancel that lands while the last clips are running still cancels
	// the run, so a partly extracted scan is never taken for a whole one.
	return parent.Err()
}

// RunClip extracts metadata for one clip, in place, like Run does for each
// clip.
func (r *Runner) RunClip(ctx context.Context, root string, c *clips.Clip, opts Options) error {
	if err := r.prepare(ctx); err != nil {
		return err
	}
	r.runClip(ctx, filepath.Clean(root), c, opts.clipTimeout())
	return ctx.Err()
}

func (r *Runner) runClip(ctx context.Context, root string, c *clips.Clip, timeout time.Duration) {
	j := r.startClip(root, c, timeout)
	j.Finish(ctx)
}

// Inputter is implemented by extractors that read one file of the clip
// other than its primary file, or none ("": a sidecar index loaded
// earlier). Extractors without it read c.PrimaryFile(). Tape mode uses
// the input to run each extractor when the tape reaches that file.
type Inputter interface {
	Input(c *clips.Clip) string
}

// ClipJob extracts one clip in steps, so that a caller can run each
// extractor when it suits its own reads. Collected results are merged by
// priority and registration order; optional extractors can use earlier results
// to skip redundant reads.
type ClipJob struct {
	r        *Runner
	root     string
	c        *clips.Clip
	timeout  time.Duration
	pending  []int // extractors still to run, in registration order
	results  map[int]meta.Result
	errs     map[int]string
	progress func(Progress)
}

// StartClip prepares shared metadata and matches a clip for extraction in steps.
// Individual extractors run in RunInput or Finish.
func (r *Runner) StartClip(ctx context.Context, root string, c *clips.Clip, opts Options) (*ClipJob, error) {
	if err := r.prepare(ctx); err != nil {
		return nil, err
	}
	j := r.startClip(filepath.Clean(root), c, opts.clipTimeout())
	j.progress = opts.Progress
	return j, nil
}

func (r *Runner) startClip(root string, c *clips.Clip, timeout time.Duration) *ClipJob {
	j := &ClipJob{r: r, root: root, c: c, timeout: timeout, results: map[int]meta.Result{}, errs: map[int]string{}}
	for i, e := range r.extractors {
		if !e.Matches(c) {
			continue
		}
		if !r.status[i].Available {
			j.errs[i] = fmt.Sprintf("%s: not available", e.Name())
			continue
		}
		j.pending = append(j.pending, i)
	}
	return j
}

// Inputs lists the file each pending extractor reads, in registration
// order. A file may appear more than once.
func (j *ClipJob) Inputs() []string {
	out := make([]string, 0, len(j.pending))
	for _, i := range j.pending {
		out = append(out, j.input(i))
	}
	return out
}

func (j *ClipJob) input(i int) string {
	if in, ok := j.r.extractors[i].(Inputter); ok {
		return in.Input(j.c)
	}
	return j.c.PrimaryFile()
}

// RunInput runs the pending extractors that read input and returns how
// many ran.
func (j *ClipJob) RunInput(ctx context.Context, input string) int {
	return j.run(ctx, func(i int) bool { return j.input(i) == input })
}

// Finish runs whatever is still pending and stores the merged result,
// sources, raw output and errors on the clip.
func (j *ClipJob) Finish(ctx context.Context) {
	j.run(ctx, func(int) bool { return true })
	// Extractors left over were stopped by cancellation; say so rather
	// than leave a clip that silently lacks their sources.
	for _, i := range j.pending {
		j.errs[i] = fmt.Sprintf("%s: not run: %v", j.r.extractors[i].Name(), context.Cause(ctx))
	}
	j.pending = nil
	idx := make([]int, 0, len(j.results))
	for i := range j.results {
		idx = append(idx, i)
	}
	// Merge order: by priority, then registration order.
	sort.Slice(idx, func(a, b int) bool {
		pa, pb := j.r.status[idx[a]].Priority, j.r.status[idx[b]].Priority
		if pa != pb {
			return pa < pb
		}
		return idx[a] < idx[b]
	})
	ordered := make([]meta.Result, 0, len(idx))
	for _, i := range idx {
		ordered = append(ordered, j.results[i])
	}
	var errs []string
	for i := range j.r.extractors {
		if msg, ok := j.errs[i]; ok {
			errs = append(errs, msg)
		}
	}
	m := meta.Merge(ordered)
	m.Errors = errs
	j.c.Meta = &m.Fields
	j.c.Sources = m.Sources
	j.c.Raw = m.Raw
	j.c.Errors = errs
}

// run runs the pending extractors selected by want, in registration
// order, and returns how many ran. Once ctx is done nothing more runs.
func (j *ClipJob) run(ctx context.Context, want func(i int) bool) int {
	ran := 0
	rest := j.pending[:0]
	for _, i := range j.pending {
		if !want(i) || ctx.Err() != nil {
			rest = append(rest, i)
			continue
		}
		if j.runOne(ctx, i) {
			ran++
		}
	}
	j.pending = rest
	return ran
}

func (j *ClipJob) runOne(ctx context.Context, i int) bool {
	e := j.r.extractors[i]
	report := func(event string, elapsed time.Duration, detail string) {
		if j.progress != nil {
			j.progress(Progress{Extractor: e.Name(), Input: j.input(i), Event: event, Elapsed: elapsed, Detail: detail})
		}
	}
	if s, ok := e.(Skipper); ok {
		previous := make([]meta.Result, 0, len(j.results))
		for k := range j.r.extractors {
			if r, ok := j.results[k]; ok && j.errs[k] == "" {
				previous = append(previous, r)
			}
		}
		if reason := s.SkipReason(j.c, previous); reason != "" {
			report("skip", 0, reason)
			return false
		}
	}
	cctx, cancel := ctx, context.CancelFunc(func() {})
	if j.timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, j.timeout)
	}
	start := time.Now()
	report("start", 0, "")
	res, err := e.Extract(cctx, j.root, j.c)
	timedOut := err != nil && errors.Is(cctx.Err(), context.DeadlineExceeded)
	cancel()
	j.r.record(i, time.Since(start), err != nil, timedOut)
	if err != nil {
		if timedOut {
			err = fmt.Errorf("timed out after %s", j.timeout)
		}
		j.errs[i] = fmt.Sprintf("%s: %v", e.Name(), err)
	}
	if res != nil {
		res.Source = e.Name()
		j.results[i] = *res
	}
	report("finish", time.Since(start), j.errs[i])
	return true
}

func (r *Runner) record(i int, d time.Duration, failed, timedOut bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := &r.timings[i]
	t.Runs++
	t.Total += d
	if failed {
		t.Errors++
	}
	if timedOut {
		t.Timeouts++
	}
}
