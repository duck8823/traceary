package sqlite

import (
	"context"

	"github.com/duck8823/traceary/domain/model"
)

// SetListWindowBatchHookForTest installs a hook that fires once per internal
// paged read performed by ListWindow. Tests use it to assert the scan loop
// actually issues multiple batches rather than returning all rows in a single
// query. Pass nil to clear.
func (d *EventDatasource) SetListWindowBatchHookForTest(hook func(batchIndex, batchSize int)) {
	d.onListWindowBatch = hook
}

// SaveSessionBoundaryForTest exposes saveSessionBoundary so tests can seed
// the sessions table (insert + optional end update) without going through
// SaveBoundary. SaveBoundary would also append a session_started or
// session_ended event, which tests that verify independent event counts
// need to control themselves.
func (d *SessionDatasource) SaveSessionBoundaryForTest(ctx context.Context, session *model.Session) error {
	db, err := d.db.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return saveSessionBoundary(ctx, db, session)
}
