package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain"
)

func newArchiveInventoryGenerationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create archive inventory generation ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

// StartArchiveSequenceInventory starts a new bounded historical inventory.
// Stable mappings from an abandoned generation are retained and skipped.
//
//nolint:wrapcheck // SQL errors retain context at this dedicated adapter boundary.
func (d *Database) StartArchiveSequenceInventory(ctx context.Context, budget apptypes.ArchiveSequenceBudget) (domain.ArchiveInventoryGeneration, error) {
	if !budget.Valid() {
		return domain.ArchiveInventoryGeneration{}, apptypes.ErrArchiveSequenceLimit
	}
	id, err := newArchiveInventoryGenerationID()
	if err != nil {
		return domain.ArchiveInventoryGeneration{}, err
	}
	operationCtx, cancel := boundedArchiveContext(ctx, budget.WallTime, budget.LockTime)
	defer cancel()
	db, err := d.open(operationCtx)
	if err != nil {
		return domain.ArchiveInventoryGeneration{}, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(operationCtx, nil)
	if err != nil {
		return domain.ArchiveInventoryGeneration{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(operationCtx, `
UPDATE archive_sequence_inventory_state
SET generation_id=?, config_hash=?, phase='inventory', event_cursor='', event_cursor_started=0,
    verify_cursor=0, high_water=0, revision=revision+1, failure_class=''
WHERE singleton=1 AND phase NOT IN ('inventory','verifying')`, id, budget.ConfigHash())
	if err != nil {
		return domain.ArchiveInventoryGeneration{}, err
	}
	if changed, changeErr := result.RowsAffected(); changeErr != nil || changed != 1 {
		return domain.ArchiveInventoryGeneration{}, apptypes.ErrArchiveSequenceStaleGeneration
	}
	if _, err = tx.ExecContext(operationCtx, `UPDATE archive_sequence_activation SET active_generation_id='',verified_high_water=0 WHERE singleton=1`); err != nil {
		return domain.ArchiveInventoryGeneration{}, err
	}
	var revision int64
	if err = tx.QueryRowContext(operationCtx, `SELECT revision FROM archive_sequence_inventory_state WHERE singleton=1`).Scan(&revision); err != nil {
		return domain.ArchiveInventoryGeneration{}, err
	}
	if err = tx.Commit(); err != nil {
		return domain.ArchiveInventoryGeneration{}, err
	}
	return domain.ArchiveInventoryGeneration{ID: id, Phase: domain.ArchiveInventoryScanning, Revision: revision}, nil
}

// SelectArchiveSequenceInventory returns one stable ID-keyset page without a
// write lock. Apply performs the generation/cursor CAS.
//
//nolint:wrapcheck // SQL errors retain context at this dedicated adapter boundary.
func (d *Database) SelectArchiveSequenceInventory(ctx context.Context, budget apptypes.ArchiveSequenceBudget) (out apptypes.ArchiveSequenceInventorySnapshot, err error) {
	if !budget.Valid() {
		return out, apptypes.ErrArchiveSequenceLimit
	}
	operationCtx, cancel := context.WithTimeout(ctx, budget.WallTime)
	defer cancel()
	db, err := d.open(operationCtx)
	if err != nil {
		return out, err
	}
	defer d.release(db)
	var phase string
	if err = db.QueryRowContext(operationCtx, `SELECT generation_id,config_hash,phase,event_cursor,event_cursor_started,revision FROM archive_sequence_inventory_state WHERE singleton=1`).Scan(
		&out.Generation.ID, &out.ConfigHash, &phase, &out.Cursor, &out.CursorStarted, &out.Generation.Revision,
	); err != nil {
		return out, err
	}
	out.Generation.Phase = domain.ArchiveInventoryPhase(phase)
	if out.Generation.Phase != domain.ArchiveInventoryScanning {
		return out, apptypes.ErrArchiveSequenceIncomplete
	}
	if out.ConfigHash != budget.ConfigHash() {
		return out, apptypes.ErrArchiveSequenceStaleGeneration
	}
	out, err = selectArchiveInventoryPage(operationCtx, db, budget, out)
	if err != nil {
		return out, err
	}
	out.PageDigest = archiveInventoryPageDigest(out)
	return out, nil
}

// ApplyArchiveSequenceInventory allocates a selected page and advances its
// cursor atomically. A rollback therefore consumes no archive sequence.
//
//nolint:wrapcheck // SQL errors retain context at this dedicated adapter boundary.
func (d *Database) ApplyArchiveSequenceInventory(ctx context.Context, budget apptypes.ArchiveSequenceBudget, snapshot apptypes.ArchiveSequenceInventorySnapshot) (out apptypes.ArchiveSequenceProgress, err error) {
	if !budget.Valid() {
		return out, apptypes.ErrArchiveSequenceLimit
	}
	if snapshot.ConfigHash != budget.ConfigHash() || snapshot.Generation.ID == "" {
		return out, apptypes.ErrArchiveSequenceStaleGeneration
	}
	if snapshot.Generation.Phase != domain.ArchiveInventoryScanning {
		return out, apptypes.ErrArchiveSequenceIncomplete
	}
	operationCtx, cancel := boundedArchiveContext(ctx, budget.WallTime, budget.LockTime)
	defer cancel()
	db, err := d.open(operationCtx)
	if err != nil {
		return out, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(operationCtx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()

	if err = assertArchiveInventoryCAS(operationCtx, tx, snapshot); err != nil {
		return out, err
	}
	exact := apptypes.ArchiveSequenceInventorySnapshot{Generation: snapshot.Generation, ConfigHash: snapshot.ConfigHash, Cursor: snapshot.Cursor, CursorStarted: snapshot.CursorStarted}
	exact, err = selectArchiveInventoryPage(operationCtx, tx, budget, exact)
	if err != nil {
		return out, err
	}
	exact.PageDigest = archiveInventoryPageDigest(exact)
	if snapshot.PageDigest == "" || snapshot.PageDigest != exact.PageDigest {
		return out, apptypes.ErrArchiveSequenceDrift
	}
	snapshot = exact
	lastCursor := snapshot.Cursor
	lastStarted := snapshot.CursorStarted
	for _, item := range snapshot.Items {
		if item.EventID == "" || item.LogicalBytes < 0 {
			return out, apptypes.ErrArchiveSequenceDrift
		}
		var exists int
		if err = tx.QueryRowContext(operationCtx, `SELECT EXISTS(SELECT 1 FROM events WHERE id=?)`, item.EventID).Scan(&exists); err != nil || exists != 1 {
			if err == nil {
				err = apptypes.ErrArchiveSequenceDrift
			}
			return out, err
		}
		var mapped int
		if err = tx.QueryRowContext(operationCtx, `SELECT EXISTS(SELECT 1 FROM archive_event_sequences WHERE event_id=?)`, item.EventID).Scan(&mapped); err != nil {
			return out, err
		}
		if mapped == 0 {
			if !item.Missing {
				return out, apptypes.ErrArchiveSequenceDrift
			}
			if err = allocateArchiveSequence(operationCtx, tx, item.EventID); err != nil {
				return out, err
			}
			out.Assigned++
		}
		out.Processed++
		lastCursor, lastStarted = item.EventID, true
	}

	phase := "inventory"
	highWater := int64(0)
	if snapshot.Done {
		phase = "verifying"
		if err = tx.QueryRowContext(operationCtx, `SELECT next_sequence-1 FROM archive_sequence_allocator WHERE singleton=1`).Scan(&highWater); err != nil {
			return out, err
		}
	}
	result, err := tx.ExecContext(operationCtx, `UPDATE archive_sequence_inventory_state SET event_cursor=?,event_cursor_started=?,phase=?,high_water=?,verify_cursor=0,revision=revision+1 WHERE singleton=1 AND generation_id=? AND config_hash=? AND phase='inventory' AND revision=? AND event_cursor=? AND event_cursor_started=?`,
		lastCursor, boolInt(lastStarted), phase, highWater, snapshot.Generation.ID, snapshot.ConfigHash, snapshot.Generation.Revision, snapshot.Cursor, boolInt(snapshot.CursorStarted))
	if err != nil {
		return out, err
	}
	if changed, changeErr := result.RowsAffected(); changeErr != nil || changed != 1 {
		return out, apptypes.ErrArchiveSequenceStaleGeneration
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Generation = domain.ArchiveInventoryGeneration{ID: snapshot.Generation.ID, Phase: domain.ArchiveInventoryPhase(phase), HighWater: highWater, Revision: snapshot.Generation.Revision + 1}
	out.Done = snapshot.Done
	return out, nil
}

// VerifyArchiveSequenceInventory verifies one bounded sequence page. The last
// page activates the generation only after the allocator and maximum mapping
// agree in the same transaction snapshot.
//
//nolint:wrapcheck // SQL errors retain context at this dedicated adapter boundary.
func (d *Database) VerifyArchiveSequenceInventory(ctx context.Context, budget apptypes.ArchiveSequenceBudget, generation domain.ArchiveInventoryGeneration) (out apptypes.ArchiveSequenceProgress, err error) {
	if !budget.Valid() {
		return out, apptypes.ErrArchiveSequenceLimit
	}
	if generation.Phase != domain.ArchiveInventoryVerifying || generation.ID == "" {
		return out, apptypes.ErrArchiveSequenceIncomplete
	}
	if budget.StoredBytes < 16 || budget.WriteBytes < 8 {
		return out, apptypes.ErrArchiveSequenceLimit
	}
	operationCtx, cancel := boundedArchiveContext(ctx, budget.WallTime, budget.LockTime)
	defer cancel()
	db, err := d.open(operationCtx)
	if err != nil {
		return out, err
	}
	defer d.release(db)
	tx, err := db.BeginTx(operationCtx, nil)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	var configHash, phase string
	var cursor, highWater, revision int64
	if err = tx.QueryRowContext(operationCtx, `SELECT config_hash,phase,verify_cursor,high_water,revision FROM archive_sequence_inventory_state WHERE singleton=1 AND generation_id=?`, generation.ID).Scan(&configHash, &phase, &cursor, &highWater, &revision); err != nil {
		return out, err
	}
	if configHash != budget.ConfigHash() || phase != "verifying" || revision != generation.Revision || highWater != generation.HighWater {
		return out, apptypes.ErrArchiveSequenceStaleGeneration
	}
	rows, err := tx.QueryContext(operationCtx, `SELECT m.sequence,e.id IS NOT NULL FROM archive_event_sequences m LEFT JOIN events e ON e.id=m.event_id WHERE m.sequence>? AND m.sequence<=? ORDER BY m.sequence LIMIT ?`, cursor, highWater, budget.Rows+1)
	if err != nil {
		return out, err
	}
	var stored int64
	processed := 0
	newCursor := cursor
	expected := cursor + 1
	more := false
	for rows.Next() {
		var sequence int64
		var eventExists bool
		if err = rows.Scan(&sequence, &eventExists); err != nil {
			_ = rows.Close()
			return out, err
		}
		if processed >= budget.Rows || stored+16 > budget.StoredBytes {
			more = true
			break
		}
		if sequence != expected || !eventExists {
			_ = rows.Close()
			return d.failArchiveVerification(operationCtx, tx, generation.ID, "sequence_gap_or_orphan")
		}
		newCursor = sequence
		expected++
		processed++
		stored += 16
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	if processed == 0 && cursor < highWater {
		return d.failArchiveVerification(operationCtx, tx, generation.ID, "sequence_gap")
	}
	done := newCursor == highWater && !more
	if done {
		var next, maximum int64
		if err = tx.QueryRowContext(operationCtx, `SELECT next_sequence FROM archive_sequence_allocator WHERE singleton=1`).Scan(&next); err != nil {
			return out, err
		}
		if err = tx.QueryRowContext(operationCtx, `SELECT COALESCE(MAX(sequence),0) FROM archive_event_sequences`).Scan(&maximum); err != nil {
			return out, err
		}
		if next <= 0 || next == math.MinInt64 || maximum != next-1 || maximum < highWater {
			return d.failArchiveVerification(operationCtx, tx, generation.ID, "allocator_mismatch")
		}
		var unmapped int
		if err = tx.QueryRowContext(operationCtx, `SELECT EXISTS(SELECT 1 FROM events e LEFT JOIN archive_event_sequences m ON m.event_id=e.id WHERE m.event_id IS NULL LIMIT 1)`).Scan(&unmapped); err != nil {
			return out, err
		}
		if unmapped != 0 {
			return d.failArchiveVerification(operationCtx, tx, generation.ID, "unmapped_event")
		}
	}
	newPhase := "verifying"
	if done {
		newPhase = "complete"
	}
	result, err := tx.ExecContext(operationCtx, `UPDATE archive_sequence_inventory_state SET verify_cursor=?,phase=?,revision=revision+1 WHERE singleton=1 AND generation_id=? AND phase='verifying' AND revision=? AND verify_cursor=?`, newCursor, newPhase, generation.ID, revision, cursor)
	if err != nil {
		return out, err
	}
	if changed, changeErr := result.RowsAffected(); changeErr != nil || changed != 1 {
		return out, apptypes.ErrArchiveSequenceStaleGeneration
	}
	if done {
		if _, err = tx.ExecContext(operationCtx, `UPDATE archive_sequence_activation SET active_generation_id=?,verified_high_water=? WHERE singleton=1`, generation.ID, highWater); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Generation = domain.ArchiveInventoryGeneration{ID: generation.ID, Phase: domain.ArchiveInventoryPhase(newPhase), HighWater: highWater, Revision: revision + 1}
	out.Processed = processed
	out.Done = done
	return out, nil
}

// ArchiveSequenceStatus returns aggregate-only readiness evidence.
//
//nolint:wrapcheck // SQL errors retain context at this dedicated adapter boundary.
func (d *Database) ArchiveSequenceStatus(ctx context.Context) (out apptypes.ArchiveSequenceStatus, err error) {
	db, err := d.open(ctx)
	if err != nil {
		return out, err
	}
	defer d.release(db)
	var phase string
	if err = db.QueryRowContext(ctx, `SELECT l.store_id,s.generation_id,s.phase,s.high_water,s.revision,a.active_generation_id,a.verified_high_water,q.next_sequence FROM archive_store_lineage l JOIN archive_sequence_inventory_state s ON s.singleton=l.singleton JOIN archive_sequence_activation a ON a.singleton=l.singleton JOIN archive_sequence_allocator q ON q.singleton=l.singleton WHERE l.singleton=1`).Scan(
		&out.StoreID, &out.Generation.ID, &phase, &out.Generation.HighWater, &out.Generation.Revision, &out.ActiveGenerationID, &out.VerifiedHighWater, &out.AllocatorNext,
	); err != nil {
		return out, err
	}
	out.Generation.Phase = domain.ArchiveInventoryPhase(phase)
	out.MappedEvents = out.AllocatorNext - 1
	return out, nil
}

func boundedArchiveContext(parent context.Context, wall, lock time.Duration) (context.Context, context.CancelFunc) {
	limit := wall
	if lock < limit {
		limit = lock
	}
	return context.WithTimeout(parent, limit)
}

//nolint:wrapcheck // internal SQL helper preserves the driver error for its caller.
func assertArchiveInventoryCAS(ctx context.Context, tx *sql.Tx, snapshot apptypes.ArchiveSequenceInventorySnapshot) error {
	var generation, config, phase, cursor string
	var started bool
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT generation_id,config_hash,phase,event_cursor,event_cursor_started,revision FROM archive_sequence_inventory_state WHERE singleton=1`).Scan(&generation, &config, &phase, &cursor, &started, &revision); err != nil {
		return err
	}
	if generation != snapshot.Generation.ID || config != snapshot.ConfigHash || phase != "inventory" || cursor != snapshot.Cursor || started != snapshot.CursorStarted || revision != snapshot.Generation.Revision {
		return apptypes.ErrArchiveSequenceStaleGeneration
	}
	return nil
}

//nolint:wrapcheck // internal SQL helper preserves the driver error for its caller.
func allocateArchiveSequence(ctx context.Context, tx *sql.Tx, eventID string) error {
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT next_sequence FROM archive_sequence_allocator WHERE singleton=1`).Scan(&next); err != nil {
		return err
	}
	if next == math.MaxInt64 {
		return apptypes.ErrArchiveSequenceOverflow
	}
	result, err := tx.ExecContext(ctx, `UPDATE archive_sequence_allocator SET next_sequence=next_sequence+1 WHERE singleton=1 AND next_sequence=?`, next)
	if err != nil {
		return err
	}
	if changed, changeErr := result.RowsAffected(); changeErr != nil || changed != 1 {
		return apptypes.ErrArchiveSequenceDrift
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO archive_event_sequences(event_id,sequence) VALUES(?,?)`, eventID, next); err != nil {
		return err
	}
	return nil
}

//nolint:wrapcheck // internal SQL helper preserves the driver error for its caller.
func (d *Database) failArchiveVerification(ctx context.Context, tx *sql.Tx, generationID, class string) (apptypes.ArchiveSequenceProgress, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE archive_sequence_inventory_state SET phase='failed',failure_class=?,revision=revision+1 WHERE singleton=1 AND generation_id=? AND phase='verifying'`, class, generationID); err != nil {
		return apptypes.ArchiveSequenceProgress{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE archive_sequence_activation SET active_generation_id='',verified_high_water=0 WHERE singleton=1`); err != nil {
		return apptypes.ArchiveSequenceProgress{}, err
	}
	if err := tx.Commit(); err != nil {
		return apptypes.ArchiveSequenceProgress{}, err
	}
	cause := apptypes.ErrArchiveSequenceGap
	if class == "unmapped_event" {
		cause = apptypes.ErrArchiveSequenceIncomplete
	}
	return apptypes.ArchiveSequenceProgress{}, fmt.Errorf("%w: %w (%s)", apptypes.ErrArchiveSequenceActivation, cause, class)
}

type archivePageQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

//nolint:wrapcheck // internal SQL helper preserves driver errors for the adapter boundary.
func selectArchiveInventoryPage(ctx context.Context, q archivePageQueryer, budget apptypes.ArchiveSequenceBudget, out apptypes.ArchiveSequenceInventorySnapshot) (apptypes.ArchiveSequenceInventorySnapshot, error) {
	query := `SELECT length(CAST(e.id AS BLOB)),CASE WHEN length(CAST(e.id AS BLOB))<=? THEN e.id ELSE NULL END,m.event_id IS NULL FROM events e LEFT JOIN archive_event_sequences m ON m.event_id=e.id ORDER BY e.id COLLATE BINARY LIMIT ?`
	args := []any{budget.StoredBytes, budget.Rows + 1}
	if out.CursorStarted {
		query = `SELECT length(CAST(e.id AS BLOB)),CASE WHEN length(CAST(e.id AS BLOB))<=? THEN e.id ELSE NULL END,m.event_id IS NULL FROM events e LEFT JOIN archive_event_sequences m ON m.event_id=e.id WHERE e.id>? COLLATE BINARY ORDER BY e.id COLLATE BINARY LIMIT ?`
		args = []any{budget.StoredBytes, out.Cursor, budget.Rows + 1}
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	var stored, written int64
	more := false
	for rows.Next() {
		var length int64
		var id sql.NullString
		var missing bool
		if err = rows.Scan(&length, &id, &missing); err != nil {
			return out, err
		}
		if !id.Valid {
			return out, apptypes.ErrArchiveSequenceLimit
		}
		wb := int64(0)
		if missing {
			wb = length + 16
		}
		if len(out.Items) >= budget.Rows || stored+length > budget.StoredBytes || written+wb > budget.WriteBytes {
			more = true
			break
		}
		out.Items = append(out.Items, apptypes.ArchiveSequenceInventoryItem{EventID: id.String, Missing: missing, LogicalBytes: wb})
		stored += length
		written += wb
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) == 0 && more {
		return out, apptypes.ErrArchiveSequenceLimit
	}
	out.Done = !more
	return out, nil
}

func archiveInventoryPageDigest(page apptypes.ArchiveSequenceInventorySnapshot) string {
	h := sha256.New()
	h.Write([]byte("traceary/archive-inventory-page/v1"))
	frame := func(b []byte) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	frame([]byte(page.Generation.ID))
	frame([]byte(page.ConfigHash))
	frame([]byte(page.Cursor))
	if page.CursorStarted {
		frame([]byte{1})
	} else {
		frame([]byte{0})
	}
	for _, i := range page.Items {
		frame([]byte(i.EventID))
		if i.Missing {
			frame([]byte{1})
		} else {
			frame([]byte{0})
		}
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(i.LogicalBytes))
		frame(n[:])
	}
	if page.Done {
		frame([]byte{1})
	} else {
		frame([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
