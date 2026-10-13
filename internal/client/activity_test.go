package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fenixstarlord/replicant/internal/api"
)

// activityServer records every progress update it receives.
func activityServer(t *testing.T) (*Client, func() []api.ActivityUpdate) {
	t.Helper()
	var mu sync.Mutex
	var got []api.ActivityUpdate
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(api.ActivityResponse{ID: 7})
			return
		}
		if r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, api.PathActivity+"/") {
			var u api.ActivityUpdate
			json.NewDecoder(r.Body).Decode(&u)
			mu.Lock()
			got = append(got, u)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	return c, func() []api.ActivityUpdate {
		mu.Lock()
		defer mu.Unlock()
		return append([]api.ActivityUpdate(nil), got...)
	}
}

// waitFor polls until cond holds or a second passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the sender")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestProgressThrottlesAlternatingStages(t *testing.T) {
	c, sent := activityServer(t)
	rep, err := NewActivityReporter(context.Background(), c, api.ActivityStart{Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	// A tape scan alternates stages for every clip.
	for i := 1; i <= 60; i++ {
		rep.Progress("extract", i, 60)
		rep.Progress("hash", i, 60)
	}
	waitFor(t, func() bool {
		got := sent()
		return len(got) > 0 && got[len(got)-1] == api.ActivityUpdate{Stage: "hash", Done: 60, Total: 60}
	})
	// At most the first report of each stage and the two final ones; the
	// sender may coalesce some of them.
	if got := sent(); len(got) > 4 {
		t.Errorf("sent %d updates: %+v", len(got), got)
	}
}

// A server that never answers must not hold up the scan.
func TestProgressDoesNotBlockOnSlowServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewEncoder(w).Encode(api.ActivityResponse{ID: 7})
			return
		}
		<-release
	}))
	defer srv.Close()
	defer close(release)
	c, _ := New(srv.URL, "t")
	rep, err := NewActivityReporter(context.Background(), c, api.ActivityStart{Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 1; i <= 100; i++ {
		rep.Progress("hash", i, 100)
		rep.Progress("extract", i, 100)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("200 progress calls took %s against a stuck server", d)
	}
}

func TestKeepaliveResendsLastProgress(t *testing.T) {
	c, sent := activityServer(t)
	rep, err := NewActivityReporter(context.Background(), c, api.ActivityStart{Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	rep.interval = 20 * time.Millisecond
	rep.Progress("hash", 3, 10)
	waitFor(t, func() bool { return len(sent()) == 1 })
	rep.Keepalive() // too soon: nothing new
	time.Sleep(30 * time.Millisecond)
	rep.Keepalive()
	waitFor(t, func() bool { return len(sent()) == 2 })
	if got := sent(); got[1] != got[0] {
		t.Errorf("sent %+v, want the same update twice", got)
	}
	rep.Finish("cancelled", "")
	time.Sleep(30 * time.Millisecond)
	rep.Keepalive()
	rep.Progress("hash", 4, 10)
	time.Sleep(20 * time.Millisecond)
	if n := len(sent()); n != 3 { // the finish, and nothing after it
		t.Errorf("sent %d updates after finish, want 3", n)
	}
	var none *ActivityReporter
	none.Keepalive()
	(&ActivityReporter{}).Keepalive()
}
