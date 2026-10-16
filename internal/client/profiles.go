package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fenixstarlord/replicant/internal/api"
	"github.com/fenixstarlord/replicant/internal/scanprofile"
	"github.com/fenixstarlord/replicant/internal/store"
)

func (c *Client) Profiles(ctx context.Context) (api.ProfilesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out api.ProfilesResponse
	err := c.do(ctx, http.MethodGet, api.PathProfiles, nil, "", &out)
	return out, err
}

func (c *Client) DriveProfile(ctx context.Context, key string) (scanprofile.Profile, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out scanprofile.Profile
	err := c.do(ctx, http.MethodGet, api.PathProfiles+"?key="+url.QueryEscape(key), nil, "", &out)
	return out, err
}

func (c *Client) SaveDriveProfile(ctx context.Context, p store.ProfileUpdate) (scanprofile.Profile, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out scanprofile.Profile
	err := c.do(ctx, http.MethodPut, api.PathProfiles, strings.NewReader(mustJSON(p)), "application/json", &out)
	return out, err
}
