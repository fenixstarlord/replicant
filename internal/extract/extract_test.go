package extract

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fenixstarlord/replicant/internal/clips"
	"github.com/fenixstarlord/replicant/internal/meta"
)

// slowExtractor waits for delay or for its context, whichever comes first.
type slowExtractor struct{ delay time.Duration }

func (slowExtractor) Name() string                             { return "slow" }
func (slowExtractor) Priority() int                            { return PriorityProbe }
func (slowExtractor) Available(context.Context) (bool, string) { return true, "test" }
func (slowExtractor) Matches(*clips.Clip) bool                 { return true }
func (s slowExtractor) Extract(ctx context.Context, _ string, _ *clips.Clip) (*meta.Result, error) {
	select {
	case <-time.After(s.delay):
		return &meta.Result{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestClipTimeoutAndTimings(t *testing.T) {
	cases := []struct {
		name        string
		timeout     time.Duration
		wantTimeout bool
	}{
		{"short timeout fires", 20 * time.Millisecond, true},
		{"negative means no limit", -1, false},
		{"zero means the default", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRunner(context.Background(), []Extractor{slowExtractor{delay: 150 * time.Millisecond}})
			c := clips.Clip{ID: "c"}
			events := []Progress{}
			job, err := r.StartClip(t.Context(), t.TempDir(), &c, Options{
				ClipTimeout: tc.timeout,
				Progress:    func(p Progress) { events = append(events, p) },
			})
			if err != nil {
				t.Fatal(err)
			}
			job.Finish(context.Background())
			timedOut := len(c.Errors) == 1 && strings.Contains(c.Errors[0], "timed out after")
			if timedOut != tc.wantTimeout {
				t.Errorf("errors = %v, want timeout %v", c.Errors, tc.wantTimeout)
			}
			tm := r.Timings()
			if len(tm) != 1 || tm[0].Name != "slow" || tm[0].Runs != 1 || tm[0].Total <= 0 {
				t.Fatalf("timings = %+v", tm)
			}
			if got := tm[0].Timeouts == 1 && tm[0].Errors == 1; got != tc.wantTimeout {
				t.Errorf("timings = %+v, want timeout %v", tm[0], tc.wantTimeout)
			}
			if len(events) != 2 || events[0].Event != "start" || events[1].Event != "finish" {
				t.Fatalf("progress events = %+v", events)
			}
			if events[1].Elapsed <= 0 || strings.Contains(events[1].Detail, "timed out after") != tc.wantTimeout {
				t.Errorf("completion did not report duration/timeout: %+v", events[1])
			}
		})
	}
}

func TestOptionsClipTimeout(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{0: DefaultClipTimeout, -time.Second: 0, 5 * time.Minute: 5 * time.Minute} {
		if got := (Options{ClipTimeout: in}).clipTimeout(); got != want {
			t.Errorf("clipTimeout(%s) = %s, want %s", in, got, want)
		}
	}
}

// funcExtractor runs fn; name and priority are fixed.
type funcExtractor struct {
	name string
	fn   func(ctx context.Context) (*meta.Result, error)
}

func (f funcExtractor) Name() string                           { return f.name }
func (funcExtractor) Priority() int                            { return PrioritySidecar }
func (funcExtractor) Available(context.Context) (bool, string) { return true, "test" }
func (funcExtractor) Matches(*clips.Clip) bool                 { return true }
func (f funcExtractor) Extract(ctx context.Context, _ string, _ *clips.Clip) (*meta.Result, error) {
	return f.fn(ctx)
}

func TestCancelRecordsExtractorsNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := funcExtractor{"first", func(context.Context) (*meta.Result, error) {
		cancel() // the user cancels while this extractor runs
		return &meta.Result{}, nil
	}}
	second := funcExtractor{"second", func(context.Context) (*meta.Result, error) {
		t.Error("an extractor started after cancellation")
		return nil, nil
	}}
	r := NewRunner(context.Background(), []Extractor{first, second})
	cl := []clips.Clip{{ID: "c"}}
	err := r.Run(ctx, t.TempDir(), cl, Options{Workers: 1}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
	if len(cl[0].Errors) != 1 || !strings.HasPrefix(cl[0].Errors[0], "second: not run: context canceled") {
		t.Errorf("errors = %q", cl[0].Errors)
	}
}
