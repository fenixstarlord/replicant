package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/fenixstarlord/indexserver/internal/store"
)

// column is one Finder-style column: the listing of a folder, with the
// child on the current path marked as selected.
type column struct {
	Path     string
	Entries  []store.EntryRow
	Selected string // path of the selected child, if any
}

const viewCookie = "shelf_view"

// browseView resolves the explorer view from the query, then the cookie.
func browseView(w http.ResponseWriter, r *http.Request) string {
	v := r.URL.Query().Get("view")
	switch v {
	case "list", "columns":
		http.SetCookie(w, &http.Cookie{Name: viewCookie, Value: v, Path: "/browse", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
		return v
	}
	if c, err := r.Cookie(viewCookie); err == nil && (c.Value == "list" || c.Value == "columns") {
		return c.Value
	}
	return "columns"
}

// handleBrowse renders the explorer page, or a fragment of it:
//
//	?frag=rows&depth=N  list-view child rows for a folder (twirl-out)
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

	// Twirl-out fragment: the children of one folder as table rows.
	if r.URL.Query().Get("frag") == "rows" {
		depth, _ := strconv.Atoi(r.URL.Query().Get("depth"))
		rows, err := s.store.ListDir(ctx, sc.ID, p)
		if err != nil {
			s.fail(w, r, err, "list dir")
			return
		}
		s.renderFragment(w, "browse", "browse_rows", map[string]any{"Drive": d, "Scan": sc, "Entries": rows, "Depth": depth + 1})
		return
	}

	// The path may point at a file: then the listing is of its parent and
	// the file is previewed.
	var target store.EntryRow
	dirPath := p
	if p != "" {
		target, err = s.store.EntryByPath(ctx, sc.ID, p)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.fail(w, r, err, "entry")
			return
		}
		if err == nil && !target.IsDir {
			dirPath = target.ParentPath
		}
	}
	view := browseView(w, r)
	data := map[string]any{
		"Title": d.Name + " / " + p, "Drive": d, "Scan": sc, "View": view,
		"DirPath": dirPath, "Crumbs": crumbs(dirPath), "Target": target, "IsFile": target.ID != 0 && !target.IsDir,
	}
	if target.ID == 0 {
		data["Dir"] = store.EntryRow{IsDir: true, DirTotalSize: sc.TotalBytes, DirFileCount: int64(sc.FileCount)}
	} else if target.IsDir {
		data["Dir"] = target
	} else {
		parent, err := s.store.EntryByPath(ctx, sc.ID, dirPath)
		if err != nil {
			parent = store.EntryRow{IsDir: true, DirTotalSize: sc.TotalBytes, DirFileCount: int64(sc.FileCount)}
		}
		data["Dir"] = parent
	}

	if view == "columns" {
		// One column per ancestor, root first; each marks the next path segment.
		var cols []column
		segs := []string{}
		if dirPath != "" {
			segs = strings.Split(dirPath, "/")
		}
		cur := ""
		for i := 0; i <= len(segs); i++ {
			rows, err := s.store.ListDir(ctx, sc.ID, cur)
			if err != nil {
				s.fail(w, r, err, "list dir")
				return
			}
			col := column{Path: cur, Entries: rows}
			if i < len(segs) {
				col.Selected = strings.TrimPrefix(cur+"/"+segs[i], "/")
				cur = col.Selected
			} else if target.ID != 0 && !target.IsDir {
				col.Selected = target.Path
			}
			cols = append(cols, col)
		}
		data["Columns"] = cols
		if target.ID != 0 && !target.IsDir {
			copies, _ := s.store.Copies(ctx, target)
			data["Copies"] = copies
		}
	} else {
		rows, err := s.store.ListDir(ctx, sc.ID, dirPath)
		if err != nil {
			s.fail(w, r, err, "list dir")
			return
		}
		data["Entries"] = rows
		data["Depth"] = 0
	}
	s.render(w, r, "browse", data)
}

// renderFragment writes one named template from a page's template set.
func (s *Server) renderFragment(w http.ResponseWriter, page, name string, data map[string]any) {
	t, ok := pages[page]
	if !ok {
		http.Error(w, "no template "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render fragment", "page", page, "name", name, "err", err)
	}
}
