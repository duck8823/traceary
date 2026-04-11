package queryservice

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// ErrSessionNotFound indicates that no session matches the filters.
var ErrSessionNotFound = xerrors.New("no matching session found")

// ErrActiveSessionNotFound indicates that no active session matches the filters.
var ErrActiveSessionNotFound = xerrors.New("no matching active session found")

// SessionQueryService provides read-side operations for sessions.
type SessionQueryService interface {
	// FindLatest returns the session_started event for the latest matching session.
	FindLatest(ctx context.Context, client types.Client, agent types.Agent, workspace types.Workspace, activeOnly bool) (*model.Event, error)
	// ListSummaries returns session summaries matching the criteria.
	ListSummaries(ctx context.Context, limit, offset int, sessionID types.SessionID, workspace types.Workspace, client types.Client, agent types.Agent, label string, from, to *time.Time) ([]*SessionSummary, error)
}

// SessionSummary holds aggregated information about a single session.
type SessionSummary struct {
	SessionID       string
	Workspace       string
	StartedAt       time.Time
	EndedAt         *time.Time
	Status          string
	TotalEvents     int
	CommandCount    int
	Agents          []string
	Label           string
	Summary         string
	ParentSessionID string
}
