package web

import (
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		iface, ip string
		kind      string
		ok        bool
	}{
		{"wt0", "100.64.3.9", "netbird", true},
		{"utun100", "100.114.10.93", "netbird", true},
		{"tailscale0", "100.100.1.1", "netbird", true},
		{"eth0", "192.168.1.20", "lan", true},
		{"en0", "10.0.0.7", "lan", true},
		{"eth0", "203.0.113.5", "other", true},
		{"lo", "127.0.0.1", "", false},
		{"docker0", "172.17.0.1", "", false},
		{"br-1234", "172.18.0.1", "", false},
		{"en0", "fe80::1", "", false},
		{"en0", "2001:db8::1", "", false},
	}
	for _, c := range cases {
		kind, ok := classify(c.iface, netip.MustParseAddr(c.ip))
		if kind != c.kind || ok != c.ok {
			t.Errorf("classify(%s, %s) = %q %v; want %q %v", c.iface, c.ip, kind, ok, c.kind, c.ok)
		}
	}
}

func TestListenParts(t *testing.T) {
	if got := listenPort(":8080"); got != "8080" {
		t.Errorf("listenPort(:8080) = %q", got)
	}
	if got := listenPort("garbage"); got != "8080" {
		t.Errorf("listenPort(garbage) = %q", got)
	}
	if got := listenPort("100.64.0.5:9000"); got != "9000" {
		t.Errorf("listenPort(host:9000) = %q", got)
	}
	if got := listenHost(":8080"); got != "" {
		t.Errorf("listenHost(:8080) = %q", got)
	}
	if got := listenHost("0.0.0.0:8080"); got != "" {
		t.Errorf("listenHost(0.0.0.0:8080) = %q", got)
	}
	if got := listenHost("100.64.0.5:8080"); got != "100.64.0.5" {
		t.Errorf("listenHost = %q", got)
	}
}
