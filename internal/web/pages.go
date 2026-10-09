package web

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/store"
)

const pageSize = 100

func (s *Server) pageRoutes() {
	m := s.mux
	auth := func(h http.HandlerFunc) http.Handler { return s.requireAuth(h) }
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	m.Handle("GET /drives", auth(s.handleDrivesPage))
	m.Handle("GET /drives/{id}", auth(s.handleDrivePage))
	m.Handle("POST /drives/{id}", auth(s.handleDriveEdit))
	m.Handle("GET /search", auth(s.handleSearch))
	m.Handle("GET /browse/{drive}", auth(s.handleBrowse))
	m.Handle("GET /browse/{drive}/{path...}", auth(s.handleBrowse))
	m.Handle("GET /clips/{id}", auth(s.handleClip))
	m.Handle("GET /files/{id}", auth(s.handleFile))
	m.Handle("GET /scans/{id}", auth(s.handleScan))
	m.Handle("GET /settings", auth(s.handleSettings))
	m.Handle("GET /settings/api-keys", auth(s.handleAPIKeys))
	m.Handle("POST /settings/tokens", auth(s.handleTokenCreate))
	m.Handle("POST /settings/tokens/{id}/revoke", auth(s.handleTokenRevoke))
	m.Handle("POST /settings/upload", auth(s.handleUpload))
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error, what string) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	s.log.Error(what, "err", err, "path", r.URL.Path)
	http.Error(w, what+": "+err.Error(), http.StatusInternalServerError)
}

func pathID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/drives", http.StatusSeeOther)
}

func (s *Server) handleDrivesPage(w http.ResponseWriter, r *http.Request) {
	drives, err := s.store.ListDrives(r.Context())
	if err != nil {
		s.fail(w, r, err, "list drives")
		return
	}
	var files, clips int
	var bytes int64
	for _, d := range drives {
		files += d.FileCount
		clips += d.ClipCount
		bytes += d.TotalBytes
	}
	s.render(w, r, "drives", map[string]any{"Title": "Drives", "Drives": drives,
		"TotalFiles": files, "TotalClips": clips, "TotalBytes": bytes})
}

func (s *Server) handleDrivePage(w http.ResponseWriter, r *http.Request) {
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
	scans, err := s.store.ListScans(r.Context(), id)
	if err != nil {
		s.fail(w, r, err, "scans")
		return
	}
	s.render(w, r, "drive", map[string]any{"Title": d.Name, "Drive": d, "Scans": scans, "Edit": r.URL.Query().Get("edit") != ""})
}

func (s *Server) handleDriveEdit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateDriveLabels(r.Context(), id, strings.TrimSpace(r.FormValue("label")),
		strings.TrimSpace(r.FormValue("location")), strings.TrimSpace(r.FormValue("notes"))); err != nil {
		s.fail(w, r, err, "update drive")
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/drives/%d", id), http.StatusSeeOther)
}

// parseSearch reads the search form into a query.
func parseSearch(r *http.Request) store.SearchQuery {
	g := r.URL.Query().Get
	i64 := func(k string) int64 { v, _ := strconv.ParseInt(strings.TrimSpace(g(k)), 10, 64); return v }
	f64 := func(k string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(g(k)), 64); return v }
	q := store.SearchQuery{
		Text: g("q"), Kind: g("kind"), Codec: g("codec"), Camera: g("camera"), ColorGamma: g("gamma"),
		Lens: g("lens"), Reel: g("reel"), Scene: g("scene"), Take: g("take"), Timecode: g("tc"),
		MinWidth: i64("min_width"), MinISO: i64("iso_min"), MaxISO: i64("iso_max"), MinWB: i64("wb_min"), MaxWB: i64("wb_max"),
		FPS: f64("fps"), MinFocal: f64("focal_min"), MaxFocal: f64("focal_max"), MinDur: f64("dur_min"), MaxDur: f64("dur_max"),
		MinSize: i64("size_min") * 1e9, MaxSize: i64("size_max") * 1e9,
		IncludeOld: g("old") != "", Sort: g("sort"), Desc: g("desc") != "", Limit: pageSize,
	}
	for _, d := range r.URL.Query()["drive"] {
		if id, err := strconv.ParseInt(d, 10, 64); err == nil && id > 0 {
			q.DriveIDs = append(q.DriveIDs, id)
		}
	}
	if t, err := time.Parse("2006-01-02", g("from")); err == nil {
		q.From = t
	}
	if t, err := time.Parse("2006-01-02", g("to")); err == nil {
		q.To = t.Add(24 * time.Hour)
	}
	if p, _ := strconv.Atoi(g("page")); p > 1 {
		q.Offset = (p - 1) * pageSize
	}
	return q
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := parseSearch(r)
	mode := r.URL.Query().Get("mode")
	data := map[string]any{"Title": "Search", "Q": q, "Mode": mode, "Form": r.URL.Query()}
	drives, err := s.store.ListDrives(ctx)
	if err != nil {
		s.fail(w, r, err, "drives")
		return
	}
	data["Drives"] = drives
	facets, err := s.store.Facets(ctx)
	if err != nil {
		s.fail(w, r, err, "facets")
		return
	}
	data["Facets"] = facets
	page := q.Offset/pageSize + 1
	if mode == "files" {
		eq := store.EntrySearchQuery{Text: q.Text, DriveIDs: q.DriveIDs, Kind: q.Kind, Ext: r.URL.Query().Get("ext"),
			MinSize: q.MinSize, MaxSize: q.MaxSize, IncludeOld: q.IncludeOld, Limit: pageSize, Offset: q.Offset}
		rows, total, err := s.store.SearchEntries(ctx, eq)
		if err != nil {
			s.fail(w, r, err, "search files")
			return
		}
		data["Entries"], data["Total"] = rows, total
		data["Page"], data["Pages"] = page, (total+pageSize-1)/pageSize
	} else {
		rows, total, err := s.store.SearchClips(ctx, q)
		if err != nil {
			s.fail(w, r, err, "search clips")
			return
		}
		data["Clips"], data["Total"] = rows, total
		data["Page"], data["Pages"] = page, (total+pageSize-1)/pageSize
	}
	s.render(w, r, "search", data)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	driveID, err := pathID(r, "drive")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, err := s.store.GetDrive(ctx, driveID)
	if err != nil {
		s.fail(w, r, err, "drive")
		return
	}
	var sc store.Scan
	if sid, _ := strconv.ParseInt(r.URL.Query().Get("scan"), 10, 64); sid > 0 {
		sc, err = s.store.GetScan(ctx, sid)
	} else {
		sc, err = s.store.LatestScan(ctx, driveID)
	}
	if err != nil {
		s.fail(w, r, err, "scan")
		return
	}
	p := strings.Trim(r.PathValue("path"), "/")
	rows, err := s.store.ListDir(ctx, sc.ID, p)
	if err != nil {
		s.fail(w, r, err, "list dir")
		return
	}
	var dir store.EntryRow
	if p != "" {
		dir, err = s.store.EntryByPath(ctx, sc.ID, p)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.fail(w, r, err, "dir")
			return
		}
	} else {
		dir = store.EntryRow{IsDir: true, DirTotalSize: sc.TotalBytes, DirFileCount: int64(sc.FileCount)}
	}
	s.render(w, r, "browse", map[string]any{"Title": d.Name + " / " + p, "Drive": d, "Scan": sc, "Dir": dir,
		"DirPath": p, "Entries": rows, "Crumbs": crumbs(p)})
}

func (s *Server) handleClip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c, raw, err := s.store.GetClip(ctx, id)
	if err != nil {
		s.fail(w, r, err, "clip")
		return
	}
	d, err := s.store.GetDrive(ctx, c.DriveID)
	if err != nil {
		s.fail(w, r, err, "drive")
		return
	}
	hist, err := s.store.ClipHistory(ctx, c.DriveID, c.RootPath)
	if err != nil {
		s.fail(w, r, err, "history")
		return
	}
	var copies []store.EntryRow
	if e, err := s.store.EntryByPath(ctx, c.ScanID, firstOr(c.Files, c.RootPath)); err == nil {
		copies, _ = s.store.Copies(ctx, e)
	}
	s.render(w, r, "clip", map[string]any{"Title": c.Name, "Clip": c, "Drive": d, "Raw": raw, "History": hist, "Copies": copies})
}

func firstOr(list []string, def string) string {
	if len(list) > 0 {
		return list[0]
	}
	return def
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	e, err := s.store.GetEntry(ctx, id)
	if err != nil {
		s.fail(w, r, err, "file")
		return
	}
	d, err := s.store.GetDrive(ctx, e.DriveID)
	if err != nil {
		s.fail(w, r, err, "drive")
		return
	}
	copies, _ := s.store.Copies(ctx, e)
	s.render(w, r, "file", map[string]any{"Title": e.Name, "Entry": e, "Drive": d, "Copies": copies})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sc, err := s.store.GetScan(ctx, id)
	if err != nil {
		s.fail(w, r, err, "scan")
		return
	}
	changes, err := s.store.ScanChanges(ctx, id, 500)
	if err != nil {
		s.fail(w, r, err, "changes")
		return
	}
	s.render(w, r, "scan", map[string]any{"Title": fmt.Sprintf("Scan #%d", id), "Scan": sc, "Changes": changes})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings", map[string]any{"Title": "Settings", "Backups": s.listBackups(),
		"Message": r.URL.Query().Get("msg"), "Error": r.URL.Query().Get("err")})
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	toks, err := s.store.ListTokens(r.Context())
	if err != nil {
		s.fail(w, r, err, "tokens")
		return
	}
	s.render(w, r, "apikeys", map[string]any{"Title": "API keys", "Tokens": toks, "Host": r.Host,
		"Message": r.URL.Query().Get("msg")})
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "key"
	}
	plain, _, err := s.store.CreateToken(r.Context(), name)
	if err != nil {
		s.fail(w, r, err, "create token")
		return
	}
	toks, _ := s.store.ListTokens(r.Context())
	s.render(w, r, "apikeys", map[string]any{"Title": "API keys", "Tokens": toks, "Host": r.Host,
		"NewToken": plain, "NewTokenName": name})
}

func (s *Server) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.store.RevokeToken(r.Context(), id); err != nil {
		s.fail(w, r, err, "revoke token")
		return
	}
	http.Redirect(w, r, "/settings/api-keys?msg=Key+revoked.", http.StatusSeeOther)
}

// handleUpload ingests a bundle from the settings page form.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxUploadSize)
	f, _, err := r.FormFile("bundle")
	if err != nil {
		http.Redirect(w, r, "/settings?err=choose+a+.shelf+file", http.StatusSeeOther)
		return
	}
	defer f.Close()
	tmp, err := os.CreateTemp(s.cfg.DataDir, "upload-*.shelf")
	if err != nil {
		s.fail(w, r, err, "spool")
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := io.Copy(tmp, f)
	if err != nil {
		s.fail(w, r, err, "read upload")
		return
	}
	b, err := bundle.Read(tmp, size)
	if err != nil {
		http.Redirect(w, r, "/settings?err="+strings.ReplaceAll(err.Error(), " ", "+"), http.StatusSeeOther)
		return
	}
	res, err := s.store.Ingest(r.Context(), b)
	if err != nil {
		s.fail(w, r, err, "ingest")
		return
	}
	s.log.Info("ingested scan via web", "scan_id", res.ScanID, "drive", res.DriveName, "entries", res.Entries)
	http.Redirect(w, r, fmt.Sprintf("/drives/%d", res.DriveID), http.StatusSeeOther)
}
