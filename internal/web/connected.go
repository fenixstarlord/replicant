package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fenixstarlord/indexserver/internal/scan"
	"github.com/fenixstarlord/indexserver/internal/scanner"
	"github.com/fenixstarlord/indexserver/internal/store"
)

// mountInfo is a volume currently mounted on the server.
type mountInfo struct {
	Path   string
	Volume scan.Volume
}

// mountCache remembers the server's mounted volumes briefly, since
// identifying each one shells out to diskutil on macOS.
type mountCache struct {
	mu     sync.Mutex
	at     time.Time
	mounts []mountInfo
	ttl    time.Duration
	list   func() []mountInfo // overridable in tests
}

func newMountCache() *mountCache {
	return &mountCache{ttl: 15 * time.Second, list: listMounts}
}

func listMounts() []mountInfo {
	var out []mountInfo
	for _, m := range scanner.Mounts() {
		v, err := scan.VolumeInfo(m.Path)
		if err != nil {
			continue
		}
		out = append(out, mountInfo{Path: m.Path, Volume: v})
	}
	return out
}

func (c *mountCache) get() []mountInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > c.ttl {
		c.mounts = c.list()
		c.at = time.Now()
	}
	return c.mounts
}

// connectedMount returns the mount path of a drive if it is attached to
// this server right now, matched by volume UUID, or by name when the
// drive was catalogued without one.
func (s *Server) connectedMount(d store.Drive) (string, bool) {
	for _, m := range s.mounts.get() {
		if d.VolumeUUID != "" && m.Volume.UUID != "" && strings.EqualFold(m.Volume.UUID, d.VolumeUUID) {
			return m.Path, true
		}
		if strings.HasPrefix(d.VolumeUUID, "name:") && m.Volume.Name != "" && m.Volume.Name == d.Name {
			return m.Path, true
		}
	}
	return "", false
}

// scanState describes a connected drive's scan job for the explorer header.
type scanState struct {
	Connected bool
	MountPath string
	Running   bool
	Stage     string
	Done      int
	Total     int
	Job       store.ScanJob
	HasJob    bool
}

func (s *Server) driveScanState(ctx context.Context, d store.Drive) scanState {
	st := scanState{}
	path, ok := s.connectedMount(d)
	if !ok {
		return st
	}
	st.Connected, st.MountPath = true, path
	job, err := s.store.GetScanJobByPath(ctx, path)
	if err == nil {
		st.Job, st.HasJob = job, true
		if s.cfg.Sched != nil {
			st.Stage, st.Done, st.Total, _, st.Running = s.cfg.Sched.Running(job.ID)
		}
	}
	return st
}

// handleDriveScanNow scans a connected drive through the scheduler: it
// reuses or creates a manual job for the mount path, then runs it.
func (s *Server) handleDriveScanNow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, err := s.store.GetDrive(r.Context(), id)
	if err != nil {
		s.fail(w, r, err, "drive")
		return
	}
	if s.cfg.Sched == nil {
		http.Redirect(w, r, fmt.Sprintf("/browse/%d?err=Server+scans+are+not+enabled.", id), http.StatusSeeOther)
		return
	}
	path, ok := s.connectedMount(d)
	if !ok {
		http.Redirect(w, r, fmt.Sprintf("/browse/%d?err=%s+is+not+connected+to+this+server.", id, d.Name), http.StatusSeeOther)
		return
	}
	job, err := s.store.GetScanJobByPath(r.Context(), path)
	if errors.Is(err, sql.ErrNoRows) {
		jid, cerr := s.store.CreateScanJob(r.Context(), store.ScanJob{Path: path, Label: d.Name, Extract: true, Fingerprint: true, Enabled: true})
		if cerr != nil {
			s.fail(w, r, cerr, "create scan job")
			return
		}
		job, err = s.store.GetScanJob(r.Context(), jid)
	}
	if err != nil {
		s.fail(w, r, err, "scan job")
		return
	}
	go func() {
		if _, err := s.cfg.Sched.RunJob(context.Background(), job.ID); err != nil {
			s.log.Error("scan now", "drive", d.Name, "err", err)
		}
	}()
	http.Redirect(w, r, fmt.Sprintf("/browse/%d?msg=Scan+started.", id), http.StatusSeeOther)
}
