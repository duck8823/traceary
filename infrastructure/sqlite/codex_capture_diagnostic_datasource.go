package sqlite

import (
	"context"
	_ "embed"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

//go:embed sql/load_codex_capture_diagnostic.sql
var loadCodexCaptureDiagnosticQuery string

var _ queryservice.CodexCaptureDiagnosticQueryService = (*CodexCaptureDiagnosticDatasource)(nil)

// CodexCaptureDiagnosticDatasource loads a complete body-free aggregate for
// one doctor window.
type CodexCaptureDiagnosticDatasource struct {
	db *Database
}

// NewCodexCaptureDiagnosticDatasource creates the read-only SQLite adapter.
func NewCodexCaptureDiagnosticDatasource(db *Database) *CodexCaptureDiagnosticDatasource {
	return &CodexCaptureDiagnosticDatasource{db: db}
}

// LoadCodexCaptureDiagnostic returns aggregate counts only. Session identities,
// event bodies, and provider payloads never leave this adapter.
func (d *CodexCaptureDiagnosticDatasource) LoadCodexCaptureDiagnostic(
	ctx context.Context,
	criteria apptypes.CodexCaptureDiagnosticCriteria,
) (apptypes.CodexCaptureDiagnosticEvidence, error) {
	if d == nil || d.db == nil {
		return apptypes.CodexCaptureDiagnosticEvidence{}, xerrors.New("Codex capture diagnostic database is not configured")
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.CodexCaptureDiagnosticEvidence{}, xerrors.Errorf("failed to open DB for Codex capture diagnostic: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.Debug("failed to close Codex capture diagnostic resource", "error", closeErr)
		}
	}()

	var evidence apptypes.CodexCaptureDiagnosticEvidence
	var sessionStart, prompt, tool, compact int
	from := formatTimestamp(criteria.From())
	to := formatTimestamp(criteria.To())
	if err := db.QueryRowContext(
		ctx,
		loadCodexCaptureDiagnosticQuery,
		criteria.Workspace().String(), from, to, from, to,
	).Scan(
		&evidence.StoredEvents,
		&sessionStart,
		&prompt,
		&tool,
		&compact,
		&evidence.StopSessions,
		&evidence.StopSessionsWithUsage,
		&evidence.UsageObservations,
		&evidence.UsageKnown,
		&evidence.UsageUnavailable,
	); err != nil {
		return apptypes.CodexCaptureDiagnosticEvidence{}, xerrors.Errorf("failed to query Codex capture diagnostic: %w", err)
	}
	evidence.SessionStartObserved = sessionStart != 0
	evidence.PromptObserved = prompt != 0
	evidence.ToolObserved = tool != 0
	evidence.CompactObserved = compact != 0
	return evidence, nil
}
