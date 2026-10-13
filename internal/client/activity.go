package client

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fenixstarlord/replicant/internal/api"
)

// StartActivity tells the server a scan has begun and returns its id.
func (c *Client) StartActivity(ctx context.Context, req api.ActivityStart) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var resp api.ActivityResponse
	if err := c.do(ctx, http.MethodPost, api.PathActivity, strings.NewReader(mustJSON(req)), "application/json", &resp); err != nil {
		return 0, err
	}
	return resp.ID, nil
}

// UpdateActivity reports progress or a final status.
func (c *Client) UpdateActivity(ctx context.Context, id int64, upd api.ActivityUpdate) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.do(ctx, http.MethodPut, api.PathActivity+"/"+strconv.FormatInt(id, 10), strings.NewReader(mustJSON(upd)), "application/json", nil)
}

// ActivityReporter sends progress to the server at most every interval,
// and never fails the scan: a server that cannot be reached is logged by
// the caller and otherwise ignored. Progress updates go out from a
// background sender that only ever holds the newest one, so a slow or
// unreachable server never stalls the scan (on a tape that would stop the
// drive). Zero value is a no-op reporter.
type ActivityReporter struct {
	c        *Client
	id       int64
	interval time.Duration
	mu       sync.Mutex
	last     time.Time
	lastSent api.ActivityUpdate
	seen     map[string]bool     // stages reported so far
	queued   *api.ActivityUpdate // newest update not yet sent
	wake     chan struct{}       // nudges the sender; closed by Finish
	closed   bool
}

// NewActivityReporter starts an activity; on failure it returns a no-op
// reporter and the error, so scans continue without progress reporting.
func NewActivityReporter(ctx context.Context, c *Client, start api.ActivityStart) (*ActivityReporter, error) {
	if c == nil {
		return &ActivityReporter{}, nil
	}
	id, err := c.StartActivity(ctx, start)
	if err != nil {
		return &ActivityReporter{}, err
	}
	return &ActivityReporter{c: c, id: id, interval: 2 * time.Second}, nil
}

// ID is the server-side activity id, 0 for a no-op reporter.
func (r *ActivityReporter) ID() int64 {
	if r == nil {
		return 0
	}
	return r.id
}

// Progress reports a stage, throttled. Safe from several goroutines.
func (r *ActivityReporter) Progress(stage string, done, total int) {
	if r == nil || r.c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	upd := api.ActivityUpdate{Stage: stage, Done: done, Total: total}
	final := total > 0 && done == total
	// A stage's first report and its final one go out at once; the rest are
	// throttled, even when stages alternate (a tape scan switches between
	// extract and hash for every clip).
	if time.Since(r.last) < r.interval && !final && r.seen[stage] {
		return
	}
	if upd == r.lastSent {
		return
	}
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	r.seen[stage] = true
	r.last, r.lastSent = time.Now(), upd
	r.enqueue(upd)
}

// Keepalive re-sends the last progress when nothing has been sent for a
// while, so the server does not presume the scan lost during one long
// read (it does after 5 minutes of silence). Call it from a ticker.
func (r *ActivityReporter) Keepalive() {
	if r == nil || r.c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || time.Since(r.last) < r.interval {
		return
	}
	r.last = time.Now()
	r.enqueue(r.lastSent)
}

// enqueue hands upd to the sender, replacing any update still waiting.
// The caller holds r.mu.
func (r *ActivityReporter) enqueue(upd api.ActivityUpdate) {
	r.queued = &upd
	if r.wake == nil {
		r.wake = make(chan struct{}, 1)
		go r.send(r.wake)
	}
	select {
	case r.wake <- struct{}{}:
	default: // a nudge is already pending
	}
}

// send delivers queued updates until Finish closes wake.
func (r *ActivityReporter) send(wake <-chan struct{}) {
	for range wake {
		r.mu.Lock()
		upd := r.queued
		r.queued = nil
		r.mu.Unlock()
		if upd != nil {
			_ = r.c.UpdateActivity(context.Background(), r.id, *upd)
		}
	}
}

// Describe fills in the drive once the scanner has identified it.
func (r *ActivityReporter) Describe(driveName, volumeUUID string) {
	if r == nil || r.c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.c.UpdateActivity(context.Background(), r.id, api.ActivityUpdate{DriveName: driveName, VolumeUUID: volumeUUID})
}

// Finish closes the activity with a final status: "error" or "cancelled".
// A successful push is closed by the upload itself. Progress still queued
// is dropped; the server ignores progress for a finished activity anyway.
func (r *ActivityReporter) Finish(status, msg string) {
	if r == nil || r.c == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.queued = nil
	if r.wake != nil {
		close(r.wake)
	}
	_ = r.c.UpdateActivity(context.Background(), r.id, api.ActivityUpdate{Status: status, Error: msg})
}
