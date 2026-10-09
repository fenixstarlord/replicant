// Package api holds the JSON types shared by the server and the CLI client.
package api

import "github.com/fenixstarlord/replicant/internal/store"

// Paths of the HTTP API.
const (
	PathScans  = "/api/scans"
	PathDrives = "/api/drives"
	PathMe     = "/api/me"
)

// IngestResponse is returned by POST /api/scans.
type IngestResponse = store.IngestResult

// DrivesResponse is returned by GET /api/drives.
type DrivesResponse struct {
	Drives []store.Drive `json:"drives"`
}

// MeResponse is returned by GET /api/me.
type MeResponse struct {
	Auth      string `json:"auth"` // "token", "session", or "open"
	TokenName string `json:"token_name,omitempty"`
	Version   string `json:"version"`
}

// ErrorResponse is the body of any error reply.
type ErrorResponse struct {
	Error string `json:"error"`
}
