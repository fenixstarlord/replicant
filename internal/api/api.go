// Package api holds the JSON types shared by the server and the CLI client.
package api

import "github.com/fenixstarlord/replicant/internal/store"

// Paths of the HTTP API.
const (
	PathScans    = "/api/scans"
	PathDrives   = "/api/drives"
	PathMe       = "/api/me"
	PathActivity = "/api/activity"
)

// ActivityStart is the body of POST /api/activity: a client scan has begun.
type ActivityStart struct {
	Host       string `json:"host"`
	DriveName  string `json:"drive_name"`
	VolumeUUID string `json:"volume_uuid,omitempty"`
	Root       string `json:"root"`
}

// ActivityUpdate is the body of PUT /api/activity/{id}. Progress fields
// update the stage; Status closes it ("error" or "cancelled").
type ActivityUpdate struct {
	Stage      string `json:"stage,omitempty"`
	Done       int    `json:"done,omitempty"`
	Total      int    `json:"total,omitempty"`
	DriveName  string `json:"drive_name,omitempty"`
	VolumeUUID string `json:"volume_uuid,omitempty"`
	Status     string `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ActivityResponse is returned by POST /api/activity.
type ActivityResponse struct {
	ID int64 `json:"id"`
}

// ActivityQuery is the query parameter POST /api/scans takes to close the
// activity that produced the bundle.
const ActivityQuery = "activity"

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
