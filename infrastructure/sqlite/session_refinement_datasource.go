package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/select_session_refinement_by_session_id.sql
var selectSessionRefinementBySessionIDQuery string

//go:embed sql/upsert_session_refinement.sql
var upsertSessionRefinementQuery string

// SessionRefinementDatasource is the SQLite-backed SessionRefinementRepository.
type SessionRefinementDatasource struct {
	db *Database
}

// NewSessionRefinementDatasource creates a datasource bound to db.
func NewSessionRefinementDatasource(db *Database) *SessionRefinementDatasource {
	return &SessionRefinementDatasource{db: db}
}

var _ model.SessionRefinementRepository = (*SessionRefinementDatasource)(nil)

// FindBySessionID returns the current refinement row, or None when absent.
func (d *SessionRefinementDatasource) FindBySessionID(
	ctx context.Context,
	sessionID types.SessionID,
) (types.Optional[*model.SessionRefinement], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.SessionRefinement](), xerrors.Errorf("failed to open DB for session refinement lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	refinement, err := scanSessionRefinement(db.QueryRowContext(
		ctx,
		selectSessionRefinementBySessionIDQuery,
		sessionID.String(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return types.None[*model.SessionRefinement](), nil
	}
	if err != nil {
		return types.None[*model.SessionRefinement](), xerrors.Errorf("failed to restore session refinement: %w", err)
	}
	return types.Some(refinement), nil
}

// SaveIfAdvances stores refinement only when the persisted row is still at
// expectedGeneration (0 means "no row yet") and the incoming range subsumes the
// stored one under canonical event order: covers_to strictly advances, and
// covers_from does not move forward. Coverage authorises an irreversible body
// discard, so it may widen but never shrink.
func (d *SessionRefinementDatasource) SaveIfAdvances(
	ctx context.Context,
	refinement *model.SessionRefinement,
	expectedGeneration int,
) (bool, error) {
	if refinement == nil {
		return false, xerrors.Errorf("session refinement must not be nil: %w", model.ErrInvalidSessionRefinement)
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return false, xerrors.Errorf("failed to open DB for session refinement save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	// Placeholder order matches upsert_session_refinement.sql:
	// SELECT row values (10), source predicate expectedGeneration + session_id,
	// then ON CONFLICT UPDATE WHERE expectedGeneration again.
	sessionID := refinement.SessionID().String()
	result, err := db.ExecContext(
		ctx,
		upsertSessionRefinementQuery,
		sessionID,
		refinement.Generation(),
		refinement.CoversFromEventID().String(),
		refinement.CoversToEventID().String(),
		refinement.Summary(),
		refinement.Keywords(),
		refinement.ProducedBy(),
		formatTimestamp(refinement.ProducedAt()),
		boolToInt(refinement.Degraded()),
		boolToInt(refinement.HasAgentReasoning()),
		expectedGeneration,
		sessionID,
		expectedGeneration,
	)
	if err != nil {
		return false, xerrors.Errorf("failed to save session refinement: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, xerrors.Errorf("failed to read session refinement save result: %w", err)
	}
	return rows == 1, nil
}

func scanSessionRefinement(row *sql.Row) (*model.SessionRefinement, error) {
	var (
		sessionID         string
		generation        int
		coversFrom        string
		coversTo          string
		summary           string
		keywords          string
		producedBy        string
		producedAtRaw     string
		degraded          int
		hasAgentReasoning int
	)
	if err := row.Scan(
		&sessionID,
		&generation,
		&coversFrom,
		&coversTo,
		&summary,
		&keywords,
		&producedBy,
		&producedAtRaw,
		&degraded,
		&hasAgentReasoning,
	); err != nil {
		return nil, xerrors.Errorf("failed to scan session refinement row: %w", err)
	}
	producedAt, err := time.Parse(time.RFC3339Nano, producedAtRaw)
	if err != nil {
		return nil, xerrors.Errorf("invalid session refinement produced_at %q: %w", producedAtRaw, err)
	}
	refinement, err := model.SessionRefinementOf(
		types.SessionID(sessionID),
		generation,
		types.EventID(coversFrom),
		types.EventID(coversTo),
		summary,
		keywords,
		producedBy,
		producedAt,
		degraded == 1,
		hasAgentReasoning == 1,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore session refinement: %w", err)
	}
	return refinement, nil
}
