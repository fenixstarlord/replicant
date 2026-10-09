package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/fenixstarlord/replicant/internal/api"
	"github.com/fenixstarlord/replicant/internal/store"
)

func (s *Server) activityRoutes() {
	auth := s.requireAuth
	m := s.mux
	m.Handle("GET /history", auth(http.HandlerFunc(s.handleHistory)))
	m.Handle("GET /history/bar", auth(http.HandlerFunc(s.handleStatusBar)))
}

// handleActivityStart: a client says it has begun scanning.
func (s *Server) handleActivityStart(w http.ResponseWriter, r *http.Request) {
	var req api.ActivityStart
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	id, _ := IdentityFrom(r.Context())
	source := id.TokenName
	if id.Auth == "open" {
		source = "local"
	}
	name := strings.TrimSpace(req.DriveName)
	if name == "" {
		name = req.Root
	}
	aid, err := s.store.StartActivity(r.Context(), store.Activity{Host: req.Host, Source: source, DriveName: name, VolumeUUID: req.VolumeUUID, Root: req.Root})
	if err != nil {
		s.log.Error("start activity", "err", err)
		writeError(w, http.StatusInternalServerError, "could not record activity")
		return
	}
	writeJSON(w, http.StatusCreated, api.ActivityResponse{ID: aid})
}

// handleActivityUpdate: progress, or a final error/cancelled status.
func (s *Server) handleActivityUpdate(w http.ResponseWriter, r *http.Request) {
	aid, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || aid <= 0 {
		http.NotFound(w, r)
		return
	}
	var upd api.ActivityUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&upd); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}
	switch upd.Status {
	case "":
		err = s.store.UpdateActivity(r.Context(), aid, upd.Stage, upd.Done, upd.Total, upd.DriveName, upd.VolumeUUID)
	case "error", "cancelled":
		err = s.store.FinishActivity(r.Context(), aid, upd.Status, 0, upd.Error)
	default:
		writeError(w, http.StatusBadRequest, "status must be error or cancelled")
		return
	}
	if err != nil {
		s.log.Error("update activity", "id", aid, "err", err)
		writeError(w, http.StatusInternalServerError, "could not record activity")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHistory lists what was scanned by whom, running scans first.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	_ = s.store.ExpireActivity(r.Context())
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	rows, total, err := s.store.ListActivity(r.Context(), pageSize, (page-1)*pageSize)
	if err != nil {
		s.fail(w, r, err, "history")
		return
	}
	s.render(w, r, "history", map[string]any{"Title": "History", "Rows": rows, "Total": total,
		"Page": page, "Pages": (total + pageSize - 1) / pageSize})
}

// handleStatusBar renders the footer status: running scans, else the last
// finished one. Polled by htmx.
func (s *Server) handleStatusBar(w http.ResponseWriter, r *http.Request) {
	_ = s.store.ExpireActivity(r.Context())
	running, err := s.store.RunningActivity(r.Context())
	if err != nil {
		s.fail(w, r, err, "activity")
		return
	}
	data := map[string]any{"Running": running}
	if len(running) == 0 {
		if last, ok, _ := s.store.LastFinishedActivity(r.Context()); ok {
			data["Last"] = last
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages["history"].ExecuteTemplate(w, "statusbar", data); err != nil {
		s.log.Error("statusbar", "err", err)
	}
}
