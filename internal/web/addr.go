package web

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
)

// The address clients should use to reach this server. It is baked into
// connection strings on the API keys page. Precedence:
//
//  1. REPLICANT_PUBLIC_URL (Config.PublicURL), for Docker and other setups where
//     the container cannot see the host's interfaces.
//  2. The "public_url" setting saved on the Settings page.
//  3. A host given in REPLICANT_LISTEN (e.g. 100.64.0.5:8080).
//  4. The NetBird (or other mesh) interface, if the server has one.
//  5. The host the browser used for this request.

const publicURLSetting = "public_url"

// Address is a candidate address the server is reachable at.
type Address struct {
	IP    string
	Iface string
	Kind  string // "netbird", "lan", or "other"
}

// Label is a short human description, e.g. "NetBird (wt0)".
func (a Address) Label() string {
	switch a.Kind {
	case "netbird":
		return "NetBird (" + a.Iface + ")"
	case "lan":
		return "LAN (" + a.Iface + ")"
	default:
		return a.Iface
	}
}

// cgnat is 100.64.0.0/10, the range NetBird (and Tailscale) hand out.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// classify decides what kind of address an interface carries. Interface
// names: NetBird is wt0 on Linux, utunN on macOS; Tailscale is tailscale0.
func classify(iface string, ip netip.Addr) (kind string, ok bool) {
	if !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return "", false
	}
	switch {
	case strings.HasPrefix(iface, "docker"), strings.HasPrefix(iface, "br-"), strings.HasPrefix(iface, "veth"),
		strings.HasPrefix(iface, "virbr"), strings.HasPrefix(iface, "vnet"), strings.HasPrefix(iface, "kube"):
		return "", false
	case iface == "wt0", strings.HasPrefix(iface, "netbird"), strings.HasPrefix(iface, "nb"), cgnat.Contains(ip):
		return "netbird", true
	case ip.IsPrivate():
		return "lan", true
	default:
		return "other", true
	}
}

// detectAddresses lists the server's reachable IPv4 addresses, NetBird first.
func detectAddresses() []Address {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Address
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipn.IP)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			if kind, ok := classify(ifc.Name, ip); ok {
				out = append(out, Address{IP: ip.String(), Iface: ifc.Name, Kind: kind})
			}
		}
	}
	rank := map[string]int{"netbird": 0, "lan": 1, "other": 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Kind] < rank[out[j].Kind] })
	return out
}

// listenPort returns the port part of a listen address such as ":8080".
func listenPort(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "8080"
	}
	return port
}

// listenHost returns the host part of a listen address if it names one.
func listenHost(listen string) string {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		return ""
	}
	return host
}

// PublicURL resolves the address to put in connection strings without a
// request to fall back on. It returns "" when nothing is configured and no
// mesh interface is present; source says where the value came from.
func PublicURL(ctx context.Context, st settingsReader, publicURL, listen string) (u, source string) {
	if v := strings.TrimRight(publicURL, "/"); v != "" {
		return v, "REPLICANT_PUBLIC_URL"
	}
	if v, _ := st.GetSetting(ctx, publicURLSetting); strings.TrimSpace(v) != "" {
		return strings.TrimRight(strings.TrimSpace(v), "/"), "Settings"
	}
	port := listenPort(listen)
	if h := listenHost(listen); h != "" {
		return "http://" + net.JoinHostPort(h, port), "REPLICANT_LISTEN"
	}
	for _, a := range detectAddresses() {
		if a.Kind == "netbird" {
			return "http://" + net.JoinHostPort(a.IP, port), "detected " + a.Label()
		}
	}
	return "", ""
}

type settingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// publicURL is PublicURL with the request's host as the last resort.
func (s *Server) publicURL(ctx context.Context, r *http.Request) (u, source string) {
	if u, source = PublicURL(ctx, s.store, s.cfg.PublicURL, s.cfg.Listen); u != "" {
		return u, source
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host, "this page's address"
}
