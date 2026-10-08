// Package cliconfig reads and writes the shelf CLI's config file at
// ~/.config/shelf/config.toml.
package cliconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the on-disk configuration.
type Config struct {
	Server string `toml:"server"` // e.g. http://shelf.netbird.cloud:8080
	Token  string `toml:"token"`  // API token from the server
	Tools  Tools  `toml:"tools"`
}

// Tools holds explicit paths to external metadata tools (Phase 4).
type Tools struct {
	FFprobe    string   `toml:"ffprobe"`
	ArtCmd     string   `toml:"art_cmd"`
	ArtCmdArgs []string `toml:"art_cmd_args"` // {input} and {outdir} are substituted
	REDline    string   `toml:"redline"`
}

// Path returns the config file path, honouring SHELF_CONFIG.
func Path() (string, error) {
	if p := os.Getenv("SHELF_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	// macOS maps UserConfigDir to ~/Library/Application Support; use the
	// XDG-style path the plan specifies instead.
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "shelf", "config.toml"), nil
}

// Load reads the config. A missing file yields an empty config, not an error.
func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	var c Config
	if _, err := toml.DecodeFile(p, &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("%s: %w", p, err)
	}
	return c, nil
}

// Save writes the config with owner-only permissions.
func Save(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
