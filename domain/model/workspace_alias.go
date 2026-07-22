package model

import (
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// WorkspaceAlias is an explicit operator-reviewed relationship between one
// session and an alternate workspace identity. It never mutates either value.
type WorkspaceAlias struct {
	sessionID  types.SessionID
	workspace  types.Workspace
	reviewedAt time.Time
	reviewedBy string
	note       string
}

// NewWorkspaceAlias validates and constructs a reviewed workspace alias.
func NewWorkspaceAlias(sessionID types.SessionID, workspace types.Workspace, reviewedAt time.Time, reviewedBy, note string) (*WorkspaceAlias, error) {
	resolvedSessionID, err := types.SessionIDFrom(sessionID.String())
	if err != nil {
		return nil, xerrors.Errorf("invalid workspace alias session ID: %w", err)
	}
	resolvedWorkspace := types.Workspace(strings.TrimSpace(workspace.String()))
	if resolvedWorkspace.String() == "" {
		return nil, xerrors.Errorf("workspace alias must not be empty")
	}
	resolvedReviewer := strings.TrimSpace(reviewedBy)
	if resolvedReviewer == "" {
		return nil, xerrors.Errorf("workspace alias reviewer must not be empty")
	}
	if reviewedAt.IsZero() {
		return nil, xerrors.Errorf("workspace alias review time must not be zero")
	}
	return &WorkspaceAlias{
		sessionID:  resolvedSessionID,
		workspace:  resolvedWorkspace,
		reviewedAt: reviewedAt.UTC(),
		reviewedBy: resolvedReviewer,
		note:       strings.TrimSpace(note),
	}, nil
}

// SessionID returns the reviewed alias's owning session.
func (a *WorkspaceAlias) SessionID() types.SessionID { return a.sessionID }

// Workspace returns the reviewed alternate workspace identity.
func (a *WorkspaceAlias) Workspace() types.Workspace { return a.workspace }

// ReviewedAt returns when the operator completed the review.
func (a *WorkspaceAlias) ReviewedAt() time.Time { return a.reviewedAt }

// ReviewedBy returns the operator identity recorded with the review.
func (a *WorkspaceAlias) ReviewedBy() string { return a.reviewedBy }

// Note returns the optional review rationale.
func (a *WorkspaceAlias) Note() string { return a.note }
