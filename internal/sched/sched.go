// Package sched runs the server's own scans: paths on the server's
// filesystem that are scanned on demand or on an interval, then
// ingested directly into the store.
package sched

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fenixstarlord/replicant/internal/cliconfig"
	"github.com/fenixstarlord/replicant/internal/scanner"
	"github.com/fenixstarlord/replicant/internal/store"
)

// Scheduler owns the server-side scan jobs.
type Scheduler struct {
	store   *store.Store
	log     *slog.Logger
	version string
	tools   cliconfig.Tools

	mu      sync.Mutex
	running map[int64]*runState
	wake    chan struct{}
}

type runState struct {
	Stage    string
	Done     int
	Total    int
	Since    time.Time
	activity int64     // activity row id, 0 if none
	reported time.Time // last progress write to the store
}

// New builds a scheduler. Call Start to run scheduled jobs.
func New(st *store.Store, log *slog.Logger, version string) *Scheduler {
	if log == nil {
		log = slog.Default()
	}
	return &Scheduler{store: st, log: log, version: version, running: map[int64]*runState{}, wake: make(chan struct{}, 1)}
}

// Start runs due jobs until ctx ends. One job runs at a time.
func (s *Scheduler) Start(ctx context.Context) {
	if err := s.store.ResetRunningScanJobs(ctx); err != nil {
		s.log.Warn("reset running scan jobs", "err", err)
	}
	if err := s.store.LoseServerActivity(ctx); err != nil {
		s.log.Warn("close stale server activity", "err", err)
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			s.runDue(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-s.wake:
			}
		}
	}()
}

// Kick asks the loop to check for due jobs now.
func (s *Scheduler) Kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Scheduler) runDue(ctx context.Context) {
	due, err := s.store.DueScanJobs(ctx, time.Now())
	if err != nil {
		s.log.Error("due scan jobs", "err", err)
		return
	}
	for _, j := range due {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.RunJob(ctx, j.ID); err != nil {
			s.log.Error("scheduled scan failed", "job", j.ID, "path", j.Path, "err", err)
		}
	}
}

// Running reports whether a job is in progress and its progress.
func (s *Scheduler) Running(id int64) (stage string, done, total int, since time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.running[id]
	if !ok {
		return "", 0, 0, time.Time{}, false
	}
	return st.Stage, st.Done, st.Total, st.Since, true
}

// RunJob scans the job's path and ingests the result. It refuses to run a
// job that is already running. It returns the stored scan's result.
func (s *Scheduler) RunJob(ctx context.Context, id int64) (store.IngestResult, error) {
	j, err := s.store.GetScanJob(ctx, id)
	if err != nil {
		return store.IngestResult{}, err
	}
	s.mu.Lock()
	if _, busy := s.running[id]; busy {
		s.mu.Unlock()
		return store.IngestResult{}, errors.New("already running")
	}
	st := &runState{Stage: "starting", Since: time.Now()}
	s.running[id] = st
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
	}()

	start := time.Now()
	if err := s.store.MarkScanJobRunning(ctx, id, start); err != nil {
		return store.IngestResult{}, err
	}
	host, _ := os.Hostname()
	name := j.Label
	if name == "" {
		name = filepath.Base(j.Path)
	}
	actID, aerr := s.store.StartActivity(ctx, store.Activity{Host: host, Source: "server", DriveName: name, Root: j.Path})
	if aerr != nil {
		s.log.Warn("start activity", "err", aerr)
	}
	st.activity = actID
	res, err := s.scan(ctx, j, st)
	if err := s.store.MarkScanJobDone(context.Background(), id, err, res.ScanID, time.Since(start), time.Now()); err != nil {
		s.log.Error("mark scan job done", "job", id, "err", err)
	}
	if actID > 0 && err != nil {
		// Success is closed by the ingest itself.
		status := "error"
		if errors.Is(err, context.Canceled) {
			status = "cancelled"
		}
		if ferr := s.store.FinishActivity(context.Background(), actID, status, 0, err.Error()); ferr != nil {
			s.log.Warn("finish activity", "err", ferr)
		}
	}
	if err != nil {
		return res, err
	}
	s.log.Info("server scan stored", "job", id, "path", j.Path, "scan_id", res.ScanID, "drive", res.DriveName,
		"files", res.Files, "clips", res.Clips, "took", time.Since(start).Round(time.Millisecond))
	return res, nil
}

func (s *Scheduler) scan(ctx context.Context, j store.ScanJob, st *runState) (store.IngestResult, error) {
	fi, err := os.Stat(j.Path)
	if err != nil {
		return store.IngestResult{}, fmt.Errorf("path not available: %w", err)
	}
	if !fi.IsDir() {
		return store.IngestResult{}, fmt.Errorf("%s is not a directory", j.Path)
	}
	opts := scanner.Options{Fast: !j.Extract, Fingerprint: j.Fingerprint, Tools: s.tools, Version: s.version + " (server)"}
	res, err := scanner.Run(ctx, j.Path, opts, func(p scanner.Progress) {
		s.mu.Lock()
		st.Stage, st.Done, st.Total = p.Stage, p.Done, p.Total
		write := st.activity > 0 && (time.Since(st.reported) > 2*time.Second || (p.Total > 0 && p.Done == p.Total))
		if write {
			st.reported = time.Now()
		}
		s.mu.Unlock()
		if write {
			_ = s.store.UpdateActivity(ctx, st.activity, p.Stage, p.Done, p.Total, "", "")
		}
	})
	if err != nil {
		return store.IngestResult{}, err
	}
	s.mu.Lock()
	st.Stage = "ingest"
	s.mu.Unlock()
	if st.activity > 0 {
		_ = s.store.UpdateActivity(ctx, st.activity, "ingest", 0, 0, res.Volume.Name, res.Volume.UUID)
	}
	return s.store.IngestFrom(ctx, res.Bundle(), "server", st.activity)
}
