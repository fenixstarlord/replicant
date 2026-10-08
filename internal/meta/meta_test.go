package meta

import (
	"encoding/json"
	"testing"
)

func TestMergePriorityAndSources(t *testing.T) {
	vendor := Result{Source: "art-cmd", Fields: Fields{ISO: Int(800), CameraModel: Str("ALEXA 35")}, Raw: json.RawMessage(`{"a":1}`)}
	sidecar := Result{Source: "ale", Fields: Fields{ISO: Int(1600), Scene: Str("12A")}}
	probe := Result{Source: "ffprobe", Fields: Fields{Width: Int(4608), CameraModel: Str("wrong")}, Raw: json.RawMessage(`{"streams":[]}`)}
	m := Merge([]Result{vendor, sidecar, probe})
	if *m.Fields.ISO != 800 || *m.Fields.Scene != "12A" || *m.Fields.Width != 4608 || *m.Fields.CameraModel != "ALEXA 35" {
		t.Errorf("merged fields wrong: %+v", m.Fields)
	}
	want := map[string]string{"iso": "art-cmd", "camera_model": "art-cmd", "scene": "ale", "width": "ffprobe"}
	for k, v := range want {
		if m.Sources[k] != v {
			t.Errorf("source[%s] = %q, want %q", k, m.Sources[k], v)
		}
	}
	if len(m.Raw) != 2 || string(m.Raw["art-cmd"]) != `{"a":1}` {
		t.Errorf("raw wrong: %v", m.Raw)
	}
	if got := m.Fields.Set(); len(got) != 4 || got[0] != "camera_model" {
		t.Errorf("Set() = %v", got)
	}
}

func TestFieldsJSONOmitsUnknown(t *testing.T) {
	b, _ := json.Marshal(Fields{FPS: Float(25)})
	if string(b) != `{"fps":25}` {
		t.Errorf("json = %s", b)
	}
}

func TestMergeWeakFieldsLose(t *testing.T) {
	ale := Result{Source: "ale", Fields: Fields{TCStart: Str("00:00:00:00"), Scene: Str("5")}, Weak: map[string]bool{"tc_start": true}}
	probe := Result{Source: "ffprobe", Fields: Fields{TCStart: Str("08:46:50:12")}}
	m := Merge([]Result{ale, probe})
	if *m.Fields.TCStart != "08:46:50:12" || m.Sources["tc_start"] != "ffprobe" || *m.Fields.Scene != "5" {
		t.Errorf("weak merge wrong: %+v %v", m.Fields, m.Sources)
	}
	only := Merge([]Result{ale})
	if *only.Fields.TCStart != "00:00:00:00" || only.Sources["tc_start"] != "ale" {
		t.Errorf("weak value should be used when alone: %+v", only.Fields)
	}
}
