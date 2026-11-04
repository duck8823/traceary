package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"golang.org/x/xerrors"
)

const historyMaintenancePoll = 10 * time.Millisecond

var _ queryservice.HistoryReadScope = (*Database)(nil)

// WithYieldingReadScope opens one bounded, maintenance-aware read-only scope.
// It never initializes/migrates a store or changes ordinary CLI reader behavior.
// Source/Git validation must run outside this scope, before and after the read.
func (d *Database) WithYieldingReadScope(parent context.Context, fn func(context.Context) error) (err error) {
	if fn == nil || d.sharedReadOnly != nil || parent.Value(readScopeKey{}) != nil {
		return queryservice.ErrHistoryReadUnsupported
	}
	if err = parent.Err(); err != nil {
		return classifyHistoryReadError(xerrors.Errorf("start yielding history read: %w", err), nil)
	}
	path := d.Path()
	budgetCtx, cancelBudget := context.WithTimeout(parent, queryservice.HistoryReadBudget)
	defer cancelBudget()
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return queryservice.ErrHistoryReadUnsupported
	}
	canonical, pathErr := filepath.EvalSymlinks(path)
	if pathErr != nil || canonical != path {
		return queryservice.ErrHistoryReadUnsupported
	}
	if pathErr = validateStoreLinkIdentity(path); pathErr != nil {
		return errors.Join(queryservice.ErrHistoryReadUnsupported, pathErr)
	}
	if err = budgetCtx.Err(); err != nil {
		return classifyHistoryReadError(err, nil)
	}
	ctx, cancelRead := context.WithCancelCause(budgetCtx)
	db := openCoordinatedYieldingReadOnlyDB(path, sqliteReadOnlyDSN(path))
	watchDone := make(chan struct{})
	go watchHistoryMaintenance(ctx, path, cancelRead, watchDone)
	defer func() {
		// Capture the reason before our own cancellation, which also interrupts
		// any accidentally retained QueryContext rows before sql.DB.Close joins.
		cause := context.Cause(ctx)
		cancelRead(nil)
		cancelBudget()
		<-watchDone
		if closeErr := db.Close(); closeErr != nil {
			err = errors.Join(err, xerrors.Errorf("close yielding history read: %w", closeErr))
		}
		err = classifyHistoryReadError(err, cause)
	}()
	// Do not use StoreOpenContext: a caller's wider open context must never
	// extend this scope's total deadline. The datasource borrows this handle.
	if err = db.PingContext(ctx); err != nil {
		return xerrors.Errorf("open yielding history read: %w", err)
	}
	if err = verifyHistoryReadCompatibility(ctx, db); err != nil {
		return err
	}
	if err = fn(context.WithValue(ctx, readScopeKey{}, &readScope{db: db})); err != nil {
		return xerrors.Errorf("query yielding history read: %w", err)
	}
	return nil
}

func watchHistoryMaintenance(ctx context.Context, path string, cancel context.CancelCauseFunc, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(historyMaintenancePoll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := consultHistoryMaintenanceMarker(path); err != nil {
			cancel(err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// History reads cannot use the empty/pre-36 bootstrap exception needed by the
// write-side initializer. Validate required projection columns without loading
// any rows or bodies; the existing metadata datasource still owns membership SQL.
func verifyHistoryReadCompatibility(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE (name='store_format_state' AND type='table') OR (name='event_metadata_projection' AND type IN ('table','view'))`).Scan(&count)
	if err == nil && count != 2 {
		return queryservice.ErrHistoryReadUnsupported
	}
	if err == nil {
		err = checkStoreCompatibility(ctx, db)
	}
	if err == nil {
		var rows *sql.Rows
		rows, err = db.QueryContext(ctx, `SELECT id, kind, client, agent, session_id, workspace, source_hook, created_at, created_at_norm, body_original_bytes, body_stored_bytes, body_ingest_truncated, body_storage_truncated, body_metadata_version, command_audit_event_id, command_exit_code, command_failed FROM event_metadata_projection LIMIT 0`)
		if err == nil {
			err = rows.Close()
		}
	}
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || historySQLiteBusy(err) {
		return xerrors.Errorf("inspect yielding history schema: %w", err)
	}
	return errors.Join(queryservice.ErrHistoryReadUnsupported, xerrors.Errorf("inspect yielding history schema: %w", err))
}

func historySQLiteBusy(err error) bool {
	var code interface{ Code() int }
	return errors.As(err, &code) && (code.Code()&0xff == 5 || code.Code()&0xff == 6)
}

func classifyHistoryReadError(err, cause error) error {
	var pending *apptypes.StoreMaintenancePendingError
	if errors.As(err, &pending) || errors.As(cause, &pending) || errors.Is(err, errStoreLeaseSelfDeadlock) || errors.Is(err, context.DeadlineExceeded) || errors.Is(cause, context.DeadlineExceeded) || historySQLiteBusy(err) {
		return errors.Join(queryservice.ErrHistoryReadBusy, err, cause)
	}
	if errors.Is(err, ErrPreparedUpgradeUnsupported) {
		return errors.Join(queryservice.ErrHistoryReadUnsupported, err, cause)
	}
	return errors.Join(err, cause)
}
