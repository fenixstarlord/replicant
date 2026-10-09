package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/bundle"
	"github.com/fenixstarlord/replicant/internal/client"
	"github.com/fenixstarlord/replicant/internal/scanner"
	"github.com/fenixstarlord/replicant/internal/store"
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
	m.Handle("POST /drives/{id}/group", auth(s.handleDriveMove))
	m.Handle("POST /drives/groups", auth(s.handleGroupCreate))
	m.Handle("POST /drives/groups/{id}/delete", auth(s.handleGroupDelete))
	m.Handle("GET /search", auth(s.handleSearch))
	m.Handle("GET /browse/{drive}", auth(s.handleBrowse))
	m.Handle("GET /browse/{drive}/{path...}", auth(s.handleBrowse))
	m.Handle("GET /clips/{id}", auth(s.handleClip))
	m.Handle("GET /files/{id}", auth(s.handleFile))
	m.Handle("GET /scans/{id}", auth(s.handleScan))
	m.Handle("GET /settings", auth(s.handleSettings))
	m.Handle("POST /settings/view", auth(s.handleSettingsView))
	m.Handle("POST /settings/jobs", auth(s.handleJobCreate))
	m.Handle("POST /settings/jobs/{id}", auth(s.handleJobUpdate))
	m.Handle("POST /settings/jobs/{id}/run", auth(s.handleJobRun))
	m.Handle("POST /settings/jobs/{id}/delete", auth(s.handleJobDelete))
	m.Handle("POST /settings/address", auth(s.handleSettingsAddress))
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

const drivesByCookie = "replicant_drives_by"

// drivesBy resolves which taxonomy the Drives page sections by.
func drivesBy(w http.ResponseWriter, r *http.Request) store.Taxonomy {
	if t := store.Taxonomy(r.URL.Query().Get("by")); t.Valid() {
		http.SetCookie(w, &http.Cookie{Name: drivesByCookie, Value: string(t), Path: "/drives", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
		return t
	}
	if c, err := r.Cookie(drivesByCookie); err == nil && store.Taxonomy(c.Value).Valid() {
		return store.Taxonomy(c.Value)
	}
	return store.ByGroup
}

func taxonomyFrom(r *http.Request) store.Taxonomy {
	if t := store.Taxonomy(r.FormValue("kind")); t.Valid() {
		return t
	}
	return store.ByGroup
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
	by := drivesBy(w, r)
	// Every set gets a section, even when empty, so it can be a drop target.
	groups, err := s.store.ListGroups(r.Context(), by)
	if err != nil {
		s.fail(w, r, err, "groups")
		return
	}
	type group struct {
		ID     int64
		Name   string
		Drives []store.Drive
	}
	byGroup := make([]group, 0, len(groups)+1)
	index := map[int64]int{}
	for _, g := range groups {
		index[g.ID] = len(byGroup)
		byGroup = append(byGroup, group{ID: g.ID, Name: g.Name})
	}
	index[0] = len(byGroup)
	byGroup = append(byGroup, group{ID: 0, Name: ""})
	for _, d := range drives {
		i := index[d.SetIn(by)]
		byGroup[i].Drives = append(byGroup[i].Drives, d)
	}
	if len(groups) == 0 && len(drives) == 0 {
		byGroup = nil
	}
	s.render(w, r, "drives", map[string]any{"Title": "Drives", "Drives": drives, "ByGroup": byGroup, "By": string(by),
		"TotalFiles": files, "TotalClips": clips, "TotalBytes": bytes})
}

// handleDrivePage sends a drive straight to its latest scan in the explorer;
// the drive details live in the explorer's inspector panel.
func (s *Server) handleDrivePage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/browse/%d", id), http.StatusSeeOther)
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
	if _, has := r.Form["group"]; has {
		if err := s.store.SetDriveGroup(r.Context(), store.ByGroup, id, r.FormValue("group")); err != nil {
			s.fail(w, r, err, "update group")
			return
		}
	}
	if _, has := r.Form["client"]; has {
		if err := s.store.SetDriveGroup(r.Context(), store.ByClient, id, r.FormValue("client")); err != nil {
			s.fail(w, r, err, "update client")
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/browse/%d", id), http.StatusSeeOther)
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

// searchScope narrows a query to the drives of a group (?group=ID) or client
// (?client=ID) from the Drives page. An explicit ?drive= wins. It returns a
// label for the page ("group Studio RAIDs") or "" when no scope applies.
func searchScope(r *http.Request, drives []store.Drive, q *store.SearchQuery) string {
	if len(q.DriveIDs) > 0 {
		return ""
	}
	for _, by := range []store.Taxonomy{store.ByGroup, store.ByClient} {
		id, err := strconv.ParseInt(r.URL.Query().Get(string(by)), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		name := ""
		for _, d := range drives {
			if d.SetIn(by) == id {
				q.DriveIDs = append(q.DriveIDs, d.ID)
				if by == store.ByClient {
					name = d.ClientName
				} else {
					name = d.GroupName
				}
			}
		}
		if len(q.DriveIDs) == 0 {
			q.DriveIDs = []int64{-1} // an empty set matches nothing
		}
		if name == "" {
			name = "#" + strconv.FormatInt(id, 10)
		}
		return string(by) + " " + name
	}
	return ""
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := parseSearch(r)
	mode := r.URL.Query().Get("mode")
	drives, err := s.store.ListDrives(ctx)
	if err != nil {
		s.fail(w, r, err, "drives")
		return
	}
	scope := searchScope(r, drives, &q)
	data := map[string]any{"Title": "Search", "Q": q, "Mode": mode, "Form": r.URL.Query(), "Scope": scope}
	data["Drives"] = drives
	groups, _ := s.store.ListGroups(ctx, store.ByGroup)
	data["Groups"] = groups
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
	change := r.URL.Query().Get("change")
	if change != "added" && change != "removed" && change != "changed" {
		change = ""
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	const per = 500
	changes, total, err := s.store.ScanChanges(ctx, id, change, per, (page-1)*per)
	if err != nil {
		s.fail(w, r, err, "changes")
		return
	}
	// A first scan of a root records counts only: every file was added.
	firstScan := sc.Added > 0 && sc.Removed == 0 && sc.Changed == 0 && total == 0 && change != "removed" && change != "changed"
	s.render(w, r, "scan", map[string]any{"Title": fmt.Sprintf("Scan #%d", id), "Scan": sc, "Changes": changes,
		"Change": change, "Total": total, "Page": page, "Pages": (total + per - 1) / per, "Form": r.URL.Query(), "FirstScan": firstScan})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListScanJobs(r.Context())
	if err != nil {
		s.fail(w, r, err, "scan jobs")
		return
	}
	type jobView struct {
		store.ScanJob
		Running bool
		Stage   string
		Done    int
		Total   int
	}
	views := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		v := jobView{ScanJob: j}
		if s.cfg.Sched != nil {
			v.Stage, v.Done, v.Total, _, v.Running = s.cfg.Sched.Running(j.ID)
		}
		views = append(views, v)
	}
	groups, _ := s.store.ListGroups(r.Context(), store.ByGroup)
	clients, _ := s.store.ListGroups(r.Context(), store.ByClient)
	addr, src := s.publicURL(r.Context(), r)
	saved, _ := s.store.GetSetting(r.Context(), publicURLSetting)
	s.render(w, r, "settings", map[string]any{"Title": "Settings", "Backups": s.listBackups(),
		"DefaultView": s.defaultView(r), "Jobs": views, "Mounts": scanner.Mounts(), "Groups": groups,
		"Clients": clients, "SchedEnabled": s.cfg.Sched != nil, "Hostname": hostname(),
		"Address": addr, "AddressSource": src, "AddressSaved": saved, "AddressLocked": s.cfg.PublicURL != "",
		"Addresses": detectAddresses(), "Port": listenPort(s.cfg.Listen),
		"Message": r.URL.Query().Get("msg"), "Error": r.URL.Query().Get("err")})
}

// handleSettingsAddress saves the address baked into connection strings.
// An empty value goes back to auto-detection.
func (s *Server) handleSettingsAddress(w http.ResponseWriter, r *http.Request) {
	v := strings.TrimSpace(r.FormValue("address"))
	if v != "" {
		if !strings.Contains(v, "://") {
			v = "http://" + v
		}
		if _, err := url.Parse(v); err != nil {
			http.Redirect(w, r, "/settings?err="+url.QueryEscape("Not a valid address: "+v)+"#address", http.StatusSeeOther)
			return
		}
		v = strings.TrimRight(v, "/")
	}
	if err := s.store.SetSetting(r.Context(), publicURLSetting, v); err != nil {
		s.fail(w, r, err, "save address")
		return
	}
	msg := "Server address saved."
	if v == "" {
		msg = "Server address will be detected automatically."
	}
	http.Redirect(w, r, "/settings?msg="+url.QueryEscape(msg)+"#address", http.StatusSeeOther)
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}

func (s *Server) handleSettingsView(w http.ResponseWriter, r *http.Request) {
	v := r.FormValue("view")
	if v != "list" && v != "columns" {
		v = "list"
	}
	if err := s.store.SetSetting(r.Context(), defaultViewSetting, v); err != nil {
		s.fail(w, r, err, "save setting")
		return
	}
	// The browser's own cookie would otherwise keep overriding the new default.
	http.SetCookie(w, &http.Cookie{Name: viewCookie, Value: "", Path: "/browse", MaxAge: -1})
	http.Redirect(w, r, "/settings?msg=Default+view+saved.", http.StatusSeeOther)
}

func parseJobForm(r *http.Request) store.ScanJob {
	interval, _ := strconv.Atoi(r.FormValue("interval"))
	return store.ScanJob{
		Path: strings.TrimSpace(r.FormValue("path")), Label: strings.TrimSpace(r.FormValue("label")),
		IntervalMin: interval, Extract: r.FormValue("extract") != "", Fingerprint: r.FormValue("fingerprint") != "",
		Enabled: r.FormValue("enabled") != "",
	}
}

func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	j := parseJobForm(r)
	if j.Path == "" {
		http.Redirect(w, r, "/settings?err=A+path+on+the+server+is+required.", http.StatusSeeOther)
		return
	}
	if fi, err := os.Stat(j.Path); err != nil || !fi.IsDir() {
		http.Redirect(w, r, "/settings?err="+url.QueryEscape("That path is not a folder on this server: "+j.Path), http.StatusSeeOther)
		return
	}
	if _, err := s.store.CreateScanJob(r.Context(), j); err != nil {
		http.Redirect(w, r, "/settings?err="+url.QueryEscape("Could not add: "+err.Error()), http.StatusSeeOther)
		return
	}
	if s.cfg.Sched != nil {
		s.cfg.Sched.Kick()
	}
	http.Redirect(w, r, "/settings?msg=Scan+job+added.#server-scans", http.StatusSeeOther)
}

func (s *Server) handleJobUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	j, err := s.store.GetScanJob(r.Context(), id)
	if err != nil {
		s.fail(w, r, err, "scan job")
		return
	}
	f := parseJobForm(r)
	j.Label, j.IntervalMin, j.Extract, j.Fingerprint, j.Enabled = f.Label, f.IntervalMin, f.Extract, f.Fingerprint, f.Enabled
	if err := s.store.UpdateScanJob(r.Context(), j); err != nil {
		s.fail(w, r, err, "update scan job")
		return
	}
	if s.cfg.Sched != nil {
		s.cfg.Sched.Kick()
	}
	http.Redirect(w, r, "/settings?msg=Scan+job+saved.#server-scans", http.StatusSeeOther)
}

func (s *Server) handleJobRun(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if s.cfg.Sched == nil {
		http.Redirect(w, r, "/settings?err=Server+scans+are+not+enabled.", http.StatusSeeOther)
		return
	}
	go func() {
		if _, err := s.cfg.Sched.RunJob(context.Background(), id); err != nil {
			s.log.Error("manual scan job", "job", id, "err", err)
		}
	}()
	http.Redirect(w, r, "/settings?msg=Scan+started.#server-scans", http.StatusSeeOther)
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.store.DeleteScanJob(r.Context(), id); err != nil {
		s.fail(w, r, err, "delete scan job")
		return
	}
	http.Redirect(w, r, "/settings?msg=Scan+job+removed.#server-scans", http.StatusSeeOther)
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	toks, err := s.store.ListTokens(r.Context())
	if err != nil {
		s.fail(w, r, err, "tokens")
		return
	}
	addr, src := s.publicURL(r.Context(), r)
	s.render(w, r, "apikeys", map[string]any{"Title": "API keys", "Tokens": toks, "Address": addr, "AddressSource": src,
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
	addr, src := s.publicURL(r.Context(), r)
	conn, err := client.ConnectionString(addr, plain)
	if err != nil {
		s.log.Warn("connection string", "address", addr, "err", err)
		conn = plain
	}
	s.render(w, r, "apikeys", map[string]any{"Title": "API keys", "Tokens": toks, "Address": addr, "AddressSource": src,
		"NewToken": plain, "NewTokenName": name, "Connection": conn})
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
		http.Redirect(w, r, "/settings?err=choose+a+.replicant+file", http.StatusSeeOther)
		return
	}
	defer f.Close()
	tmp, err := os.CreateTemp(s.cfg.DataDir, "upload-*.replicant")
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
	http.Redirect(w, r, fmt.Sprintf("/browse/%d", res.DriveID), http.StatusSeeOther)
}

// handleDriveMove moves a drive into a group (group_id 0 ungroups). It is
// called by drag-and-drop on the Drives page.
func (s *Server) handleDriveMove(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	gid, _ := strconv.ParseInt(r.FormValue("group_id"), 10, 64)
	if err := s.store.SetDriveGroupID(r.Context(), taxonomyFrom(r), id, gid); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/drives", http.StatusSeeOther)
}

func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	t := taxonomyFrom(r)
	if _, err := s.store.CreateGroup(r.Context(), t, r.FormValue("name")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/drives?by="+string(t), http.StatusSeeOther)
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	t := taxonomyFrom(r)
	if err := s.store.DeleteGroup(r.Context(), t, id); err != nil {
		s.fail(w, r, err, "delete group")
		return
	}
	http.Redirect(w, r, "/drives?by="+string(t), http.StatusSeeOther)
}
