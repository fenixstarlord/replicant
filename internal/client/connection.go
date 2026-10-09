package client

import (
	"fmt"
	"net/url"
	"strings"
)

// A connection string carries the server address and the API token in one
// pasteable value so a client can be set up with a single copy and paste:
//
//	replicant://replicant_abc123@100.64.0.5:8080        (http)
//	replicants://replicant_abc123@replicant.example.com     (https)
//
// A plain http(s) URL with the token in the user part is accepted too.

// ConnectionString builds the string for a server URL and token.
func ConnectionString(server, token string) (string, error) {
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("server URL must look like http://host:8080, got %q", server)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "replicant"
	case "https":
		u.Scheme = "replicants"
	default:
		return "", fmt.Errorf("server URL must be http or https, got %q", server)
	}
	u.User = url.User(token)
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// ParseConnection splits a connection string into server URL and token.
// A URL without a token returns token == "" and no error, so callers can
// fall back to prompting.
func ParseConnection(s string) (server, token string, err error) {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("expected replicant://key@host:port or http://host:port, got %q", s)
	}
	switch u.Scheme {
	case "replicant":
		u.Scheme = "http"
	case "replicants":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", "", fmt.Errorf("unsupported scheme %q in %q", u.Scheme, s)
	}
	if u.User != nil {
		token = u.User.Username()
		if p, ok := u.User.Password(); ok && token == "" {
			token = p
		}
		u.User = nil
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), token, nil
}
