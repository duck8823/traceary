package types

import (
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// CodexCaptureDiagnosticCriteria scopes one complete, body-free diagnostic
// projection to a canonical workspace and half-open event window.
type CodexCaptureDiagnosticCriteria struct {
	workspace types.Workspace
	from      time.Time
	to        time.Time
}

// CodexCaptureDiagnosticCriteriaOf creates validated diagnostic criteria.
func CodexCaptureDiagnosticCriteriaOf(
	workspace types.Workspace,
	from, to time.Time,
) (CodexCaptureDiagnosticCriteria, error) {
	if workspace == "" {
		return CodexCaptureDiagnosticCriteria{}, xerrors.New("Codex capture diagnostic workspace must not be empty")
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return CodexCaptureDiagnosticCriteria{}, xerrors.New("Codex capture diagnostic requires a valid half-open interval")
	}
	return CodexCaptureDiagnosticCriteria{
		workspace: workspace,
		from:      from.UTC(),
		to:        to.UTC(),
	}, nil
}

// Workspace returns the canonical workspace filter.
func (c CodexCaptureDiagnosticCriteria) Workspace() types.Workspace { return c.workspace }

// From returns the inclusive lower event bound.
func (c CodexCaptureDiagnosticCriteria) From() time.Time { return c.from }

// To returns the exclusive upper event bound.
func (c CodexCaptureDiagnosticCriteria) To() time.Time { return c.to }

// CodexCaptureDiagnosticEvidence is the complete body-free projection consumed
// by doctor. Session identities and event bodies never cross this boundary.
type CodexCaptureDiagnosticEvidence struct {
	StoredEvents          int
	SessionStartObserved  bool
	PromptObserved        bool
	ToolObserved          bool
	CompactObserved       bool
	StopSessions          int
	StopSessionsWithUsage int
	UsageObservations     int
	UsageKnown            int
	UsageUnavailable      int
}
