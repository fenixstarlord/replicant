package braw

import "testing"

func TestMap(t *testing.T) {
	kv := map[string]any{
		"iso": 800.0, "white_balance_kelvin": 5600.0, "white_balance_tint": -2.0, "shutter_angle": 180.0,
		"lens_type": "Sigma 18-35", "focal_length": "24mm", "aperture": "f2.8", "distance": "1.2m",
		"lut_name": "Blackmagic Design Film to Video", "gamma": "Blackmagic Design Film", "gamut": "Blackmagic Design",
		"camera_type": "Blackmagic URSA Mini Pro 12K", "camera_id": "123", "reel_name": "A001", "good_take": "true",
	}
	f := Map(kv)
	if *f.ISO != 800 || *f.WBKelvin != 5600 || *f.Tint != -2 || *f.ShutterAngle != 180 || *f.Lens != "Sigma 18-35" || *f.TStop != 2.8 || *f.FocusDistance != "1.2m" {
		t.Errorf("fields wrong: %v", f.Set())
	}
	if *f.ColorGamma != "Blackmagic Design / Blackmagic Design Film" || *f.CameraMake != "Blackmagic Design" || *f.Reel != "A001" || *f.Circled != true {
		t.Errorf("fields wrong: %v", f.Set())
	}
	if f.FocalMM != nil {
		t.Errorf("'24mm' is not numeric, should stay nil: %v", *f.FocalMM)
	}
}
