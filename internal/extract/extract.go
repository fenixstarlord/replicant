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
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/meta"
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

// Status is an extractor's availability, for `shelf doctor` and the manifest.
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
	Workers     int           // concurrent clips; 0 = NumCPU
	ClipTimeout time.Duration // per extractor per clip; 0 = 60s
}

// Runner holds a set of extractors and their resolved availability.
type Runner struct {
	extractors []Extractor
	status     []Status
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
	}
	return r
}

// Statuses returns availability for every registered extractor.
func (r *Runner) Statuses() []Status { return append([]Status(nil), r.status...) }

// Run extracts metadata for every clip, in place: it fills c.Meta,
// c.Sources, c.Raw, and c.Errors. A clip that fails is still returned
// with its errors recorded. progress, if non-nil, is called from worker
// goroutines.
func (r *Runner) Run(ctx context.Context, root string, cl []clips.Clip, opts Options, progress func(done, total int)) error {
	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers()
	}
	timeout := opts.ClipTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	root = filepath.Clean(root)
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
	return g.Wait()
}

func (r *Runner) runClip(ctx context.Context, root string, c *clips.Clip, timeout time.Duration) {
	var results []meta.Result
	var errs []string
	for i, e := range r.extractors {
		if !e.Matches(c) {
			continue
		}
		if !r.status[i].Available {
			errs = append(errs, fmt.Sprintf("%s: not available", e.Name()))
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		res, err := e.Extract(cctx, root, c)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = fmt.Errorf("timed out after %s", timeout)
			}
			errs = append(errs, fmt.Sprintf("%s: %v", e.Name(), err))
			if res == nil {
				continue
			}
		}
		if res != nil {
			res.Source = e.Name()
			results = append(results, *res)
		}
	}
	// Stable merge order: by priority, then registration order.
	order := make([]int, len(results))
	for i := range order {
		order[i] = i
	}
	prio := map[string]int{}
	for i, e := range r.extractors {
		prio[e.Name()] = r.status[i].Priority
	}
	sort.SliceStable(order, func(a, b int) bool { return prio[results[order[a]].Source] < prio[results[order[b]].Source] })
	ordered := make([]meta.Result, len(results))
	for i, j := range order {
		ordered[i] = results[j]
	}
	m := meta.Merge(ordered)
	m.Errors = errs
	c.Meta = &m.Fields
	c.Sources = m.Sources
	c.Raw = m.Raw
	c.Errors = errs
}
