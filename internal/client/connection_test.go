package client

import "testing"

func TestConnectionStringRoundTrip(t *testing.T) {
	cases := []struct {
		server, token, want string
	}{
		{"http://100.64.0.5:8080", "shelf_abc", "shelf://shelf_abc@100.64.0.5:8080"},
		{"http://100.64.0.5:8080/", "shelf_abc", "shelf://shelf_abc@100.64.0.5:8080"},
		{"https://shelf.example.com", "shelf_x-y_z", "shelfs://shelf_x-y_z@shelf.example.com"},
		{"http://host:8080/prefix", "shelf_abc", "shelf://shelf_abc@host:8080/prefix"},
	}
	for _, c := range cases {
		got, err := ConnectionString(c.server, c.token)
		if err != nil || got != c.want {
			t.Errorf("ConnectionString(%q) = %q, %v; want %q", c.server, got, err, c.want)
			continue
		}
		server, token, err := ParseConnection(got)
		if err != nil || token != c.token {
			t.Errorf("ParseConnection(%q) = %q %q %v", got, server, token, err)
		}
		if want, _, _ := ParseConnection(c.server); server != want {
			t.Errorf("ParseConnection(%q) server = %q, want %q", got, server, want)
		}
	}
}

func TestParseConnectionForms(t *testing.T) {
	cases := []struct {
		in, server, token string
		wantErr           bool
	}{
		{"shelf://shelf_abc@host:8080", "http://host:8080", "shelf_abc", false},
		{"  shelfs://shelf_abc@host  ", "https://host", "shelf_abc", false},
		{"http://shelf_abc@host:8080", "http://host:8080", "shelf_abc", false},
		{"http://host:8080", "http://host:8080", "", false},
		{"http://host:8080/", "http://host:8080", "", false},
		{"host:8080", "", "", true},
		{"ftp://x@host", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		server, token, err := ParseConnection(c.in)
		if (err != nil) != c.wantErr || server != c.server || token != c.token {
			t.Errorf("ParseConnection(%q) = %q %q %v; want %q %q err=%v", c.in, server, token, err, c.server, c.token, c.wantErr)
		}
	}
}

func TestConnectionStringRejectsBadServer(t *testing.T) {
	for _, s := range []string{"", "host:8080", "ftp://host"} {
		if _, err := ConnectionString(s, "shelf_abc"); err == nil {
			t.Errorf("ConnectionString(%q) should fail", s)
		}
	}
}
