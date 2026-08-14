package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/duck8823/traceary/domain/attestation"
	"github.com/duck8823/traceary/domain/model"
)

// SetRawBodyPrunedHookForTest injects an interruption after a candidate write.
func (d *StoreManagementDatasource) SetRawBodyPrunedHookForTest(hook func(index int) error) {
	d.onRawBodyPruned = hook
}

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

// LoadEventPlaintextForTest exposes codec decoding for persistence assertions.
func LoadEventPlaintextForTest(ctx context.Context, db *sql.DB, eventID string) ([]byte, error) {
	return loadEventPlaintext(ctx, db, eventID)
}

// PublishAttestationAnchorForTest exposes the locked sidecar append.
func PublishAttestationAnchorForTest(path string, record attestation.AnchorRecord) error {
	return publishAttestationAnchor(path, record)
}

// SetGarbageCollectionNowForTest fixes the timestamp persisted by gc discards.
func (d *StoreManagementDatasource) SetGarbageCollectionNowForTest(now func() time.Time) {
	d.now = now
}
