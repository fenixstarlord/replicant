package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fenixstarlord/replicant/internal/api"
	"github.com/fenixstarlord/replicant/internal/store"
)

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if key := r.URL.Query().Get("key"); key != "" {
		p, err := s.store.DriveProfile(r.Context(), key)
		if err != nil {
			writeError(w, 500, "read profile failed")
			return
		}
		writeJSON(w, 200, p)
		return
	}
	profiles, err := s.store.ListDriveProfiles(r.Context())
	if err != nil {
		writeError(w, 500, "list profiles failed")
		return
	}
	groups, err := s.store.ListGroups(r.Context(), store.ByGroup)
	if err != nil {
		writeError(w, 500, "list groups failed")
		return
	}
	clients, err := s.store.ListGroups(r.Context(), store.ByClient)
	if err != nil {
		writeError(w, 500, "list clients failed")
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	if clients == nil {
		clients = []store.Group{}
	}
	writeJSON(w, 200, api.ProfilesResponse{Profiles: profiles, Groups: groups, Clients: clients})
}

func (s *Server) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	var u store.ProfileUpdate
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&u); err != nil {
		writeError(w, 400, "invalid profile JSON")
		return
	}
	if err := u.Validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p, err := s.store.SaveDriveProfile(r.Context(), u)
	if errors.Is(err, store.ErrProfileConflict) {
		writeError(w, 409, err.Error())
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, p)
}
