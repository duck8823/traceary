package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/insert_consolidation_request.sql
var insertConsolidationRequestQuery string

//go:embed sql/select_latest_open_consolidation_request.sql
var selectLatestOpenConsolidationRequestQuery string

//go:embed sql/select_latest_consolidation_request.sql
var selectLatestConsolidationRequestQuery string

//go:embed sql/update_consolidation_request_refine_outcome.sql
var updateConsolidationRequestRefineOutcomeQuery string

//go:embed sql/select_consolidation_conversion.sql
var selectConsolidationConversionQuery string

//go:embed sql/select_consolidation_refinement_authorship.sql
var selectConsolidationRefinementAuthorshipQuery string

// ConsolidationRequestDatasource persists the fold-request ledger.
type ConsolidationRequestDatasource struct {
	db *Database
}

// NewConsolidationRequestDatasource binds the ledger to db.
func NewConsolidationRequestDatasource(db *Database) *ConsolidationRequestDatasource {
	return &ConsolidationRequestDatasource{db: db}
}

var (
	_ model.ConsolidationRequestRepository             = (*ConsolidationRequestDatasource)(nil)
	_ queryservice.ConsolidationConversionQueryService = (*ConsolidationRequestDatasource)(nil)
)

// Save inserts a request. Returns (false, nil) when the unique key already exists.
func (d *ConsolidationRequestDatasource) Save(ctx context.Context, request *model.ConsolidationRequest) (bool, error) {
	if request == nil {
		return false, xerrors.Errorf("consolidation request must not be nil: %w", model.ErrInvalidConsolidationRequest)
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return false, xerrors.Errorf("failed to open DB for consolidation request save: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	result, err := db.ExecContext(
		ctx,
		insertConsolidationRequestQuery,
		request.SessionID().String(),
		request.Client(),
		formatMemoryValidityTimestamp(request.RequestedAt()),
		request.AtEventID().String(),
		request.Signal(),
		request.PressureValue(),
		request.ThresholdValue(),
		boolToInt(request.ReRequest()),
		request.Delivery().String(),
	)
	if err != nil {
		return false, xerrors.Errorf("failed to insert consolidation request: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, xerrors.Errorf("failed to read consolidation request insert rows: %w", err)
	}
	return n > 0, nil
}

// FindLatestOpen returns the newest unstamped request for the session.
func (d *ConsolidationRequestDatasource) FindLatestOpen(
	ctx context.Context,
	sessionID types.SessionID,
) (types.Optional[*model.ConsolidationRequest], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("failed to open DB for consolidation request lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var (
		storedSessionID string
		client          string
		requestedAt     string
		atEventID       string
		signal          string
		pressure        int64
		threshold       int64
		reRequest       int
		delivery        string
	)
	err = db.QueryRowContext(ctx, selectLatestOpenConsolidationRequestQuery, sessionID.String()).Scan(
		&storedSessionID, &client, &requestedAt, &atEventID, &signal, &pressure, &threshold, &reRequest, &delivery,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return types.None[*model.ConsolidationRequest](), nil
	}
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("failed to load open consolidation request: %w", err)
	}
	parsedAt, err := time.Parse(time.RFC3339Nano, requestedAt)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid consolidation requested_at: %w", err)
	}
	sid, err := types.SessionIDFrom(storedSessionID)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation session id: %w", err)
	}
	eid, err := types.EventIDFrom(atEventID)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation at_event_id: %w", err)
	}
	del, err := types.ConsolidationDeliveryFrom(delivery)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation delivery: %w", err)
	}
	request, err := model.NewConsolidationRequest(sid, client, parsedAt, eid, signal, pressure, threshold, reRequest == 1, del)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation request: %w", err)
	}
	return types.Some(request), nil
}

// FindLatest returns the newest request for the session regardless of outcome.
func (d *ConsolidationRequestDatasource) FindLatest(
	ctx context.Context,
	sessionID types.SessionID,
) (types.Optional[*model.ConsolidationRequest], error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("failed to open DB for consolidation request lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var (
		storedSessionID string
		client          string
		requestedAt     string
		atEventID       string
		signal          string
		pressure        int64
		threshold       int64
		reRequest       int
		delivery        string
	)
	err = db.QueryRowContext(ctx, selectLatestConsolidationRequestQuery, sessionID.String()).Scan(
		&storedSessionID, &client, &requestedAt, &atEventID, &signal, &pressure, &threshold, &reRequest, &delivery,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return types.None[*model.ConsolidationRequest](), nil
	}
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("failed to load latest consolidation request: %w", err)
	}
	parsedAt, err := time.Parse(time.RFC3339Nano, requestedAt)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid consolidation requested_at: %w", err)
	}
	sid, err := types.SessionIDFrom(storedSessionID)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation session id: %w", err)
	}
	eid, err := types.EventIDFrom(atEventID)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation at_event_id: %w", err)
	}
	del, err := types.ConsolidationDeliveryFrom(delivery)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation delivery: %w", err)
	}
	request, err := model.NewConsolidationRequest(sid, client, parsedAt, eid, signal, pressure, threshold, reRequest == 1, del)
	if err != nil {
		return types.None[*model.ConsolidationRequest](), xerrors.Errorf("invalid stored consolidation request: %w", err)
	}
	return types.Some(request), nil
}

// MarkRefineOutcome stamps the newest open request for the session.
func (d *ConsolidationRequestDatasource) MarkRefineOutcome(ctx context.Context, stamp model.ConsolidationRefineStamp) (bool, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return false, xerrors.Errorf("failed to open DB for consolidation request stamp: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var generation any
	if value, ok := stamp.Generation.Value(); ok {
		generation = value
	}
	result, err := db.ExecContext(
		ctx,
		updateConsolidationRequestRefineOutcomeQuery,
		stamp.Outcome.String(),
		stamp.Reason,
		stamp.ProducedBy,
		formatMemoryValidityTimestamp(stamp.At),
		generation,
		stamp.SessionID.String(),
	)
	if err != nil {
		return false, xerrors.Errorf("failed to stamp consolidation request: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, xerrors.Errorf("failed to read consolidation request stamp rows: %w", err)
	}
	return n > 0, nil
}

// ConversionSince returns per-client conversion over the window.
func (d *ConsolidationRequestDatasource) ConversionSince(ctx context.Context, since time.Time) ([]queryservice.ConsolidationConversionRow, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for consolidation conversion: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	rows, err := db.QueryContext(ctx, selectConsolidationConversionQuery, formatMemoryValidityTimestamp(since))
	if err != nil {
		return nil, xerrors.Errorf("failed to query consolidation conversion: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var out []queryservice.ConsolidationConversionRow
	for rows.Next() {
		var row queryservice.ConsolidationConversionRow
		if err := rows.Scan(&row.Client, &row.Requests, &row.SessionsRequested, &row.RequestsAccepted, &row.SessionsRefined); err != nil {
			return nil, xerrors.Errorf("failed to scan consolidation conversion: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("consolidation conversion rows: %w", err)
	}
	return out, nil
}

// RefinementAuthorshipSince returns produced_by buckets for asked sessions.
func (d *ConsolidationRequestDatasource) RefinementAuthorshipSince(ctx context.Context, since time.Time) ([]queryservice.RefinementAuthorshipRow, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for consolidation authorship: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	rows, err := db.QueryContext(ctx, selectConsolidationRefinementAuthorshipQuery, formatMemoryValidityTimestamp(since))
	if err != nil {
		return nil, xerrors.Errorf("failed to query consolidation authorship: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var out []queryservice.RefinementAuthorshipRow
	for rows.Next() {
		var row queryservice.RefinementAuthorshipRow
		if err := rows.Scan(&row.Client, &row.ProducedBy, &row.Sessions); err != nil {
			return nil, xerrors.Errorf("failed to scan consolidation authorship: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("consolidation authorship rows: %w", err)
	}
	return out, nil
}
