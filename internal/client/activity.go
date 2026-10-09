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
// the caller and otherwise ignored. Zero value is a no-op reporter.
type ActivityReporter struct {
	c        *Client
	id       int64
	interval time.Duration
	mu       sync.Mutex
	last     time.Time
	lastSent api.ActivityUpdate
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
	if time.Since(r.last) < r.interval && !final && stage == r.lastSent.Stage {
		return
	}
	if upd == r.lastSent {
		return
	}
	r.last, r.lastSent = time.Now(), upd
	_ = r.c.UpdateActivity(context.Background(), r.id, upd)
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
// A successful push is closed by the upload itself.
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
	_ = r.c.UpdateActivity(context.Background(), r.id, api.ActivityUpdate{Status: status, Error: msg})
}
