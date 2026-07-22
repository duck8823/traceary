package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const selectUsageObservation = `
SELECT observation_id, session_id, host, source_name, source_version, provider, model,
       scope, accounting, status, observed_at, finalized_at, terminal_code,
       input_state, input_tokens, cached_input_state, cached_input_tokens,
       cache_write_input_state, cache_write_input_tokens, output_state, output_tokens,
       reasoning_output_state, reasoning_output_tokens, total_state, total_tokens,
       cost_state, cost_amount_micros, cost_currency, cost_origin, price_table_version,
       snapshot_series, snapshot_revision, supersedes_id
  FROM usage_observations
 WHERE observation_id = ?`

// UsageObservationDatasource persists usage observations with serialized
// write transitions and immutable snapshot chains.
type UsageObservationDatasource struct {
	db *Database
}

// NewUsageObservationDatasource creates a usage observation datasource.
func NewUsageObservationDatasource(db *Database) *UsageObservationDatasource {
	return &UsageObservationDatasource{db: db}
}

var _ model.UsageObservationRepository = (*UsageObservationDatasource)(nil)

// Record inserts or idempotently finalizes one authoritative observation.
func (d *UsageObservationDatasource) Record(
	ctx context.Context,
	observation *model.UsageObservation,
) (transition model.UsageObservationTransition, err error) {
	if observation == nil {
		return "", model.ErrInvalidUsageObservation
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return "", xerrors.Errorf("failed to open DB for usage observation record: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close DB after usage observation record: %w", closeErr)
		}
	}()

	conn, err := db.Conn(ctx)
	if err != nil {
		return "", xerrors.Errorf("failed to acquire usage observation connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil && err == nil {
			err = xerrors.Errorf("failed to close usage observation connection: %w", closeErr)
		}
	}()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return "", xerrors.Errorf("failed to begin usage observation transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if _, rollbackErr := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK"); rollbackErr != nil {
			slog.Debug("failed to roll back usage observation transaction", "error", rollbackErr)
		}
	}()

	current, err := findUsageObservation(ctx, conn, observation.Descriptor().ObservationID())
	if err != nil {
		return "", xerrors.Errorf("failed to inspect existing usage observation: %w", err)
	}
	if existing, present := current.Value(); present {
		transition, err = existing.Reconcile(observation)
		if err != nil {
			return "", xerrors.Errorf("failed to reconcile usage observation: %w", err)
		}
		if transition == model.UsageObservationTransitionApplied {
			if err := updateFinalizedUsageObservation(ctx, conn, existing); err != nil {
				return "", xerrors.Errorf("failed to apply usage finalization: %w", err)
			}
		}
	} else {
		if observation.Descriptor().Scope() == types.UsageScopeSessionSnapshot {
			head, err := findUsageSnapshotHead(ctx, conn, observation.Descriptor().SnapshotSeries())
			if err != nil {
				return "", xerrors.Errorf("failed to inspect usage snapshot series: %w", err)
			}
			if err := observation.ValidateSnapshotSuccessor(head); err != nil {
				return "", xerrors.Errorf("failed to validate usage snapshot successor: %w", err)
			}
		}
		if err := insertUsageObservation(ctx, conn, observation); err != nil {
			return "", xerrors.Errorf("failed to record new usage observation: %w", err)
		}
		transition = model.UsageObservationTransitionApplied
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return "", xerrors.Errorf("failed to commit usage observation transaction: %w", err)
	}
	committed = true
	return transition, nil
}

// FindByID restores an observation by authoritative identity.
func (d *UsageObservationDatasource) FindByID(
	ctx context.Context,
	observationID types.UsageObservationID,
) (types.Optional[*model.UsageObservation], error) {
	if _, err := types.UsageObservationIDFrom(observationID.String()); err != nil {
		return types.None[*model.UsageObservation](), xerrors.Errorf("invalid usage observation lookup identity: %w", err)
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return types.None[*model.UsageObservation](), xerrors.Errorf("failed to open DB for usage observation lookup: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()
	observation, err := findUsageObservation(ctx, db, observationID)
	if err != nil {
		return types.None[*model.UsageObservation](), xerrors.Errorf("failed to find usage observation: %w", err)
	}
	return observation, nil
}

type usageRowScanner interface {
	Scan(dest ...any) error
}

type usageQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type usageExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func findUsageObservation(
	ctx context.Context,
	queryer usageQueryer,
	observationID types.UsageObservationID,
) (types.Optional[*model.UsageObservation], error) {
	observation, err := scanUsageObservation(queryer.QueryRowContext(ctx, selectUsageObservation, observationID.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return types.None[*model.UsageObservation](), nil
	}
	if err != nil {
		return types.None[*model.UsageObservation](), xerrors.Errorf("failed to restore usage observation: %w", err)
	}
	return types.Some(observation), nil
}

func findUsageSnapshotHead(ctx context.Context, queryer usageQueryer, series string) (*model.UsageObservation, error) {
	const query = `
SELECT COUNT(*), COALESCE(MAX(candidate.observation_id), '')
  FROM usage_observations AS candidate
 WHERE candidate.snapshot_series = ?
   AND NOT EXISTS (
       SELECT 1 FROM usage_observations AS successor
        WHERE successor.supersedes_id = candidate.observation_id
   )`
	var count int
	var observationID string
	if err := queryer.QueryRowContext(ctx, query, series).Scan(&count, &observationID); err != nil {
		return nil, xerrors.Errorf("failed to inspect usage snapshot head: %w", err)
	}
	if count == 0 {
		return nil, nil
	}
	if count != 1 {
		return nil, xerrors.Errorf("usage snapshot series %q has %d heads: %w", series, count, model.ErrConflictingUsageObservation)
	}
	id, err := types.UsageObservationIDFrom(observationID)
	if err != nil {
		return nil, xerrors.Errorf("invalid usage snapshot head identity: %w", err)
	}
	head, err := findUsageObservation(ctx, queryer, id)
	if err != nil {
		return nil, xerrors.Errorf("failed to load usage snapshot head: %w", err)
	}
	value, present := head.Value()
	if !present {
		return nil, xerrors.Errorf("usage snapshot head disappeared: %w", model.ErrConflictingUsageObservation)
	}
	return value, nil
}

func scanUsageObservation(row usageRowScanner) (*model.UsageObservation, error) {
	var (
		observationID, sessionID, host, sourceName, sourceVersion string
		provider, modelName, finalizedAt, terminalCode            sql.NullString
		scope, accounting, status, observedAt                     string
		inputState, cachedInputState, cacheWriteInputState        string
		outputState, reasoningOutputState, totalState             string
		inputTokens, cachedInputTokens, cacheWriteInputTokens     sql.NullInt64
		outputTokens, reasoningOutputTokens, totalTokens          sql.NullInt64
		costState                                                 string
		costAmount                                                sql.NullInt64
		costCurrency, costOrigin, priceTableVersion               sql.NullString
		snapshotSeries, supersedesID                              sql.NullString
		snapshotRevision                                          sql.NullInt64
	)
	if err := row.Scan(
		&observationID, &sessionID, &host, &sourceName, &sourceVersion, &provider, &modelName,
		&scope, &accounting, &status, &observedAt, &finalizedAt, &terminalCode,
		&inputState, &inputTokens, &cachedInputState, &cachedInputTokens,
		&cacheWriteInputState, &cacheWriteInputTokens, &outputState, &outputTokens,
		&reasoningOutputState, &reasoningOutputTokens, &totalState, &totalTokens,
		&costState, &costAmount, &costCurrency, &costOrigin, &priceTableVersion,
		&snapshotSeries, &snapshotRevision, &supersedesID,
	); err != nil {
		return nil, xerrors.Errorf("failed to scan usage observation row: %w", err)
	}

	id, err := types.UsageObservationIDFrom(observationID)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage observation ID: %w", err)
	}
	sid, err := types.SessionIDFrom(sessionID)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage session ID: %w", err)
	}
	source, err := types.UsageSourceOf(host, sourceName, sourceVersion, provider.String, modelName.String)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage source: %w", err)
	}
	observedTime, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return nil, xerrors.Errorf("invalid usage observed_at: %w", err)
	}
	resolvedScope, err := types.UsageScopeFrom(scope)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage scope: %w", err)
	}
	resolvedAccounting, err := types.UsageAccountingFrom(accounting)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage accounting: %w", err)
	}

	var descriptor model.UsageObservationDescriptor
	if resolvedScope == types.UsageScopeSessionSnapshot {
		predecessor := types.None[types.UsageObservationID]()
		if supersedesID.Valid {
			value, err := types.UsageObservationIDFrom(supersedesID.String)
			if err != nil {
				return nil, xerrors.Errorf("failed to restore superseded usage observation ID: %w", err)
			}
			predecessor = types.Some(value)
		}
		if !snapshotRevision.Valid {
			return nil, xerrors.Errorf("usage snapshot revision is missing")
		}
		descriptor, err = model.NewUsageSnapshotDescriptor(
			id, sid, source, snapshotSeries.String, snapshotRevision.Int64, predecessor, observedTime,
		)
	} else {
		descriptor, err = model.NewUsageObservationDescriptor(id, sid, source, resolvedScope, resolvedAccounting, observedTime)
	}
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage observation descriptor: %w", err)
	}

	counters, err := restoreUsageCounters(
		inputState, inputTokens, cachedInputState, cachedInputTokens,
		cacheWriteInputState, cacheWriteInputTokens, outputState, outputTokens,
		reasoningOutputState, reasoningOutputTokens, totalState, totalTokens,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage counters: %w", err)
	}
	cost, err := types.UsageCostFrom(
		costState, optionalInt64(costAmount), costCurrency.String, costOrigin.String, priceTableVersion.String,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage cost: %w", err)
	}
	resolvedStatus, err := types.UsageObservationStatusFrom(status)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage observation status: %w", err)
	}
	resolvedTerminal := types.None[types.UsageTerminalCode]()
	if terminalCode.Valid {
		code, err := types.UsageTerminalCodeFrom(terminalCode.String)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore usage terminal code: %w", err)
		}
		resolvedTerminal = types.Some(code)
	}
	resolvedFinalizedAt := types.None[time.Time]()
	if finalizedAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, finalizedAt.String)
		if err != nil {
			return nil, xerrors.Errorf("invalid usage finalized_at: %w", err)
		}
		resolvedFinalizedAt = types.Some(value)
	}
	observation, err := model.UsageObservationOf(descriptor, resolvedStatus, counters, cost, resolvedTerminal, resolvedFinalizedAt)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore usage observation aggregate: %w", err)
	}
	return observation, nil
}

func restoreUsageCounters(values ...any) (types.UsageCounters, error) {
	if len(values) != 12 {
		return types.UsageCounters{}, xerrors.Errorf("usage counter row requires 12 values")
	}
	restored := make([]types.UsageValue, 0, 6)
	for index := 0; index < len(values); index += 2 {
		state, stateOK := values[index].(string)
		numeric, numericOK := values[index+1].(sql.NullInt64)
		if !stateOK || !numericOK {
			return types.UsageCounters{}, xerrors.Errorf("invalid usage counter row types")
		}
		value, err := types.UsageValueFrom(state, optionalInt64(numeric))
		if err != nil {
			return types.UsageCounters{}, xerrors.Errorf("failed to restore usage counter %d: %w", index/2, err)
		}
		restored = append(restored, value)
	}
	counters, err := types.UsageCountersOf(restored[0], restored[1], restored[2], restored[3], restored[4], restored[5])
	if err != nil {
		return types.UsageCounters{}, xerrors.Errorf("failed to validate restored usage counters: %w", err)
	}
	return counters, nil
}

func optionalInt64(value sql.NullInt64) types.Optional[int64] {
	if value.Valid {
		return types.Some(value.Int64)
	}
	return types.None[int64]()
}

func insertUsageObservation(ctx context.Context, exec usageExecer, observation *model.UsageObservation) error {
	const query = `
INSERT INTO usage_observations (
    observation_id, session_id, host, source_name, source_version, provider, model,
    scope, accounting, status, observed_at, finalized_at, terminal_code,
    input_state, input_tokens, cached_input_state, cached_input_tokens,
    cache_write_input_state, cache_write_input_tokens, output_state, output_tokens,
    reasoning_output_state, reasoning_output_tokens, total_state, total_tokens,
    cost_state, cost_amount_micros, cost_currency, cost_origin, price_table_version,
    snapshot_series, snapshot_revision, supersedes_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := usageObservationArgs(observation)
	if _, err := exec.ExecContext(ctx, query, args...); err != nil {
		return xerrors.Errorf("failed to insert usage observation: %w", err)
	}
	return nil
}

func updateFinalizedUsageObservation(ctx context.Context, exec usageExecer, observation *model.UsageObservation) error {
	const query = `
UPDATE usage_observations
   SET status = ?, finalized_at = ?, terminal_code = ?,
       input_state = ?, input_tokens = ?, cached_input_state = ?, cached_input_tokens = ?,
       cache_write_input_state = ?, cache_write_input_tokens = ?, output_state = ?, output_tokens = ?,
       reasoning_output_state = ?, reasoning_output_tokens = ?, total_state = ?, total_tokens = ?,
       cost_state = ?, cost_amount_micros = ?, cost_currency = ?, cost_origin = ?, price_table_version = ?
 WHERE observation_id = ? AND status = 'pending'`
	args := usageFinalizationArgs(observation)
	args = append(args, observation.Descriptor().ObservationID().String())
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return xerrors.Errorf("failed to finalize usage observation: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return xerrors.Errorf("failed to inspect usage finalization: %w", err)
	}
	if updated != 1 {
		return xerrors.Errorf("usage observation finalization updated %d rows: %w", updated, model.ErrConflictingUsageObservation)
	}
	return nil
}

func usageObservationArgs(observation *model.UsageObservation) []any {
	descriptor := observation.Descriptor()
	source := descriptor.Source()
	args := []any{
		descriptor.ObservationID().String(), descriptor.SessionID().String(), source.Host(), source.Name(), source.Version(),
		nullableString(source.Provider()), nullableString(source.Model()), descriptor.Scope().String(), descriptor.Accounting().String(),
		observation.Status().String(), formatTimestamp(descriptor.ObservedAt()),
	}
	args = append(args, usageFinalizationArgs(observation)[1:]...)
	args = append(args, nullableString(descriptor.SnapshotSeries()))
	if descriptor.SnapshotRevision() > 0 {
		args = append(args, descriptor.SnapshotRevision())
	} else {
		args = append(args, nil)
	}
	if predecessor, present := descriptor.SupersedesID().Value(); present {
		args = append(args, predecessor.String())
	} else {
		args = append(args, nil)
	}
	return args
}

func usageFinalizationArgs(observation *model.UsageObservation) []any {
	args := []any{observation.Status().String()}
	if finalizedAt, present := observation.FinalizedAt().Value(); present {
		args = append(args, formatTimestamp(finalizedAt))
	} else {
		args = append(args, nil)
	}
	if terminalCode, present := observation.TerminalCode().Value(); present {
		args = append(args, terminalCode.String())
	} else {
		args = append(args, nil)
	}
	for _, value := range []types.UsageValue{
		observation.Counters().Input(), observation.Counters().CachedInput(), observation.Counters().CacheWriteInput(),
		observation.Counters().Output(), observation.Counters().ReasoningOutput(), observation.Counters().Total(),
	} {
		args = append(args, value.State().String())
		if numeric, present := value.Value(); present {
			args = append(args, numeric)
		} else {
			args = append(args, nil)
		}
	}
	cost := observation.Cost()
	args = append(args, cost.State().String())
	if amount, present := cost.AmountMicros(); present {
		args = append(args, amount, cost.Currency(), cost.Origin().String(), nullableString(cost.PriceTableVersion()))
	} else {
		args = append(args, nil, nil, nil, nil)
	}
	return args
}
