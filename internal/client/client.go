// Package client talks to a replicant-server over HTTP with an API token.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fenixstarlord/indexserver/internal/api"
	"github.com/fenixstarlord/indexserver/internal/store"
)

// Client is an authenticated API client.
type Client struct {
	Server string
	Token  string
	HTTP   *http.Client
}

// New returns a client for server (scheme and host, optional path prefix).
func New(server, token string) (*Client, error) {
	server = strings.TrimRight(server, "/")
	u, err := url.Parse(server)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("server URL must look like http://host:8080, got %q", server)
	}
	return &Client{Server: server, Token: token, HTTP: &http.Client{Timeout: 0}}, nil
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.Server+path, body)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e api.ErrorResponse
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s %s: %s (HTTP %d)", method, path, e.Error, resp.StatusCode)
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Me verifies the token and returns the server's view of it.
func (c *Client) Me(ctx context.Context) (api.MeResponse, error) {
	var me api.MeResponse
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := c.do(ctx, http.MethodGet, api.PathMe, nil, "", &me)
	return me, err
}

// Drives lists known drives.
func (c *Client) Drives(ctx context.Context) ([]store.Drive, error) {
	var dr api.DrivesResponse
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := c.do(ctx, http.MethodGet, api.PathDrives, nil, "", &dr); err != nil {
		return nil, err
	}
	return dr.Drives, nil
}

// Upload streams a .replicant bundle file to the server.
func (c *Client) Upload(ctx context.Context, path string) (api.IngestResponse, error) {
	var res api.IngestResponse
	f, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer f.Close()
	err = c.do(ctx, http.MethodPost, api.PathScans, f, "application/zip", &res)
	return res, err
}
