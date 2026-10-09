package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fenixstarlord/indexserver/internal/store"
)

func (s *Server) historyRoutes() {
	m := s.mux
	auth := func(h http.HandlerFunc) http.Handler { return s.requireAuth(h) }
	m.Handle("GET /scans/{a}/diff/{b}", auth(s.handleDiff))
	m.Handle("GET /export.csv", auth(s.handleExportCSV))
	m.Handle("GET /export.ale", auth(s.handleExportALE))
	m.Handle("POST /settings/backup", auth(s.handleBackup))
	m.Handle("GET /settings/backup/{name}", auth(s.handleBackupDownload))
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	a, err1 := pathID(r, "a")
	b, err2 := pathID(r, "b")
	if err1 != nil || err2 != nil {
		http.NotFound(w, r)
		return
	}
	d, err := s.store.DiffScans(r.Context(), a, b, 2000)
	if err != nil {
		s.fail(w, r, err, "diff")
		return
	}
	s.render(w, r, "diff", map[string]any{"Title": fmt.Sprintf("Diff #%d → #%d", a, b), "Diff": d})
}

// exportClips runs the search from the query string without paging.
func (s *Server) exportClips(r *http.Request) ([]store.ClipRow, error) {
	q := parseSearch(r)
	q.Limit, q.Offset = 100000, 0
	rows, _, err := s.store.SearchClips(r.Context(), q)
	return rows, err
}

func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := s.exportClips(r)
	if err != nil {
		s.fail(w, r, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="shelf-clips.csv"`)
	if err := store.WriteCSV(w, rows); err != nil {
		s.log.Error("csv export", "err", err)
	}
}

func (s *Server) handleExportALE(w http.ResponseWriter, r *http.Request) {
	rows, err := s.exportClips(r)
	if err != nil {
		s.fail(w, r, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="shelf-clips.ale"`)
	if err := store.WriteALE(w, rows); err != nil {
		s.log.Error("ale export", "err", err)
	}
}

func (s *Server) backupDir() string { return filepath.Join(s.cfg.DataDir, "backups") }

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if err := os.MkdirAll(s.backupDir(), 0o755); err != nil {
		s.fail(w, r, err, "backup dir")
		return
	}
	name := store.BackupName(time.Now())
	if err := s.store.Backup(r.Context(), filepath.Join(s.backupDir(), name)); err != nil {
		s.fail(w, r, err, "backup")
		return
	}
	http.Redirect(w, r, "/settings?msg=Backup+written:+"+name, http.StatusSeeOther)
}

func (s *Server) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("name"))
	p := filepath.Join(s.backupDir(), name)
	if _, err := os.Stat(p); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, p)
}

// listBackups returns backup file names, newest first.
func (s *Server) listBackups() []string {
	entries, _ := os.ReadDir(s.backupDir())
	var out []string
	for i := len(entries) - 1; i >= 0; i-- {
		if n := entries[i].Name(); filepath.Ext(n) == ".db" {
			out = append(out, n)
		}
	}
	return out
}
