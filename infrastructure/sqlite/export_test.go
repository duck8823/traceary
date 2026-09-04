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

// SetAfterVerifiedAttestationSnapshotForTest runs after the verified
// snapshot is captured and before inspect publishes.
func SetAfterVerifiedAttestationSnapshotForTest(fn func(context.Context, *sql.DB)) {
	afterVerifiedAttestationSnapshot = fn
}

// SetTimelinePayloadQueryHookForTest counts schema/body reads on the
// timeline walk. Pass nil to clear.
func (d *EventDatasource) SetTimelinePayloadQueryHookForTest(hook func(kind string)) {
	d.timelinePayloadQueryHook = hook
}

// SetAuditHydrationQueryHookForTest installs a hook called once for the
// schema probe ("schema") and once for the batch payload SELECT ("payload")
// inside HydrateCommandAudits. Tests use it to assert O(1) query count for
// a page of N events. Pass nil to clear.
func (d *EventDatasource) SetAuditHydrationQueryHookForTest(hook func(kind string)) {
	d.onAuditHydrationQuery = hook
}

// SetGarbageCollectionNowForTest fixes the timestamp persisted by gc discards.
func (d *StoreManagementDatasource) SetGarbageCollectionNowForTest(now func() time.Time) {
	d.now = now
}

// SchemaDigestForTest exposes schemaDigest so tests can pin that command_audits
// DDL stays in schema comparison after the row-digest skip is added.
func SchemaDigestForTest(ctx context.Context, db *sql.DB) (string, error) {
	return schemaDigest(ctx, db)
}

// VerifyCommandAuditsForTest exposes the codec-independent audit projection.
// Compact's event-level checks never authorize dropping an audit-held event
// (dedupe skips auditHeld; GC does not delete events), so VerifyPair cannot
// reach this permitted-absence rule on its own.
func VerifyCommandAuditsForTest(ctx context.Context, sourceDB, candidateDB *sql.DB, candidateEventIDs map[string]struct{}) error {
	candidateEvents := make(map[string]eventVerifyRecord, len(candidateEventIDs))
	for id := range candidateEventIDs {
		candidateEvents[id] = eventVerifyRecord{}
	}
	return verifyCommandAudits(ctx, sourceDB, candidateDB, candidateEvents)
}

// OpenStoreForTest opens a live store through the same coordinated-lease,
// WAL, busy_timeout DSN production readers and writers use, so tests do not
// hand-write a divergent DSN for the store they exercise.
func OpenStoreForTest(path string) *sql.DB { return openCoordinatedDB(path, sqliteDSN(path)) }

// NewPreparedStoreUpgradeFilesForTest constructs files with an optional recovery hook.
func NewPreparedStoreUpgradeFilesForTest(held bool, hook func(string) error) PreparedStoreUpgradeFiles {
	return PreparedStoreUpgradeFiles{CallerHoldsExclusiveLease: held, recoveryHook: hook}
}

// SetPreparedUpgradeStepRecorderForTest captures recipe/protocol step names.
func SetPreparedUpgradeStepRecorderForTest(fn func(string)) { preparedUpgradeStepRecorder = fn }

// SetPreparedUpgradeFailureHookForTest injects copy/migration/verification failures.
func SetPreparedUpgradeFailureHookForTest(fn func(string) error) {
	preparedUpgradeFailureHook = fn
}

// PreparedVerifyOpenIsReadOnlyForTest reports the last upgrade verify reopen result.
func PreparedVerifyOpenIsReadOnlyForTest() bool { return preparedVerifyOpenIsReadOnly }

// SetPlanAvailableBytesForTest fakes free space for Plan preflight.
func SetPlanAvailableBytesForTest(fn func(string) (uint64, error)) {
	if fn == nil {
		planAvailableBytes = availableBytes
		return
	}
	planAvailableBytes = fn
}

// SetSpoolReserveBytesForTest fakes the spool-reserve term.
func SetSpoolReserveBytesForTest(fn func(string) uint64) { inspectSpoolReserveBytes = fn }

// IdenticalObservationCountsForTest exposes the rejected identical-counts comparison.
func IdenticalObservationCountsForTest(ctx context.Context, sourceDB, candidateDB *sql.DB) error {
	return identicalObservationCounts(ctx, sourceDB, candidateDB)
}

// OpenReadOnlyStoreForTest opens through the coordinated read-only lease path.
func OpenReadOnlyStoreForTest(path string) *sql.DB {
	return openCoordinatedReadOnlyDB(path, sqliteReadOnlyDSN(path))
}

// SetAfterOpenAtHookForTest counts or intercepts Database.openAt. Pass nil to clear.
func SetAfterOpenAtHookForTest(fn func()) { afterOpenAtHook = fn }

// SetAfterMaintenanceMarkerConsultForTest pauses Connect after the one-time
// RW marker consult. Pass nil to clear.
func SetAfterMaintenanceMarkerConsultForTest(fn func()) { afterMaintenanceMarkerConsult = fn }

// SetAfterSharedLeaseWouldBlockForTest runs on each shared-acquire EWOULDBLOCK.
func SetAfterSharedLeaseWouldBlockForTest(fn func()) { afterSharedLeaseWouldBlock = fn }

// SetCompactPendingActiveCheckForTest replaces the stale-reaping marker consult.
func SetCompactPendingActiveCheckForTest(fn func(string) bool) {
	if fn == nil {
		compactPendingActiveCheck = CompactPendingActive
		return
	}
	compactPendingActiveCheck = fn
}

// WriteCompactPendingMarkerForTest writes the live-pid compact-pending marker.
func WriteCompactPendingMarkerForTest(storePath string) error {
	return writeCompactPendingMarker(storePath)
}

// SetReadOnlyOpenHookForTest runs each time openReadOnly or WithReadScope
// opens a genuinely fresh read-only connection (not a scope/shared-handle
// reuse). Tests use it to assert O(1) opens across a read-scoped pass. Pass
// nil to clear.
func (d *Database) SetReadOnlyOpenHookForTest(hook func()) {
	d.afterReadOnlyConnectionOpened = hook
}

// SetCompatibilityCheckHookForTest runs each time checkStoreCompatibility
// succeeds inside openReadOnly or WithReadScope. Tests use it to assert the
// guard runs once per scope. Pass nil to clear.
func (d *Database) SetCompatibilityCheckHookForTest(hook func()) {
	d.afterCompatibilityCheck = hook
}
