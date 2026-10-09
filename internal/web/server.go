// Package web serves the HTTP API and the web UI.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fenixstarlord/indexserver/internal/api"
	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/store"
)

// Config configures the server.
type Config struct {
	Password      string // plain password, or
	PasswordHash  string // bcrypt hash (takes precedence)
	SessionSecret string // optional; auto-generated and persisted if empty
	DataDir       string // where uploads are spooled
	Version       string
	MaxUploadSize int64 // bytes; 0 means 4 GiB
}

// Server holds the handlers' shared state.
type Server struct {
	cfg    Config
	store  *store.Store
	secret []byte
	log    *slog.Logger
	mux    *http.ServeMux
}

// New builds the HTTP handler.
func New(ctx context.Context, st *store.Store, cfg Config, log *slog.Logger) (*Server, error) {
	if cfg.Password == "" && cfg.PasswordHash == "" {
		return nil, errors.New("SHELF_PASSWORD or SHELF_PASSWORD_HASH must be set")
	}
	if cfg.MaxUploadSize == 0 {
		cfg.MaxUploadSize = 4 << 30
	}
	if log == nil {
		log = slog.Default()
	}
	secret, err := loadSessionSecret(ctx, st, cfg.SessionSecret)
	if err != nil {
		return nil, fmt.Errorf("session secret: %w", err)
	}
	s := &Server{cfg: cfg, store: st, secret: secret, log: log, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", s.handleHealth)
	m.HandleFunc("GET /login", s.handleLoginForm)
	m.HandleFunc("POST /login", s.handleLogin)
	m.HandleFunc("POST /logout", s.handleLogout)

	m.Handle("POST "+api.PathScans, s.requireAuth(http.HandlerFunc(s.handleIngest)))
	m.Handle("GET "+api.PathDrives, s.requireAuth(http.HandlerFunc(s.handleDrives)))
	m.Handle("GET "+api.PathMe, s.requireAuth(http.HandlerFunc(s.handleMe)))
	m.Handle("GET /{$}", s.requireAuth(http.HandlerFunc(s.handleHome)))
	s.pageRoutes()
	s.historyRoutes()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorResponse{Error: msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	fmt.Fprintln(w, "ok")
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if _, err := s.authenticate(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") {
		next = "/"
	}
	s.render(w, r, "login", map[string]any{"Title": "Log in", "Next": next, "Failed": r.URL.Query().Get("failed") != ""})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "bad form")
		return
	}
	next := r.FormValue("next")
	if !strings.HasPrefix(next, "/") {
		next = "/"
	}
	if !s.checkPassword(r.FormValue("password")) {
		time.Sleep(300 * time.Millisecond) // blunt brute-force damper
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			writeError(w, http.StatusUnauthorized, "wrong password")
			return
		}
		http.Redirect(w, r, "/login?failed=1&next="+next, http.StatusSeeOther)
		return
	}
	s.setSession(w, r)
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r.Context())
	writeJSON(w, http.StatusOK, api.MeResponse{Auth: id.Auth, TokenName: id.TokenName, Version: s.cfg.Version})
}

func (s *Server) handleDrives(w http.ResponseWriter, r *http.Request) {
	drives, err := s.store.ListDrives(r.Context())
	if err != nil {
		s.log.Error("list drives", "err", err)
		writeError(w, http.StatusInternalServerError, "list drives failed")
		return
	}
	if drives == nil {
		drives = []store.Drive{}
	}
	writeJSON(w, http.StatusOK, api.DrivesResponse{Drives: drives})
}

// handleIngest accepts a .shelf bundle as the raw request body or as a
// multipart form with a "bundle" file, spools it to disk, and ingests it.
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)
	var src io.Reader = r.Body
	if ct := r.Header.Get("Content-Type"); strings.HasPrefix(ct, "multipart/form-data") {
		f, _, err := r.FormFile("bundle")
		if err != nil {
			writeError(w, http.StatusBadRequest, "multipart field 'bundle' required")
			return
		}
		defer f.Close()
		src = f
	}

	if err := os.MkdirAll(s.cfg.DataDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "data dir: "+err.Error())
		return
	}
	tmp, err := os.CreateTemp(s.cfg.DataDir, "upload-*.shelf")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "spool: "+err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := io.Copy(tmp, src)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	b, err := bundle.Read(tmp, size)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad bundle: "+err.Error())
		return
	}

	start := time.Now()
	res, err := s.store.Ingest(r.Context(), b)
	if err != nil {
		s.log.Error("ingest", "err", err, "volume", b.Manifest.Volume.Name)
		writeError(w, http.StatusInternalServerError, "ingest failed: "+err.Error())
		return
	}
	id, _ := IdentityFrom(r.Context())
	s.log.Info("ingested scan", "scan_id", res.ScanID, "drive", res.DriveName, "entries", res.Entries,
		"clips", res.Clips, "added", res.Added, "removed", res.Removed, "changed", res.Changed,
		"bytes", size, "took", time.Since(start).Round(time.Millisecond), "by", id.TokenName)
	writeJSON(w, http.StatusCreated, res)
}
