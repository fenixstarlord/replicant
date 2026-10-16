package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fenixstarlord/replicant/internal/api"
)

func TestProfilesAPI(t *testing.T) {
	s, st := newTestServer(t)
	token, _, err := st.CreateToken(t.Context(), "profiles test")
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, body string, auth bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, api.PathProfiles, strings.NewReader(body))
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "", false); w.Code != 401 {
		t.Fatalf("unauthenticated read: %d", w.Code)
	}
	if w := do("PUT", `{}`, false); w.Code != 401 {
		t.Fatalf("unauthenticated save: %d", w.Code)
	}
	body := `{"key":"tape-uuid","name":"EGG09","volume_name":"1097007720","extractors":[],"fingerprint":false,"new_group":"Shelf","new_client":"Client"}`
	if w := do("PUT", body, true); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	w := do("GET", "", true)
	var catalog api.ProfilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Profiles) != 1 || catalog.Profiles[0].Indexed || catalog.Profiles[0].Fingerprint || len(catalog.Profiles[0].Extractors) != 0 || len(catalog.Groups) != 1 || len(catalog.Clients) != 1 {
		t.Fatalf("catalog: %+v", catalog)
	}
	if w := do("PUT", body, true); w.Code != 409 {
		t.Fatalf("stale save: %d %s", w.Code, w.Body.String())
	}
	if w := do("PUT", `{"key":"new","extractors":["bogus"]}`, true); w.Code != 400 {
		t.Fatalf("invalid extractor: %d", w.Code)
	}
}
