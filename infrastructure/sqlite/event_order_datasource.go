package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/select_earliest_event_id_for_session.sql
var selectEarliestEventIDForSessionQuery string

//go:embed sql/select_event_session_id.sql
var selectEventSessionIDQuery string

//go:embed sql/select_event_is_strictly_after.sql
var selectEventIsStrictlyAfterQuery string

//go:embed sql/select_session_body_bytes.sql
var selectSessionBodyBytesQuery string

//go:embed sql/select_session_body_bytes_after.sql
var selectSessionBodyBytesAfterQuery string

var (
	_ model.SessionEventOrderRepository            = (*EventDatasource)(nil)
	_ model.SessionConsolidationPressureRepository = (*EventDatasource)(nil)
)

// EarliestEventID returns the canonically earliest event id in the session.
func (d *EventDatasource) EarliestEventID(
	ctx context.Context,
	sessionID types.SessionID,
) (types.Optional[types.EventID], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[types.EventID](), xerrors.Errorf("failed to open DB for earliest event lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var id string
	err = db.QueryRowContext(ctx, selectEarliestEventIDForSessionQuery, sessionID.String()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return types.None[types.EventID](), nil
	}
	if err != nil {
		return types.None[types.EventID](), xerrors.Errorf("failed to resolve earliest event: %w", err)
	}
	eventID, err := types.EventIDFrom(id)
	if err != nil {
		return types.None[types.EventID](), xerrors.Errorf("invalid stored earliest event id: %w", err)
	}
	return types.Some(eventID), nil
}

// FindEventSessionID returns the session that owns the event, or None when missing.
func (d *EventDatasource) FindEventSessionID(
	ctx context.Context,
	eventID types.EventID,
) (types.Optional[types.SessionID], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[types.SessionID](), xerrors.Errorf("failed to open DB for event session lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var sessionID string
	err = db.QueryRowContext(ctx, selectEventSessionIDQuery, eventID.String()).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return types.None[types.SessionID](), nil
	}
	if err != nil {
		return types.None[types.SessionID](), xerrors.Errorf("failed to look up event session: %w", err)
	}
	parsed, err := types.SessionIDFrom(sessionID)
	if err != nil {
		return types.None[types.SessionID](), xerrors.Errorf("invalid stored event session id: %w", err)
	}
	return types.Some(parsed), nil
}

// EventIsStrictlyAfter reports whether left is strictly after right.
func (d *EventDatasource) EventIsStrictlyAfter(
	ctx context.Context,
	left, right types.EventID,
) (bool, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return false, xerrors.Errorf("failed to open DB for event order comparison: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var after int
	// Bind order matches the SQL: right_event.id first, left_event.id second.
	err = db.QueryRowContext(ctx, selectEventIsStrictlyAfterQuery, right.String(), left.String()).Scan(&after)
	if errors.Is(err, sql.ErrNoRows) {
		return false, xerrors.Errorf("cannot compare missing events %s and %s: %w", left, right, model.ErrInvalidSessionRefinement)
	}
	if err != nil {
		return false, xerrors.Errorf("failed to compare event order: %w", err)
	}
	return after == 1, nil
}

// SumBodyBytesAfter returns the unrefined body-byte pressure for a session.
// When coversTo is present, only events strictly after that boundary under
// (ts_norm(created_at), id) contribute; otherwise the whole session is summed.
func (d *EventDatasource) SumBodyBytesAfter(
	ctx context.Context,
	sessionID types.SessionID,
	coversTo types.Optional[types.EventID],
) (int64, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return 0, xerrors.Errorf("failed to open DB for consolidation pressure: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var total int64
	if boundary, ok := coversTo.Value(); ok {
		// Bind order matches the SQL: boundary id first, session id second.
		err = db.QueryRowContext(
			ctx,
			selectSessionBodyBytesAfterQuery,
			boundary.String(),
			sessionID.String(),
		).Scan(&total)
	} else {
		err = db.QueryRowContext(ctx, selectSessionBodyBytesQuery, sessionID.String()).Scan(&total)
	}
	if err != nil {
		return 0, xerrors.Errorf("failed to sum session body bytes: %w", err)
	}
	return total, nil
}
