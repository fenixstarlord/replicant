package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fenixstarlord/replicant/internal/api"
	"github.com/fenixstarlord/replicant/internal/cliconfig"
	"github.com/fenixstarlord/replicant/internal/client"
	"github.com/fenixstarlord/replicant/internal/scanprofile"
	"github.com/fenixstarlord/replicant/internal/store"
	"github.com/spf13/cobra"
)

func newProfilesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profiles", Short: "List shared drive profiles as JSON", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := loadClient()
			if err != nil {
				return err
			}
			out, err := c.Profiles(cmd.Context())
			if err != nil {
				return err
			}
			cacheProfiles(c.Server, out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		}}
	cmd.AddCommand(&cobra.Command{Use: "save", Short: "Save a shared drive profile from JSON on stdin", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var u store.ProfileUpdate
			if err := json.NewDecoder(io.LimitReader(cmd.InOrStdin(), 64<<10)).Decode(&u); err != nil {
				return err
			}
			if err := u.Validate(); err != nil {
				return err
			}
			c, err := loadClient()
			if err != nil {
				return err
			}
			out, err := c.SaveDriveProfile(cmd.Context(), u)
			if err != nil {
				return err
			}
			cacheProfile(c.Server, out)
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		}})
	return cmd
}

func profileCachePath(server string) string {
	p, err := cliconfig.Path()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(p), fmt.Sprintf("profiles-%x.json", sha256.Sum256([]byte(server))))
}

func cachedProfiles(server string) api.ProfilesResponse {
	var out api.ProfilesResponse
	data, err := os.ReadFile(profileCachePath(server))
	if err == nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func cacheProfiles(server string, p api.ProfilesResponse) {
	path := profileCachePath(server)
	if path == "" {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	// Local cache only, never under the scanned mount. Atomic replacement.
	f, err := os.CreateTemp(filepath.Dir(path), "profiles-*.tmp")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	cerr := f.Close()
	if err == nil && cerr == nil {
		_ = os.Rename(f.Name(), path)
	}
}

func cacheProfile(server string, p scanprofile.Profile) {
	all := cachedProfiles(server)
	for i := range all.Profiles {
		if all.Profiles[i].Key == p.Key {
			all.Profiles[i] = p
			cacheProfiles(server, all)
			return
		}
	}
	all.Profiles = append(all.Profiles, p)
	cacheProfiles(server, all)
}

func resolveProfile(ctx context.Context, c *client.Client, key string, stderr io.Writer) (scanprofile.Profile, error) {
	p, err := c.DriveProfile(ctx, key)
	if err == nil {
		cacheProfile(c.Server, p)
		return p, nil
	}
	if ctx.Err() != nil {
		return p, ctx.Err()
	}
	for _, cached := range cachedProfiles(c.Server).Profiles {
		if cached.Key == key && (cached.Configured || cached.Indexed) {
			fmt.Fprintf(stderr, "note: server profile unavailable; using cached settings for %s\n", key)
			return cached, nil
		}
	}
	return p, fmt.Errorf("load shared drive settings (update replicant-server if this endpoint is missing): %w", err)
}
