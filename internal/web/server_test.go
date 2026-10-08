package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fenixstarlord/indexserver/internal/api"
	"github.com/fenixstarlord/indexserver/internal/bundle"
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

	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "session") {
		t.Errorf("home with session: %d %s", rec.Code, rec.Body.String())
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
