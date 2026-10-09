package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fenixstarlord/indexserver/internal/api"
	"github.com/fenixstarlord/indexserver/internal/bundle"
	"github.com/fenixstarlord/indexserver/internal/clips"
	"github.com/fenixstarlord/indexserver/internal/meta"
	"github.com/fenixstarlord/indexserver/internal/scan"
	"github.com/fenixstarlord/indexserver/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s, err := New(context.Background(), st, Config{Password: "hunter2", DataDir: t.TempDir(), Version: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func testBundle(t *testing.T) []byte {
	t.Helper()
	now := time.Now().UTC()
	entries := []scan.Entry{{Path: "a.mov", Name: "a.mov", Ext: "mov", Kind: scan.KindVideo, Size: 1, ModTime: now}}
	var buf bytes.Buffer
	if err := bundle.Write(&buf, bundle.Manifest{ScannedAt: now, Volume: scan.Volume{UUID: "U1", Name: "Drive1"}}, entries, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAuthRequired(t *testing.T) {
	s, _ := newTestServer(t)
	for _, c := range []struct {
		method, path string
		wantStatus   int
	}{
		{"GET", "/api/drives", 401},
		{"POST", "/api/scans", 401},
		{"GET", "/api/me", 401},
		{"GET", "/", 303}, // redirect to login
		{"GET", "/healthz", 200},
		{"GET", "/login", 200},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != c.wantStatus {
			t.Errorf("%s %s = %d, want %d (%s)", c.method, c.path, rec.Code, c.wantStatus, rec.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "/api/drives", nil)
	req.Header.Set("Authorization", "Bearer shelf_bogus")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("bogus token = %d", rec.Code)
	}
}

func TestTokenUploadAndDrives(t *testing.T) {
	s, st := newTestServer(t)
	plain, _, err := st.CreateToken(context.Background(), "laptop")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/scans", bytes.NewReader(testBundle(t)))
	req.Header.Set("Authorization", "Bearer "+plain)
	req.Header.Set("Content-Type", "application/zip")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	var res api.IngestResponse
	json.Unmarshal(rec.Body.Bytes(), &res)
	if res.DriveName != "Drive1" || res.Files != 1 || !res.FirstScan {
		t.Errorf("ingest response wrong: %+v", res)
	}

	req = httptest.NewRequest("GET", "/api/drives", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var dr api.DrivesResponse
	json.Unmarshal(rec.Body.Bytes(), &dr)
	if rec.Code != 200 || len(dr.Drives) != 1 || dr.Drives[0].Name != "Drive1" || dr.Drives[0].FileCount != 1 {
		t.Errorf("drives = %d %+v", rec.Code, dr)
	}

	req = httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var me api.MeResponse
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Auth != "token" || me.TokenName != "laptop" {
		t.Errorf("me = %+v", me)
	}

	// Garbage upload is a 400 and leaves no scan behind.
	req = httptest.NewRequest("POST", "/api/scans", strings.NewReader("not a zip"))
	req.Header.Set("Authorization", "Bearer "+plain)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("garbage upload = %d", rec.Code)
	}
}

func TestPasswordLoginAndSession(t *testing.T) {
	s, _ := newTestServer(t)
	form := url.Values{"password": {"wrong"}, "next": {"/"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "failed=1") || len(rec.Result().Cookies()) != 0 {
		t.Errorf("wrong password: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	form.Set("password", "hunter2")
	req = httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()
	if rec.Code != 303 || len(cookies) != 1 || cookies[0].Name != sessionCookie {
		t.Fatalf("login: %d cookies=%v", rec.Code, cookies)
	}

	req = httptest.NewRequest("GET", "/drives", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Drives") || !strings.Contains(rec.Body.String(), "Log out") {
		t.Errorf("drives with session: %d %.200s", rec.Code, rec.Body.String())
	}

	// Tampered and expired cookies are rejected.
	bad := *cookies[0]
	bad.Value = bad.Value[:len(bad.Value)-1] + "0"
	req = httptest.NewRequest("GET", "/api/me", nil)
	req.AddCookie(&bad)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("tampered cookie accepted: %d", rec.Code)
	}
	if s.validSession(s.newSessionValue(time.Now().Add(-2*sessionTTL)), time.Now()) {
		t.Error("expired session accepted")
	}

	// bcrypt hash config path.
	st2, _ := store.Open(":memory:")
	defer st2.Close()
	s2, err := New(context.Background(), st2, Config{PasswordHash: "$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5XH1T2Y0C9Jm5l0yC0Y3v9m6cJ2m2", DataDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s2.checkPassword("nope") {
		t.Error("bcrypt accepted a wrong password")
	}
	if _, err := New(context.Background(), st2, Config{}, nil); err == nil {
		t.Error("expected error when no password is configured")
	}
}

// seedPages ingests a small bundle with metadata and returns a session cookie.
func seedPages(t *testing.T) (*Server, *http.Cookie, api.IngestResponse) {
	t.Helper()
	s, st := newTestServer(t)
	now := time.Now().UTC()
	entries := []scan.Entry{
		{Path: "A001", Name: "A001", Kind: scan.KindDir, IsDir: true, ModTime: now},
		{Path: "A001/A001C001.mxf", Name: "A001C001.mxf", Ext: "mxf", Kind: scan.KindVideo, Size: 1234, ModTime: now, Fingerprint: "00000000deadbeef", ClipID: "k1"},
		{Path: "notes.txt", Name: "notes.txt", Ext: "txt", Kind: scan.KindSidecar, Size: 5, ModTime: now},
	}
	cl := []clips.Clip{{ID: "k1", Kind: clips.KindFile, Name: "A001C001", RootPath: "A001/A001C001.mxf", Files: []string{"A001/A001C001.mxf"}, FileCount: 1, TotalSize: 1234, ModTime: now,
		Meta:    &meta.Fields{Codec: meta.Str("ARRICORE"), Width: meta.Int(4608), Height: meta.Int(3164), FPS: meta.Float(24), CameraModel: meta.Str("ALEXA 35"), ISO: meta.Int(800), TCStart: meta.Str("08:46:50:00")},
		Sources: map[string]string{"codec": "ale", "iso": "ale"}, Raw: map[string]json.RawMessage{"ale": json.RawMessage(`{"Name":"A001C001"}`)}}}
	var buf bytes.Buffer
	if err := bundle.Write(&buf, bundle.Manifest{ScannedAt: now, Volume: scan.Volume{UUID: "U9", Name: "Shelf9"}}, entries, cl); err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.Ingest(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"password": {"hunter2"}, "next": {"/"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return s, rec.Result().Cookies()[0], res
}

func get(t *testing.T, s *Server, c *http.Cookie, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestPagesRender(t *testing.T) {
	s, c, res := seedPages(t)
	var clipID, entryID int64
	if err := s.store.DB.QueryRow(`SELECT id FROM clips WHERE scan_id = ?`, res.ScanID).Scan(&clipID); err != nil {
		t.Fatal(err)
	}
	if err := s.store.DB.QueryRow(`SELECT id FROM entries WHERE scan_id = ? AND path = 'A001/A001C001.mxf'`, res.ScanID).Scan(&entryID); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want []string
	}{
		{"/drives", []string{"Shelf9", "Browse"}},
		{fmt.Sprintf("/drives/%d", res.DriveID), []string{"Scan history", "latest", "Where is it?"}},
		{"/search?q=c001", []string{"1 clips", "A001C001", "ARRICORE", "4608x3164", "ALEXA 35"}},
		{"/search?camera=ALEXA+35&fps=24&iso_min=800&iso_max=800", []string{"1 clips", "A001C001"}},
		{"/search?q=zzz", []string{"0 clips", "No clips match"}},
		{"/search?mode=files&q=notes", []string{"1 files", "notes.txt"}},
		{fmt.Sprintf("/browse/%d", res.DriveID), []string{"A001/", "notes.txt"}},
		{fmt.Sprintf("/browse/%d/A001", res.DriveID), []string{"A001C001.mxf", "file"}},
		{fmt.Sprintf("/clips/%d", clipID), []string{"A001C001", "ARRICORE", ">ale<", "08:46:50:00", "Raw extractor output", "location not set"}},
		{fmt.Sprintf("/files/%d", entryID), []string{"A001C001.mxf", "00000000deadbeef", "file clip"}},
		{fmt.Sprintf("/scans/%d", res.ScanID), []string{"Scan #", "Changes since the previous scan"}},
		{"/settings", []string{"Upload a scan", "Backups"}},
		{"/settings/api-keys", []string{"API keys", "Create a key", "No keys yet"}},
	}
	for _, tc := range cases {
		code, body := get(t, s, c, tc.path)
		if code != 200 {
			t.Errorf("%s: status %d: %.300s", tc.path, code, body)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", tc.path, w)
			}
		}
	}
	if code, _ := get(t, s, c, "/clips/999999"); code != 404 {
		t.Errorf("missing clip = %d", code)
	}
	// htmx request gets only the content block, no <html>.
	req := httptest.NewRequest("GET", "/search?q=c001", nil)
	req.AddCookie(c)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), `id="results"`) {
		t.Errorf("htmx fragment wrong: %.200s", rec.Body.String())
	}
	// Drive label edit round-trips.
	form := url.Values{"label": {"2TB LACIE"}, "location": {"SHELF B, BOX 3"}, "notes": {""}}
	req = httptest.NewRequest("POST", fmt.Sprintf("/drives/%d", res.DriveID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 303 {
		t.Errorf("edit = %d", rec.Code)
	}
	if _, body := get(t, s, c, fmt.Sprintf("/clips/%d", clipID)); !strings.Contains(body, "SHELF B, BOX 3") {
		t.Errorf("location not shown on clip page")
	}
	// Static assets are served.
	if code, body := get(t, s, c, "/static/app.css"); code != 200 || !strings.Contains(body, "silk") {
		t.Errorf("static css = %d", code)
	}
}

func TestHistoryPagesAndExports(t *testing.T) {
	s, c, res := seedPages(t)
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/duplicates", []string{"Duplicates", "No duplicates found"}},
		{fmt.Sprintf("/scans/%d/diff/%d", res.ScanID, res.ScanID), []string{"Diff", "Added", "None"}},
		{"/export.csv?q=c001", []string{"drive,label,location,path,clip", "A001C001", "ARRICORE"}},
		{"/export.ale?q=c001", []string{"Heading", "Column", "A001C001.mxf", "ALEXA 35"}},
	} {
		code, body := get(t, s, c, tc.path)
		if code != 200 {
			t.Errorf("%s: %d %.200s", tc.path, code, body)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", tc.path, w)
			}
		}
	}
	req := httptest.NewRequest("POST", "/settings/backup", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "Backup+written") {
		t.Fatalf("backup = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	names := s.listBackups()
	if len(names) != 1 {
		t.Fatalf("backups = %v", names)
	}
	if code, body := get(t, s, c, "/settings"); code != 200 || !strings.Contains(body, names[0]) {
		t.Errorf("settings should list the backup")
	}
	if code, body := get(t, s, c, "/settings/backup/"+names[0]); code != 200 || !strings.HasPrefix(body, "SQLite format 3") {
		t.Errorf("backup download = %d", code)
	}
	if code, _ := get(t, s, c, "/settings/backup/..%2F..%2Fetc%2Fpasswd"); code != 404 {
		t.Errorf("path traversal = %d", code)
	}
}

func TestAPIKeyCreateAndRevokeViaUI(t *testing.T) {
	s, c, _ := seedPages(t)
	form := url.Values{"name": {"MacBook Pro"}}
	req := httptest.NewRequest("POST", "/settings/tokens", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "shelf_") || !strings.Contains(body, "MacBook Pro") || !strings.Contains(body, "shown only once") {
		t.Fatalf("create key: %d %.300s", rec.Code, body)
	}
	toks, _ := s.store.ListTokens(context.Background())
	if len(toks) != 1 {
		t.Fatalf("tokens = %d", len(toks))
	}
	req = httptest.NewRequest("POST", fmt.Sprintf("/settings/tokens/%d/revoke", toks[0].ID), nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "/settings/api-keys") {
		t.Errorf("revoke = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if toks, _ := s.store.ListTokens(context.Background()); len(toks) != 0 {
		t.Errorf("token not revoked")
	}
}
