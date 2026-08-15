package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"log/slog"
	"math"
	"math/bits"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domaintypes "github.com/duck8823/traceary/domain/types"
)

//go:embed sql/measure_search_projection_family_split.sql
var measureSearchProjectionFamilySplitSQL string

//go:embed sql/select_search_projection_family_total.sql
var selectSearchProjectionFamilyTotalSQL string

//go:embed sql/select_search_projection_recent_cutoff.sql
var selectSearchProjectionRecentCutoffSQL string

//go:embed sql/select_search_projection_logical_non_recent_bytes.sql
var selectSearchProjectionLogicalNonRecentBytesSQL string

//go:embed sql/reclaim_search_projection_fts.sql
var reclaimSearchProjectionFTSSQL string

//go:embed sql/select_search_projection_fts_logical_bytes.sql
var selectSearchProjectionFTSLogicalBytesSQL string

// lowerSearchASCII folds ASCII only, matching SQLite's bundled lower(). The
// projection and the query must fold identically, so this cannot be replaced
// with strings.ToLower: that would fold non-ASCII on one side only.
func lowerSearchASCII(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}

const searchProjectionVersion = 1
const searchProjectionKeywordVersion = 1
const searchProjectionSummaryVersion = 1

// searchProjectionIndexFamilyName is the durable label for physical bytes of
// the bounded projection (search_projection_* + literal_search_*). It is never
// the legacy migration-032 event_search_* family — that number belongs to #1718.
const searchProjectionIndexFamilyName = "bounded_search_projection"

// searchProjectionFallbackAmplificationPPM is the #1620 measured ratio of
// recent-family physical bytes to source text (2.16x), expressed as parts per
// million. Used only when the store has no usable sample.
const searchProjectionFallbackAmplificationPPM int64 = 2_160_000

// searchProjectionMinAmplificationSampleBytes is the minimum SUM(decoded_bytes)
// required before a measured amplification is trusted. Below this the ratio is
// dominated by fixed per-table overhead.
const searchProjectionMinAmplificationSampleBytes int64 = 8 << 20

// Each incremental merge step is limited to this many FTS5 output pages. The
// reclaim loop checks its time budget before every step; this page bound also
// limits output pages, not the amount of input scanned by FTS5.
const searchProjectionFTSMergePages int64 = 16

// The step cap prevents one unexpectedly expensive reclaim loop from consuming
// the whole window. Each step is committed independently, so a partial merge
// remains durable when a later step fails.
const searchProjectionFTSReclaimStepCap = 8

// searchProjectionCutoffTimeout bounds the source-phase prefilter walk. A
// timeout yields age-only retention; eviction still enforces the ceiling.
const searchProjectionCutoffTimeout = 2 * time.Second

// searchProjectionCutoffSlackFactor loosens the source-phase prefilter walk
// ceiling relative to the derived source-text ceiling. The prefilter counts
// body_plaintext_bytes (whole envelopes), which diverges from decoded_bytes in
// both directions; a factor of 4 keeps thinking-heavy corpora from irreversibly
// excluding text that still fits under the true ceiling. Eviction is the exact
// enforcement. A ceiling corrected upward after re-derivation still cannot
// re-project documents the prefilter already excluded (v0.34 follow-up).
const searchProjectionCutoffSlackFactor int64 = 4

// searchProjectionCutoffRetainNothing is the persisted cutoff for a derived
// ceiling of 0 — the permanent tiers alone exhaust the budget, so the recent
// tier must stay empty. A far-future timestamp makes the planner's
// created.After(cutoff) false for every row without a second retention flag.
// It is deliberately not the empty string: empty already means "the whole
// corpus fits", the exact opposite.
const searchProjectionCutoffRetainNothing = "9999-12-31T23:59:59.999999999Z"

// capacityDerivation holds the measured family split and the derived source
// ceiling. SplitEvidence answers "was the family byte figure measured"
// (feeds cutover_before_evidence_*). Evidence answers "was the amplification
// / reserve derivation usable" (feeds capacity_evidence_*). They are not the
// same question: a perfectly measured family with a sample below the minimum
// is measured for cutover and unavailable for capacity.
type capacityDerivation struct {
	// RecentBytes is recent-tier dbstat allocation. NonRecentPhysical is
	// NonRecentScoped + NonRecentShared (raw). NonRecentBytes is the reserve
	// used for the ceiling: shared fully + scoped apportioned by generation
	// (see deriveSearchProjectionCapacity / scopedNonRecentReserve).
	RecentBytes, NonRecentScoped, NonRecentShared, NonRecentPhysical, NonRecentBytes int64
	SampleSourceBytes                                                                int64
	AmplificationPPM                                                                 int64
	SourceCeiling                                                                    int64
	SplitEvidence                                                                    apptypes.CapacityEvidence
	Evidence                                                                         apptypes.CapacityEvidence
}

// mulDiv returns floor(a*b/c) without intermediate int64 overflow, using
// math/bits.Mul64 + Div64. Negative inputs are clamped to 0 before bits is
// reached. c <= 0 yields 0. When the 128-bit product's high half is >= c the
// quotient would not fit in 64 bits and the result saturates to MaxInt64.
func mulDiv(a, b, c int64) int64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if c <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return math.MaxInt64
	}
	quo, _ := bits.Div64(hi, lo, uint64(c))
	if quo > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(quo)
}

func generationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(b[:])
}

const searchProjectionStartStateSQL = `UPDATE search_projection_state SET generation_id=?,config_hash=?,source_revision=?,high_water=?,checkpoint=0,phase='source',cleanup_scope='old',failure_class='',state='rebuilding',recent_source_bytes=0,recent_age_seconds=?,index_family_byte_limit=?,recent_byte_limit=?,capacity_semantics_version=?,recent_source_ceiling_bytes=?,recent_amplification_ppm=?,non_recent_family_bytes=?,recent_cutoff_norm=?,capacity_evidence_status=?,capacity_evidence_reason=?,index_family_within_budget=-1,cutover_index_family=?,cutover_family_bytes_before=?,cutover_family_bytes_after=0,cutover_before_evidence_status=?,cutover_before_evidence_reason=?,cutover_after_evidence_status='',cutover_after_evidence_reason='',updated_at=? `

func (d *Database) measureSearchProjectionStart(ctx context.Context, db *sql.DB, b apptypes.SearchProjectionBudget) (capacityDerivation, string, int64, apptypes.CapacityEvidence) {
	var lastNonRecent int64
	_ = db.QueryRowContext(ctx, `SELECT non_recent_family_bytes FROM search_projection_state WHERE singleton=1`).Scan(&lastNonRecent)
	// At Start the new generation has no rows yet, so the outgoing generation's
	// own non-recent size is the best available estimate of what the new one
	// will need (#1679 MUST 4b).
	var reserveGenerationID string
	_ = db.QueryRowContext(ctx, `SELECT COALESCE(active_generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(&reserveGenerationID)
	derivation := d.deriveSearchProjectionCapacity(ctx, db, b.IndexFamilyBytes, lastNonRecent, reserveGenerationID)
	if derivation.Evidence.Status == searchProjectionEvidenceUnavailable {
		slog.Warn("search projection capacity evidence unavailable at start; using documented amplification estimate",
			"reason", derivation.Evidence.Reason,
			"amplification_ppm", derivation.AmplificationPPM,
			"source_ceiling_bytes", derivation.SourceCeiling,
			"non_recent_family_bytes", derivation.NonRecentBytes,
		)
	}
	cutoffNorm, cutoffReason := d.deriveSearchProjectionRecentCutoff(ctx, db, derivation.SourceCeiling)
	if cutoffReason != "" {
		// "Corpus fits" (ErrNoRows → empty reason) and "prefilter did not run"
		// are different states; only the latter folds into capacity evidence.
		derivation.Evidence = apptypes.CapacityEvidence{
			Status: searchProjectionEvidenceUnavailable,
			Method: "dbstat",
			Reason: foldEvidenceReason(derivation.Evidence.Reason, cutoffReason),
		}
		slog.Warn("search projection recent cutoff prefilter unavailable; retention is age-only until eviction",
			"reason", cutoffReason,
			"source_ceiling_bytes", derivation.SourceCeiling,
		)
	}
	familyBytesBefore := derivation.RecentBytes + derivation.NonRecentPhysical
	beforeEvidence := derivation.SplitEvidence
	if beforeEvidence.Status == "" {
		beforeEvidence = apptypes.CapacityEvidence{Status: searchProjectionEvidenceUnavailable, Method: "dbstat", Reason: "family not measured"}
	}
	return derivation, cutoffNorm, familyBytesBefore, beforeEvidence
}

func searchProjectionStartStateArgs(g apptypes.SearchProjectionGeneration, b apptypes.SearchProjectionBudget, derivation capacityDerivation, cutoffNorm string, familyBytesBefore int64, beforeEvidence apptypes.CapacityEvidence, now time.Time) []any {
	return []any{
		g.GenerationID, g.ConfigHash, g.SourceRevision, g.HighWater,
		int64(b.RecentAge / time.Second), b.IndexFamilyBytes, b.IndexFamilyBytes,
		apptypes.SearchProjectionCapacitySemanticsVersion,
		derivation.SourceCeiling, derivation.AmplificationPPM, derivation.NonRecentBytes, cutoffNorm,
		derivation.Evidence.Status, derivation.Evidence.Reason,
		searchProjectionIndexFamilyName, familyBytesBefore, beforeEvidence.Status, beforeEvidence.Reason, formatTimestamp(now.UTC()),
	}
}

func applySearchProjectionStartSideEffects(ctx context.Context, tx *sql.Tx, g apptypes.SearchProjectionGeneration, requiresInventory int, now time.Time) error {
	inventoryState := "complete"
	if requiresInventory != 0 {
		inventoryState = "rebuilding"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE search_projection_inventory_state SET generation_id=?,cursor='',cursor_started=0,state=? WHERE singleton=1`, g.GenerationID, inventoryState); err != nil {
		return xerrors.Errorf("write search projection inventory start state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE literal_search_projection_state SET generation_id=?,high_water=?,fingerprint_version=1,state='rebuilding',updated_at=? WHERE singleton=1`, g.GenerationID, g.HighWater, formatTimestamp(now.UTC())); err != nil {
		return xerrors.Errorf("write literal search start state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water) VALUES(?,'rebuilding',?,?,?)`, g.GenerationID, g.ConfigHash, g.SourceRevision, g.HighWater); err != nil {
		return xerrors.Errorf("insert search projection start lifecycle: %w", err)
	}
	return nil
}

func prepareStartedGeneration(ctx context.Context, tx *sql.Tx, b apptypes.SearchProjectionBudget) (apptypes.SearchProjectionGeneration, int, error) {
	g := apptypes.SearchProjectionGeneration{GenerationID: generationID(), ConfigHash: b.ConfigHash()}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&g.SourceRevision); err != nil {
		return g, 0, xerrors.Errorf("read source revision for projection start: %w", err)
	}
	var requiresInventory int
	if err := tx.QueryRowContext(ctx, `SELECT requires_inventory,(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence) FROM search_projection_inventory_compat WHERE singleton=1`).Scan(&requiresInventory, &g.HighWater); err != nil {
		return g, 0, xerrors.Errorf("read inventory compat for projection start: %w", err)
	}
	if requiresInventory != 0 {
		g.HighWater = 0
	}
	return g, requiresInventory, nil
}

// Start freezes monotonic SQLite rowid membership.
// Inserts after this point belong to the next generation; updates/deletes cause drift.
//
//nolint:wrapcheck,errcheck // SQL errors are returned without losing typed projection errors.
func (d *Database) Start(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	if !b.Valid() {
		return apptypes.SearchProjectionGeneration{}, errors.New("search projection budgets must all be positive")
	}
	db, e := d.open(ctx)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer db.Close()
	// Measure and derive before taking the write lock. The dbstat walk is
	// unbounded in the family's own size and would otherwise consume the start
	// budget; the cutoff walk is separately bounded.
	derivation, cutoffNorm, familyBytesBefore, beforeEvidence := d.measureSearchProjectionStart(ctx, db, b)
	lockCtx, cancel := context.WithTimeout(ctx, b.LockTime)
	defer cancel()
	tx, e := db.BeginTx(lockCtx, nil)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer tx.Rollback()
	g, requiresInventory, e := prepareStartedGeneration(lockCtx, tx, b)
	if e != nil {
		return g, e
	}
	// Write recent_byte_limit alongside index_family_byte_limit so a binary
	// rolled back past migration 055 still sees the column it knows
	// (recent_byte_limit is retired; kept only for that contract).
	args := searchProjectionStartStateArgs(g, b, derivation, cutoffNorm, familyBytesBefore, beforeEvidence, now)
	result, e := tx.ExecContext(lockCtx, searchProjectionStartStateSQL+`WHERE singleton=1 AND state<>'rebuilding'`, args...)
	if e == nil {
		if n, x := result.RowsAffected(); x != nil || n != 1 {
			return g, &apptypes.SearchProjectionNoProgressError{Reason: "a generation is already rebuilding"}
		}
		if e = applySearchProjectionStartSideEffects(lockCtx, tx, g, requiresInventory, now); e != nil {
			return g, e
		}
		e = tx.Commit()
	}
	return g, e
}

const replaceObsoleteGenerationSQL = searchProjectionStartStateSQL +
	`WHERE singleton=1 AND generation_id=? AND capacity_semantics_version<? AND (state IN ('complete','rebuilding','drifted') OR (state='failed' AND failure_class='abandoned'))`

// ReplaceObsoleteCapacityGeneration retires the observed obsolete generation
// and starts its replacement in one write transaction. A concurrent caller
// fenced on the same generation loses the UPDATE and observes the winner
// instead of abandoning it. A crash rolls the transaction back, so the next
// open still sees the old generation (or the abandoned corpse, which this
// fence also matches).
//
//nolint:wrapcheck,errcheck // SQL errors are returned without losing typed projection errors.
func (d *Database) ReplaceObsoleteCapacityGeneration(ctx context.Context, observedGenerationID string, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.SearchProjectionGeneration, error) {
	if !b.Valid() {
		return apptypes.SearchProjectionGeneration{}, errors.New("search projection budgets must all be positive")
	}
	if observedGenerationID == "" {
		return apptypes.SearchProjectionGeneration{}, &apptypes.SearchProjectionNoProgressError{Reason: "no obsolete generation to replace"}
	}
	db, e := d.open(ctx)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer db.Close()
	derivation, cutoffNorm, familyBytesBefore, beforeEvidence := d.measureSearchProjectionStart(ctx, db, b)
	lockCtx, cancel := context.WithTimeout(ctx, b.LockTime)
	defer cancel()
	tx, e := db.BeginTx(lockCtx, nil)
	if e != nil {
		return apptypes.SearchProjectionGeneration{}, e
	}
	defer tx.Rollback()
	g, requiresInventory, e := prepareStartedGeneration(lockCtx, tx, b)
	if e != nil {
		return g, e
	}
	if _, e = tx.ExecContext(lockCtx, `UPDATE search_projection_generation_lifecycle SET state='abandoned',abandoned_at=? WHERE generation_id=? AND state<>'complete'`, formatTimestamp(now.UTC()), observedGenerationID); e != nil {
		return g, e
	}
	args := searchProjectionStartStateArgs(g, b, derivation, cutoffNorm, familyBytesBefore, beforeEvidence, now)
	args = append(args, observedGenerationID, apptypes.SearchProjectionCapacitySemanticsVersion)
	result, e := tx.ExecContext(lockCtx, replaceObsoleteGenerationSQL, args...)
	if e != nil {
		return g, e
	}
	n, x := result.RowsAffected()
	if x != nil {
		return g, x
	}
	if n != 1 {
		return observeWinningObsoleteReplacement(lockCtx, tx, observedGenerationID)
	}
	if e = applySearchProjectionStartSideEffects(lockCtx, tx, g, requiresInventory, now); e != nil {
		return g, e
	}
	return g, tx.Commit()
}

func observeWinningObsoleteReplacement(ctx context.Context, tx *sql.Tx, observedGenerationID string) (apptypes.SearchProjectionGeneration, error) {
	var live apptypes.SearchProjectionGeneration
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(generation_id,''),config_hash,source_revision,high_water,checkpoint,capacity_semantics_version FROM search_projection_state WHERE singleton=1`).Scan(&live.GenerationID, &live.ConfigHash, &live.SourceRevision, &live.HighWater, &live.Checkpoint, &version); err != nil {
		return live, xerrors.Errorf("observe winning obsolete replacement: %w", err)
	}
	if live.GenerationID != "" && live.GenerationID != observedGenerationID && version >= apptypes.SearchProjectionCapacitySemanticsVersion {
		if err := tx.Commit(); err != nil {
			return live, xerrors.Errorf("commit observed obsolete replacement: %w", err)
		}
		return live, nil
	}
	return live, &apptypes.SearchProjectionNoProgressError{Reason: "obsolete generation is no longer replaceable"}
}

// SelectInventory reads a stable event-ID keyset without holding a write lock.
// Identity bytes use StoredBytes and insertion bytes use WriteBytes, keeping
// this phase under the same reviewed generation budget as payload projection.
//
//nolint:wrapcheck,errcheck // SQL errors preserve the typed inventory contract.
func (d *Database) SelectInventory(ctx context.Context, b apptypes.SearchProjectionBudget) (out apptypes.SearchProjectionInventorySnapshot, err error) {
	db, err := d.open(ctx)
	if err != nil {
		return out, err
	}
	defer db.Close()
	var state, phase string
	if err = db.QueryRowContext(ctx, `SELECT s.generation_id,s.config_hash,s.source_revision,i.cursor,i.cursor_started,s.state,i.state FROM search_projection_state s JOIN search_projection_inventory_state i ON i.singleton=s.singleton WHERE s.singleton=1`).Scan(&out.Generation.GenerationID, &out.Generation.ConfigHash, &out.Generation.SourceRevision, &out.Cursor, &out.CursorStarted, &state, &phase); err != nil {
		return out, err
	}
	if state != "rebuilding" || phase != "rebuilding" {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "historical inventory is not rebuilding"}
	}
	if out.Generation.ConfigHash != b.ConfigHash() {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	var revision int64
	if err = db.QueryRowContext(ctx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&revision); err != nil {
		return out, err
	}
	if revision != out.Generation.SourceRevision {
		if err = markProjectionDrifted(ctx, db, out.Generation.GenerationID); err != nil {
			return out, err
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	query := `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id ORDER BY e.id LIMIT ?`
	args := []any{b.Rows + 1}
	if out.CursorStarted {
		query = `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id WHERE e.id>? ORDER BY e.id LIMIT ?`
		args = []any{out.Cursor, b.Rows + 1}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var stored, written int64
	for rows.Next() {
		var id string
		var missing bool
		if err = rows.Scan(&id, &missing); err != nil {
			return out, err
		}
		identityBytes := int64(len(id))
		logicalBytes := int64(0)
		if missing {
			logicalBytes = identityBytes + 16
		}
		if len(out.Items) >= b.Rows || stored+identityBytes > b.StoredBytes || written+logicalBytes > b.WriteBytes {
			break
		}
		out.Items = append(out.Items, apptypes.SearchProjectionInventoryItem{EventID: id, LogicalBytes: logicalBytes, Missing: missing})
		stored += identityBytes
		written += logicalBytes
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) == 0 {
		var next string
		var missing bool
		nextQuery := `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id ORDER BY e.id LIMIT 1`
		nextArgs := []any{}
		if out.CursorStarted {
			nextQuery = `SELECT e.id,q.event_id IS NULL FROM events e LEFT JOIN search_projection_source_sequence q ON q.event_id=e.id WHERE e.id>? ORDER BY e.id LIMIT 1`
			nextArgs = []any{out.Cursor}
		}
		err = db.QueryRowContext(ctx, nextQuery, nextArgs...).Scan(&next, &missing)
		if err == nil {
			class, bytes, limit := "inventory_stored_bytes", int64(len(next)), b.StoredBytes
			if bytes <= limit && missing {
				class, bytes, limit = "inventory_write_bytes", int64(len(next))+16, b.WriteBytes
			}
			return out, &apptypes.SearchProjectionOversizeError{Class: class, Bytes: bytes, Limit: limit}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		out.Done = true
		return out, nil
	}
	var remaining int
	if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id>? LIMIT 1)`, out.Items[len(out.Items)-1].EventID).Scan(&remaining); err != nil {
		return out, err
	}
	out.Done = remaining == 0
	return out, nil
}

// ApplyInventoryBatch atomically persists the admitted identities and cursor.
// The final batch freezes high-water in the same transaction.
//
//nolint:wrapcheck,errcheck // SQL errors preserve the typed inventory contract; rollback is best effort.
func (d *Database) ApplyInventoryBatch(ctx context.Context, p apptypes.SearchProjectionInventoryPlan, lock time.Duration, now time.Time) (out apptypes.SearchProjectionProgress, err error) {
	lockCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	for {
		remaining := lock
		if deadline, ok := lockCtx.Deadline(); ok {
			remaining = time.Until(deadline)
		}
		if remaining <= 0 {
			return out, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "inventory lock duration cap exceeded"}
		}
		out, err = d.applyInventoryBatchOnce(lockCtx, p, remaining, now)
		classified, retry, classifiedErr := classifySearchProjectionApplyResult(out, err)
		if classifiedErr != nil || !retry {
			return classified, classifiedErr
		}
		select {
		case <-lockCtx.Done():
			return out, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "inventory lock duration cap exceeded"}
		case <-time.After(min(5*time.Millisecond, remaining)):
		}
	}
}

func isSearchProjectionDeadline(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// reclaimSearchProjectionFTS runs FTS5's incremental-optimize protocol until
// the caller's reclaim budget is spent. The negative first step starts a
// merge; positive steps continue it. A bare positive step is deliberately not
// used because it may be a no-op.
//
// Each step commits on its own, so budget exhaustion is normal and costs
// nothing: every completed step is already durable and the next batch picks
// the merge up again. Nothing else shares these transactions, which is the
// point — an interrupted merge rolls its transaction back, and when that
// transaction also held the batch's cleanup deletes it discarded them.
//
//nolint:wrapcheck // The caller logs this: reclaim is maintenance and never fails a batch.
func reclaimSearchProjectionFTS(ctx context.Context, db *sql.DB, budget time.Duration) error {
	if budget <= 0 {
		return nil
	}
	deadline := time.Now().Add(budget)
	for step := 0; step < searchProjectionFTSReclaimStepCap; step++ {
		if time.Until(deadline) <= 0 {
			return nil
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		rollback := func() {
			_ = tx.Rollback()
		}
		var before, after int64
		if err = tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
			rollback()
			return err
		}
		mergePages := searchProjectionFTSMergePages
		if step == 0 {
			mergePages = -mergePages
		}
		if _, err = tx.ExecContext(ctx, reclaimSearchProjectionFTSSQL, mergePages); err != nil {
			rollback()
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil {
			rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		// A merge step is indivisible. It intentionally uses the caller's
		// context without the batch deadline: a step can overshoot the budget
		// by its own measured 1-3 ms, but cannot be cancelled mid-write. The
		// budget is therefore enforced between independently committed steps.
		if after-before < 2 {
			return nil
		}
	}
	return nil
}

// reclaimSearchProjectionFTSFn exists so tests can pin that reclaim is
// maintenance and cannot fail an already committed batch.
var reclaimSearchProjectionFTSFn = reclaimSearchProjectionFTS

//nolint:wrapcheck,errcheck // SQL errors preserve the typed inventory contract; rollback is best effort.
func (d *Database) applyInventoryBatchOnce(ctx context.Context, p apptypes.SearchProjectionInventoryPlan, lock time.Duration, now time.Time) (out apptypes.SearchProjectionProgress, err error) {
	lockCtx, cancel := context.WithTimeout(ctx, lock)
	defer cancel()
	db, err := d.open(lockCtx)
	if err != nil {
		return out, err
	}
	defer db.Close()
	tx, err := db.BeginTx(lockCtx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var revision int64
	var cursor, phase string
	var cursorStarted bool
	if err = tx.QueryRowContext(lockCtx, `SELECT s.source_revision,i.cursor,i.cursor_started,i.state FROM search_projection_state s JOIN search_projection_inventory_state i ON i.singleton=s.singleton WHERE s.generation_id=? AND s.state='rebuilding' AND i.generation_id=s.generation_id`, p.GenerationID).Scan(&revision, &cursor, &cursorStarted, &phase); err != nil {
		return out, err
	}
	var globalRevision int64
	if err = tx.QueryRowContext(lockCtx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&globalRevision); err != nil {
		return out, err
	}
	if phase != "rebuilding" || revision != p.ExpectedRevision || cursor != p.ExpectedCursor || cursorStarted != p.ExpectedCursorStarted {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if globalRevision != p.ExpectedRevision {
		var driftResult sql.Result
		if driftResult, err = tx.ExecContext(lockCtx, `UPDATE search_projection_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if rows, rowsErr := driftResult.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if driftResult, err = tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if rows, rowsErr := driftResult.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if driftResult, err = tx.ExecContext(lockCtx, `UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if rows, rowsErr := driftResult.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	for _, item := range p.Items {
		if !item.Missing {
			continue
		}
		if _, err = tx.ExecContext(lockCtx, `INSERT OR IGNORE INTO search_projection_source_sequence(event_id) SELECT id FROM events WHERE id=?`, item.EventID); err != nil {
			return out, err
		}
	}
	nextCursor := p.ExpectedCursor
	if p.NextCursor != "" {
		nextCursor = p.NextCursor
	}
	if p.Done {
		var result sql.Result
		if result, err = tx.ExecContext(lockCtx, `UPDATE search_projection_state SET high_water=(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence),checkpoint=0,phase='source',updated_at=? WHERE generation_id=? AND source_revision=? AND state='rebuilding'`, formatTimestamp(now), p.GenerationID, p.ExpectedRevision); err != nil {
			return out, err
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if result, err = tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_state SET cursor='',cursor_started=0,state='complete' WHERE singleton=1 AND generation_id=? AND cursor=? AND cursor_started=? AND state='rebuilding'`, p.GenerationID, p.ExpectedCursor, p.ExpectedCursorStarted); err != nil {
			return out, err
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if result, err = tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_compat SET requires_inventory=0 WHERE singleton=1 AND requires_inventory=1`); err != nil {
			return out, err
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if _, err = tx.ExecContext(lockCtx, `UPDATE search_projection_generation_lifecycle SET high_water=(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence) WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(lockCtx, `UPDATE literal_search_projection_state SET high_water=(SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence) WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, p.GenerationID); err != nil {
			return out, err
		}
	} else {
		result, updateErr := tx.ExecContext(lockCtx, `UPDATE search_projection_inventory_state SET cursor=?,cursor_started=? WHERE singleton=1 AND generation_id=? AND cursor=? AND cursor_started=? AND state='rebuilding'`, nextCursor, p.NextCursorStarted, p.GenerationID, p.ExpectedCursor, p.ExpectedCursorStarted)
		if updateErr != nil {
			return out, updateErr
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Selected = len(p.Items)
	for _, item := range p.Items {
		if item.Missing {
			out.Written++
		}
	}
	out.StoredBytes, out.WrittenBytes = p.Ledger.StoredBytes, p.Ledger.LogicalWriteBytes
	out.GenerationID = p.GenerationID
	return out, nil
}

// SelectSnapshot performs bounded reads and canonical hydration. Every read uses
// the caller's wall-time context; no transaction or lock is held.
//
//nolint:wrapcheck,errcheck // SQL and typed application errors cross this adapter unchanged.
func (d *Database) SelectSnapshot(ctx context.Context, b apptypes.SearchProjectionBudget, now time.Time) (out apptypes.ProjectionSnapshot, err error) {
	db, e := d.open(ctx)
	if e != nil {
		return out, e
	}
	defer db.Close()
	var state, cleanupScope string
	if e = db.QueryRowContext(ctx, `SELECT generation_id,config_hash,source_revision,high_water,checkpoint,state,phase,cleanup_scope,COALESCE(recent_cutoff_norm,''),recent_source_ceiling_bytes,recent_source_bytes FROM search_projection_state WHERE singleton=1`).Scan(&out.Generation.GenerationID, &out.Generation.ConfigHash, &out.Generation.SourceRevision, &out.Generation.HighWater, &out.Generation.Checkpoint, &state, &out.Phase, &cleanupScope, &out.RecentCutoffNorm, &out.RecentSourceCeilingBytes, &out.RecentSourceBytes); e != nil {
		return out, e
	}
	out.Now = now.UTC()
	out.CleanupAll = cleanupScope == "all"
	if state != "rebuilding" && (state != "drifted" || out.Phase != "cleanup") {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "no generation has been started"}
	}
	if out.Generation.ConfigHash != b.ConfigHash() {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "budget does not match generation configuration"}
	}
	var revision int64
	if e = db.QueryRowContext(ctx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&revision); e != nil {
		return out, e
	}
	if revision != out.Generation.SourceRevision && !out.CleanupAll {
		if e = markProjectionDrifted(ctx, db, out.Generation.GenerationID); e != nil {
			return out, e
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if out.Phase != "source" {
		return selectProjectionCleanup(ctx, db, out, b, now)
	}
	if out.RecentSourceCeilingBytes > 0 {
		expired, err := recentProjectionHasExpired(ctx, db, out.Generation.GenerationID, projectionCutoff(now.Add(-b.RecentAge)))
		if err != nil {
			return out, err
		}
		if out.RecentSourceBytes > out.RecentSourceCeilingBytes || expired {
			return selectProjectionEviction(ctx, db, out, b, now)
		}
	}
	rows, e := db.QueryContext(ctx, `SELECT q.sequence,COALESCE(e.id,q.event_id),COALESCE(e.session_id,''),COALESCE(e.created_at_norm,''),COALESCE(e.body_availability,''),e.id IS NULL,COALESCE(length(CAST(e.body AS BLOB)),0)+COALESCE(length(CAST(a.command_text AS BLOB)),0)+COALESCE(length(CAST(a.input_text AS BLOB)),0)+COALESCE(length(CAST(a.output_text AS BLOB)),0),CASE WHEN e.body_availability='available' THEN COALESCE(e.body_plaintext_bytes,e.body_stored_bytes,length(CAST(e.body AS BLOB)),0) ELSE 0 END+COALESCE(a.command_plaintext_bytes,length(CAST(a.command_text AS BLOB)),0)+COALESCE(a.input_plaintext_bytes,length(CAST(a.input_text AS BLOB)),0)+COALESCE(a.output_plaintext_bytes,length(CAST(a.output_text AS BLOB)),0) FROM search_projection_source_sequence q LEFT JOIN events e ON e.id=q.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE q.sequence>? AND q.sequence<=? ORDER BY q.sequence LIMIT ?`, out.Generation.Checkpoint, out.Generation.HighWater, b.Rows)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	var selection apptypes.BudgetLedger
	for rows.Next() {
		var x apptypes.ProjectionDocument
		var avail string
		if e = rows.Scan(&x.Sequence, &x.EventID, &x.SessionID, &x.CreatedAt, &avail, &x.Deleted, &x.StoredBytes, &x.DecodedBytes); e != nil {
			return out, e
		}
		disposition, admissionErr := selection.AdmitSource(b, x.StoredBytes, x.DecodedBytes)
		if admissionErr != nil {
			return out, admissionErr
		}
		if disposition == apptypes.ProjectionDispositionBatchFull {
			break
		}
		x.Disposition = disposition
		if disposition == apptypes.ProjectionDispositionExcluded {
			out.Documents = append(out.Documents, x)
			continue
		}
		if !x.Deleted {
			var body string
			if avail == "available" {
				plain, xerr := loadEventPlaintext(ctx, db, x.EventID)
				if xerr != nil {
					return out, xerr
				}
				body, _ = visibleEventBody(string(plain), domaintypes.BodyAvailabilityAvailable)
			}
			cmd, xerr := hydrateAuditPayload(ctx, db, x.EventID, "command")
			if xerr != nil {
				return out, xerr
			}
			in, xerr := hydrateAuditPayload(ctx, db, x.EventID, "input")
			if xerr != nil {
				return out, xerr
			}
			output, xerr := hydrateAuditPayload(ctx, db, x.EventID, "output")
			if xerr != nil {
				return out, xerr
			}
			parts := []string{}
			for _, v := range []string{body, cmd.String, in.String, output.String} {
				if v != "" {
					parts = append(parts, v)
				}
			}
			x.Text = strings.Join(parts, "\n")
			if cmd.Valid && cmd.String != "" {
				x.CommandCount = 1
			}
			_ = db.QueryRowContext(ctx, `SELECT COALESCE(failed,0) FROM command_audits WHERE event_id=?`, x.EventID).Scan(&x.FailureCount)
			_ = db.QueryRowContext(ctx, `SELECT summary_text FROM search_projection_session_summaries WHERE generation_id=? AND session_id=?`, out.Generation.GenerationID, x.SessionID).Scan(&x.PreviousSummary)
		}
		if x.Deleted {
			x.Disposition = apptypes.ProjectionDispositionDeleted
		}
		out.Documents = append(out.Documents, x)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	out.SourceDone = len(out.Documents) == 0 || out.Documents[len(out.Documents)-1].Sequence == out.Generation.HighWater
	return out, nil
}

//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func selectProjectionCleanup(ctx context.Context, db *sql.DB, out apptypes.ProjectionSnapshot, b apptypes.SearchProjectionBudget, now time.Time) (apptypes.ProjectionSnapshot, error) {
	var q string
	var args []any
	if out.Phase == "eviction" {
		return selectProjectionEviction(ctx, db, out, b, now)
	} else if out.CleanupAll {
		q = `SELECT 'recent',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(created_at_norm AS BLOB))+2*length(CAST(body_text AS BLOB))+32 FROM search_projection_recent_documents UNION ALL SELECT 'summary',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(summary_text AS BLOB))+24 FROM search_projection_session_summaries UNION ALL SELECT 'aggregate',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+16 FROM search_projection_command_aggregates UNION ALL SELECT 'keyword',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(keyword AS BLOB))+16 FROM search_projection_session_keywords UNION ALL SELECT 'fingerprint',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(fingerprint)+16 FROM literal_search_fingerprints UNION ALL SELECT 'exclusion',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(class AS BLOB))+32 FROM search_projection_exclusions LIMIT ?`
		args = []any{b.Rows + 1}
	} else {
		q = `SELECT 'recent',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(created_at_norm AS BLOB))+2*length(CAST(body_text AS BLOB))+32 FROM search_projection_recent_documents WHERE generation_id<>? UNION ALL SELECT 'summary',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(summary_text AS BLOB))+24 FROM search_projection_session_summaries WHERE generation_id<>? UNION ALL SELECT 'aggregate',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+16 FROM search_projection_command_aggregates WHERE generation_id<>? UNION ALL SELECT 'keyword',rowid,length(CAST(generation_id AS BLOB))+length(CAST(session_id AS BLOB))+length(CAST(keyword AS BLOB))+16 FROM search_projection_session_keywords WHERE generation_id<>? UNION ALL SELECT 'fingerprint',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(fingerprint)+16 FROM literal_search_fingerprints WHERE generation_id<>? UNION ALL SELECT 'exclusion',rowid,length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(CAST(class AS BLOB))+32 FROM search_projection_exclusions WHERE generation_id<>? LIMIT ?`
		args = []any{out.Generation.GenerationID, out.Generation.GenerationID, out.Generation.GenerationID, out.Generation.GenerationID, out.Generation.GenerationID, out.Generation.GenerationID, b.Rows + 1}
	}
	rows, e := db.QueryContext(ctx, q, args...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var c apptypes.ProjectionCleanupCandidate
		if out.Phase == "cleanup" {
			e = rows.Scan(&c.Class, &c.RowID, &c.LogicalBytes)
		} else {
			e = rows.Scan(&c.RowID, &c.LogicalBytes)
			c.Class = out.Phase
		}
		if e != nil {
			return out, e
		}
		out.Cleanup = append(out.Cleanup, c)
	}
	if len(out.Cleanup) <= b.Rows {
		out.CleanupDone = true
	} else {
		out.Cleanup = out.Cleanup[:b.Rows]
	}
	return out, rows.Err()
}

// ApplyBatch acquires the write lock with BEGIN IMMEDIATE under its own
// budget, then times only the held lock against lock. Persists writes,
// cleanup and phase/checkpoint advancement atomically.
func (d *Database) ApplyBatch(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (apptypes.SearchProjectionProgress, error) {
	if p.Phase != "source" {
		return apptypes.SearchProjectionProgress{}, errors.New("apply batch requires source phase")
	}
	return d.applyProjectionPlanWithRetry(ctx, p, lock, now)
}

// CleanupBatch applies only an application-planned eviction or old-generation batch.
func (d *Database) CleanupBatch(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (apptypes.SearchProjectionProgress, error) {
	if p.Phase == "source" {
		return apptypes.SearchProjectionProgress{}, errors.New("cleanup batch requires cleanup phase")
	}
	return d.applyProjectionPlanWithRetry(ctx, p, lock, now)
}

// applyProjectionPlanWithRetry fences concurrent workers at the persisted
// checkpoint. SQLite busy/snapshot conflicts are retried only inside the
// caller's lock cap; the eventual loser observes the winner's checkpoint and
// returns the domain drift error instead of leaking a driver error.
func (d *Database) applyProjectionPlanWithRetry(ctx context.Context, p apptypes.ProjectionBatchPlan, lock time.Duration, now time.Time) (apptypes.SearchProjectionProgress, error) {
	acquireDeadline := time.Now().Add(lock)
	for {
		remaining := time.Until(acquireDeadline)
		if remaining <= 0 {
			return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "projection lock acquisition exceeded"}
		}
		out, err := d.applyProjectionPlan(ctx, p, remaining, lock, now)
		var noProgress *apptypes.SearchProjectionNoProgressError
		if errors.As(err, &noProgress) && noProgress.Code == apptypes.SearchProjectionNoProgressRowWorkCap {
			return out, err
		}
		classified, retry, classifiedErr := classifySearchProjectionApplyResult(out, err)
		if classifiedErr != nil || !retry {
			return classified, classifiedErr
		}
		select {
		case <-ctx.Done():
			return apptypes.SearchProjectionProgress{}, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "projection lock acquisition exceeded"}
		case <-time.After(min(5*time.Millisecond, remaining)):
		}
	}
}

func isSearchProjectionSQLiteBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

func classifySearchProjectionApplyResult(out apptypes.SearchProjectionProgress, err error) (apptypes.SearchProjectionProgress, bool, error) {
	var noProgress *apptypes.SearchProjectionNoProgressError
	if errors.As(err, &noProgress) {
		return out, false, err
	}
	if isSearchProjectionDeadline(err) {
		return apptypes.SearchProjectionProgress{}, false, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "projection lock acquisition exceeded"}
	}
	if err == nil {
		return out, false, nil
	}
	if isSearchProjectionSQLiteBusy(err) {
		return out, true, nil
	}
	return out, false, err
}

// MarkFailed makes an oversize generation recoverable without advancing its checkpoint.
//
//nolint:wrapcheck,errcheck // Typed failure transition is preserved.
func (d *Database) MarkFailed(ctx context.Context, generation string, revision int64, class string, now time.Time) error {
	db, err := d.open(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE search_projection_state SET state='failed',phase='complete',failure_class=?,updated_at=? WHERE generation_id=? AND source_revision=? AND EXISTS(SELECT 1 FROM search_projection_source_revision WHERE singleton=1 AND revision=?)`, class, formatTimestamp(now), generation, revision, revision)
	if err != nil {
		return err
	}
	if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
		return &apptypes.SearchProjectionDriftError{}
	}
	r, err = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET state='failed' WHERE generation_id=? AND state='rebuilding'`, generation)
	if err != nil {
		return err
	}
	if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
		return &apptypes.SearchProjectionDriftError{}
	}
	return tx.Commit()
}

//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
func (d *Database) applyProjectionPlan(ctx context.Context, p apptypes.ProjectionBatchPlan, acquire, hold time.Duration, now time.Time) (out apptypes.SearchProjectionProgress, err error) {
	started := time.Now()
	db, e := d.open(ctx)
	if e != nil {
		return out, e
	}
	defer db.Close()
	acquireCtx, acquireCancel := context.WithTimeout(ctx, acquire)
	tx, e := beginImmediate(acquireCtx, db)
	acquireCancel()
	if e != nil {
		if isSearchProjectionDeadline(e) || isSearchProjectionSQLiteBusy(e) {
			return out, &apptypes.SearchProjectionNoProgressError{Code: apptypes.SearchProjectionNoProgressLockDurationCap, Reason: "projection lock acquisition exceeded"}
		}
		return out, e
	}
	defer func() { _ = tx.Rollback() }()
	holdDeadline := time.Now().Add(hold)
	holdOwnClock := true
	var holdCtx context.Context
	var holdCancel context.CancelFunc
	if parentDeadline, ok := ctx.Deadline(); ok && !parentDeadline.After(holdDeadline) {
		// Parent (usually the wall budget) expires first. Keep cancellation
		// but do not treat that deadline as a held-lock row-work overrun.
		holdOwnClock = false
		holdCtx, holdCancel = context.WithCancel(ctx)
	} else {
		holdCtx, holdCancel = context.WithDeadline(ctx, holdDeadline)
	}
	defer holdCancel()
	defer func() {
		err = rowWorkOrHoldError(p, hold, holdOwnClock, err)
	}()
	if d.afterProjectionLockHeld != nil {
		if hookErr := d.afterProjectionLockHeld(holdCtx); hookErr != nil {
			return out, hookErr
		}
	}
	var rev, checkpoint, globalRevision, recentSourceBytes int64
	var phase string
	if e = tx.QueryRowContext(holdCtx, `SELECT source_revision,checkpoint,phase,recent_source_bytes FROM search_projection_state WHERE generation_id=?`, p.GenerationID).Scan(&rev, &checkpoint, &phase, &recentSourceBytes); e != nil {
		return out, e
	}
	if rev != p.ExpectedRevision || checkpoint != p.ExpectedCheckpoint || phase != p.Phase {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if p.Phase == "source" && len(p.Cleanup) > 0 && recentSourceBytes != p.ExpectedRecentSourceBytes {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if e = tx.QueryRowContext(holdCtx, `SELECT revision FROM search_projection_source_revision WHERE singleton=1`).Scan(&globalRevision); e != nil {
		return out, e
	}
	if globalRevision != p.ExpectedRevision && !p.AllowRevisionDrift {
		var r sql.Result
		if r, e = tx.ExecContext(holdCtx, `UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE generation_id=? AND source_revision=?`, p.GenerationID, p.ExpectedRevision); e != nil {
			return out, e
		}
		if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if r, e = tx.ExecContext(holdCtx, `UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); e != nil {
			return out, e
		}
		if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if e = tx.Commit(); e != nil {
			return out, e
		}
		return out, &apptypes.SearchProjectionDriftError{}
	}
	for _, exclusion := range p.Exclusions {
		if e = insertSearchProjectionExclusion(holdCtx, tx, p.GenerationID, exclusion); e != nil {
			return out, e
		}
		out.Selected++
	}
	var recentDelta int64
	for _, w := range p.Writes {
		d := w.Document
		if !d.Deleted {
			if w.RetainRecent {
				// The recent FTS tokenizer is `trigram case_sensitive 1` and
				// queries are ASCII-folded before they are matched, so the
				// indexed text must be folded too — exactly as the legacy
				// event_search_documents writer does. Storing raw text here
				// makes every term containing an uppercase letter unfindable.
				// Folding is length-preserving, so decoded_bytes is unaffected.
				bodyText := lowerSearchASCII(d.Text)
				if _, e = tx.ExecContext(holdCtx, `INSERT INTO search_projection_recent_documents(generation_id,event_rowid,event_id,created_at_norm,body_text,decoded_bytes) VALUES(?,?,?,?,?,?)`, p.GenerationID, d.Sequence, d.EventID, d.CreatedAt, bodyText, len(bodyText)); e != nil {
					return out, e
				}
				recentDelta += int64(len([]byte(bodyText)))
			}
			if _, e = tx.ExecContext(holdCtx, `INSERT INTO search_projection_session_summaries VALUES(?,?,1,?,?,?) ON CONFLICT(generation_id,session_id) DO UPDATE SET event_count=event_count+1,summary_text=excluded.summary_text`, p.GenerationID, d.SessionID, w.Summary, searchProjectionVersion, searchProjectionSummaryVersion); e != nil {
				return out, e
			}
			if _, e = tx.ExecContext(holdCtx, `INSERT INTO search_projection_command_aggregates VALUES(?,?,?,?) ON CONFLICT(generation_id,session_id) DO UPDATE SET command_count=command_count+excluded.command_count,failure_count=failure_count+excluded.failure_count`, p.GenerationID, d.SessionID, d.CommandCount, d.FailureCount); e != nil {
				return out, e
			}
			for k, n := range w.Keywords {
				if _, e = tx.ExecContext(holdCtx, `INSERT INTO search_projection_session_keywords VALUES(?,?,?,?,?) ON CONFLICT(generation_id,session_id,keyword) DO UPDATE SET occurrences=occurrences+excluded.occurrences`, p.GenerationID, d.SessionID, k, n, searchProjectionKeywordVersion); e != nil {
					return out, e
				}
			}
			for _, fingerprint := range w.LiteralFingerprints {
				if _, e = tx.ExecContext(holdCtx, `INSERT OR IGNORE INTO literal_search_fingerprints(generation_id,source_sequence,event_id,fingerprint,fingerprint_version) VALUES(?,?,?,?,1)`, p.GenerationID, d.Sequence, d.EventID, []byte(fingerprint)); e != nil {
					return out, e
				}
			}
			out.Written++
		}
		out.Selected++
	}
	for _, c := range p.Cleanup {
		var table string
		switch c.Class {
		case "eviction", "recent":
			table = "search_projection_recent_documents"
		case "summary":
			table = "search_projection_session_summaries"
		case "aggregate":
			table = "search_projection_command_aggregates"
		case "keyword":
			table = "search_projection_session_keywords"
		case "fingerprint":
			table = "literal_search_fingerprints"
		case "exclusion":
			table = "search_projection_exclusions"
		default:
			continue
		}
		r, x := tx.ExecContext(holdCtx, `DELETE FROM `+table+` WHERE rowid=?`, c.RowID)
		if x != nil {
			return out, x
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
		if c.Class == "eviction" {
			recentDelta -= c.ReleasedSourceBytes
		}
		out.Evicted += int(n)
		out.Cleaned += int(n)
		out.CleanupBytes += c.LogicalBytes
	}
	next := p.Phase
	if p.NextPhase != "" {
		next = p.NextPhase
	}
	// Capacity re-derivation at source→eviction is deliberately *not* done
	// inside this write transaction: measureSearchProjectionFamilySplit walks
	// dbstat and SUM(decoded_bytes) under WithoutCancel, so a multi-second
	// hold would ignore the lock deadline entirely. Shell-hook writers share
	// this store; that is a real outage. See recordSearchProjectionCapacityRederivation.
	state := p.ContinueState
	if state == "" {
		state = "rebuilding"
	}
	if p.Completed {
		state = p.FinalState
		if state == "" {
			state = "complete"
		}
	}
	if p.Completed && state == "complete" {
		// Accept 'stale' as well as 'rebuilding': events recorded during a
		// rebuild flip literal state to stale via migration-039 triggers, so
		// requiring only 'rebuilding' left the row permanently incomplete on
		// live stores. Zero rows means this generation is not the one the
		// literal singleton is finishing — same drift fence as lifecycle.
		var literalResult sql.Result
		if literalResult, e = tx.ExecContext(holdCtx, `UPDATE literal_search_projection_state SET state='complete',updated_at=? WHERE singleton=1 AND generation_id=? AND state IN ('rebuilding','stale')`, formatTimestamp(now), p.GenerationID); e != nil {
			return out, e
		}
		if n, rowErr := literalResult.RowsAffected(); rowErr != nil || n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
	}
	if p.Completed && state == "complete" {
		var lifecycleResult sql.Result
		if lifecycleResult, e = tx.ExecContext(holdCtx, `UPDATE search_projection_generation_lifecycle SET state='complete' WHERE generation_id=? AND state='rebuilding'`, p.GenerationID); e != nil {
			return out, e
		}
		if n, rowErr := lifecycleResult.RowsAffected(); rowErr != nil || n != 1 {
			return out, &apptypes.SearchProjectionDriftError{}
		}
	}
	fenceRecent := p.Phase == "source" && len(p.Cleanup) > 0
	fenceRecentInt := 0
	if fenceRecent {
		fenceRecentInt = 1
	}
	r, e := tx.ExecContext(holdCtx, `UPDATE search_projection_state SET checkpoint=?,phase=?,state=?,recent_source_bytes=recent_source_bytes+?,active_generation_id=CASE WHEN ? AND ?='complete' THEN generation_id ELSE active_generation_id END,last_batch_milliseconds=?,updated_at=? WHERE generation_id=? AND source_revision=? AND checkpoint=? AND phase=? AND (? OR EXISTS(SELECT 1 FROM search_projection_source_revision WHERE singleton=1 AND revision=?)) AND (?=0 OR recent_source_bytes=?)`, p.NextCheckpoint, next, state, recentDelta, p.Completed, state, time.Since(started).Milliseconds(), formatTimestamp(now), p.GenerationID, p.ExpectedRevision, p.ExpectedCheckpoint, p.Phase, p.AllowRevisionDrift, p.ExpectedRevision, fenceRecentInt, p.ExpectedRecentSourceBytes)
	if e != nil {
		return out, e
	}
	if n, x := r.RowsAffected(); x != nil || n != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	if p.Phase != "source" {
		// Reclaim is maintenance after the batch commit. Strip the lock
		// deadline so an indivisible merge step cannot interrupt and roll back
		// its own transaction; the reclaim budget is checked between steps.
		if reclaimErr := reclaimSearchProjectionFTSFn(context.WithoutCancel(ctx), db, hold); reclaimErr != nil {
			slog.Debug("search projection FTS reclaim failed", "error", reclaimErr)
		}
	}
	// Re-derive the source ceiling after the source→eviction transition is
	// durable, in its own bounded write fenced on generation_id + phase so it
	// cannot stamp a generation that has moved on. Failure leaves the
	// Start-time ceiling in place — the previous generation's estimate, which
	// can err in either direction (a higher same-generation amplification
	// would correctly shrink the ceiling; a lower one would raise it). A
	// single eviction batch may therefore run against the Start-time ceiling
	// if re-derivation loses the race. index_family_within_budget on the
	// completion record is what surfaces whether the family ultimately held
	// the configured budget.
	//
	// Limitation (v0.34 follow-up): a crash between this commit and the
	// re-derivation means the re-derivation never retries — the transition is
	// the only trigger.
	if p.Phase == "source" && next == "eviction" {
		d.recordSearchProjectionCapacityRederivation(ctx, db, p.GenerationID, now)
	}
	// Cutover evidence is recorded after the completion is durable. Measuring
	// inside that transaction would put an unbounded dbstat walk under the lock
	// budget, and a walk that overran it rolled back a generation that had
	// otherwise finished — leaving the store to repeat the same final batch on
	// every open, forever.
	if p.Completed && state == "complete" {
		d.recordSearchProjectionCutoverEvidence(ctx, db, p.GenerationID, now)
	}
	out.Completed = p.Completed
	out.GenerationID = p.GenerationID
	out.StoredBytes = p.Ledger.StoredBytes
	out.DecodedBytes = p.Ledger.DecodedBytes
	out.WrittenBytes = p.Ledger.LogicalWriteBytes
	return out, nil
}

// rowWorkOrHoldError maps a hold-clock deadline to RowWorkCap. Acquisition
// failures never reach here. A single source write also carries the exclusion
// identity so the caller can persist a row_work skip without re-reading.
func rowWorkOrHoldError(p apptypes.ProjectionBatchPlan, hold time.Duration, holdOwnClock bool, err error) error {
	if err == nil {
		return nil
	}
	var noProgress *apptypes.SearchProjectionNoProgressError
	if errors.As(err, &noProgress) {
		return err
	}
	if !holdOwnClock || !isSearchProjectionDeadline(err) {
		return err
	}
	out := &apptypes.SearchProjectionNoProgressError{
		Code:   apptypes.SearchProjectionNoProgressRowWorkCap,
		Reason: "projection row work exceeded hold budget",
	}
	if p.Phase == "source" && len(p.Writes) == 1 && len(p.Exclusions) == 0 && len(p.Cleanup) == 0 {
		doc := p.Writes[0].Document
		out.Exclusion = apptypes.ProjectionExclusion{
			Sequence:      doc.Sequence,
			EventID:       doc.EventID,
			Class:         "row_work",
			MeasuredBytes: int64(hold),
			ByteLimit:     int64(hold),
		}
	}
	return out
}

// AbandonSearchProjection idempotently retires the current incomplete generation.
//
//nolint:wrapcheck,errcheck // Transaction errors remain adapter-owned and typed state errors pass through.
func (d *Database) AbandonSearchProjection(ctx context.Context, now time.Time) (out apptypes.SearchProjectionAbandonResult, err error) {
	db, e := d.open(ctx)
	if e != nil {
		return out, e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var generation, state, active string
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(generation_id,''),state,COALESCE(active_generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(&generation, &state, &active); e != nil {
		return out, e
	}
	out.GenerationID = generation
	out.State = "abandoned"
	if generation == "" {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "no derived generation exists"}
	}
	var lifecycle string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM search_projection_generation_lifecycle WHERE generation_id=?`, generation).Scan(&lifecycle); e == nil && lifecycle == "abandoned" {
		out.AlreadyAbandoned = true
		return out, tx.Commit()
	}
	if generation == active || state == "complete" {
		return out, &apptypes.SearchProjectionNoProgressError{Reason: "active complete generation cannot be abandoned"}
	}
	r, e := tx.ExecContext(ctx, `UPDATE search_projection_state SET state='failed',phase='complete',failure_class='abandoned',updated_at=? WHERE singleton=1 AND generation_id=? AND state<>'complete'`, formatTimestamp(now), generation)
	if e != nil {
		return out, e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if r, e = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET state='abandoned',abandoned_at=? WHERE generation_id=? AND state<>'complete'`, formatTimestamp(now), generation); e != nil {
		return out, e
	}
	if n, rowErr := r.RowsAffected(); rowErr != nil || n != 1 {
		return out, &apptypes.SearchProjectionDriftError{}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE literal_search_projection_state SET state='stale',updated_at=? WHERE singleton=1 AND generation_id=?`, formatTimestamp(now), generation); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM search_projection_exclusions WHERE generation_id=?`, generation); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

// SearchProjectionStatus returns payload-free operational evidence.
//
// statusQueryer is the read surface SearchProjectionStatus uses inside its
// generation snapshot. *sql.Tx and *sql.DB both satisfy it.
//
//nolint:wrapcheck,errcheck // SQL errors are contextual to this adapter.
type statusQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// SearchProjectionStatus reports persisted projection state and
// generation-scoped counters from one read snapshot. The dbstat
// physical-byte walk runs after that snapshot is released (#1839).
func (d *Database) SearchProjectionStatus(ctx context.Context) (s apptypes.SearchProjectionStatus, err error) {
	started := time.Now()
	db, e := d.openReadOnly(ctx)
	if e != nil {
		return s, e
	}
	defer func() { _ = db.Close() }()
	s.SchemaVersion = "traceary.search-projection-status/v1"
	s.KeywordVersion = searchProjectionKeywordVersion
	s.FingerprintVersion = 1
	// Generation-scoped fields share one read snapshot. The dbstat walk
	// below is outside this transaction: holding it here is not acceptable
	// on a large store (#1839). There is no selectSearchProjectionRecentRangeSQL
	// in tree; range-like fields on this object come from the state row
	// in the same snapshot (recent_cutoff_norm).
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return s, xerrors.Errorf("begin search projection status snapshot: %w", e)
	}
	defer func() { _ = tx.Rollback() }()
	var activeGenerationID, rebuildGenerationID string
	e = tx.QueryRowContext(ctx, `SELECT state,phase,projection_version,fts_design,config_hash,source_revision,high_water,checkpoint,state='complete',recent_age_seconds,index_family_byte_limit,recent_source_ceiling_bytes,recent_amplification_ppm,non_recent_family_bytes,COALESCE(recent_cutoff_norm,''),capacity_semantics_version,COALESCE(capacity_evidence_status,''),COALESCE(capacity_evidence_reason,''),index_family_within_budget,last_batch_milliseconds,CASE WHEN COALESCE(generation_id,'')='' THEN '' ELSE (SELECT state FROM search_projection_generation_lifecycle WHERE generation_id=search_projection_state.generation_id) END,COALESCE((SELECT abandoned_at FROM search_projection_generation_lifecycle WHERE generation_id=search_projection_state.generation_id),''),COALESCE(cutover_index_family,''),cutover_family_bytes_before,cutover_family_bytes_after,COALESCE(cutover_before_evidence_status,''),COALESCE(cutover_before_evidence_reason,''),COALESCE(cutover_after_evidence_status,''),COALESCE(cutover_after_evidence_reason,''),COALESCE(failure_class,''),COALESCE(active_generation_id,''),COALESCE(generation_id,'') FROM search_projection_state WHERE singleton=1`).Scan(
		&s.State, &s.Phase, &s.ProjectionVersion, &s.FTSDesign, &s.ConfigHash, &s.SourceRevision, &s.HighWater, &s.Checkpoint, &s.Completed,
		&s.RecentAgeSeconds, &s.IndexFamilyByteLimit, &s.RecentSourceCeilingBytes, &s.RecentAmplificationPPM, &s.NonRecentFamilyBytes,
		&s.RecentCutoffNorm, &s.CapacitySemanticsVersion, &s.CapacityEvidence.Status, &s.CapacityEvidence.Reason, &s.IndexFamilyWithinBudget,
		&s.LastBatchMilliseconds, &s.LifecycleState, &s.AbandonedAt, &s.CutoverIndexFamily, &s.CutoverFamilyBytesBefore, &s.CutoverFamilyBytesAfter,
		&s.CutoverBeforeEvidence.Status, &s.CutoverBeforeEvidence.Reason, &s.CutoverAfterEvidence.Status, &s.CutoverAfterEvidence.Reason, &s.FailureClass,
		&activeGenerationID, &rebuildGenerationID)
	if e != nil {
		return s, xerrors.Errorf("read search projection state: %w", e)
	}
	enrichCapacityEvidenceMethod(&s.CutoverBeforeEvidence, &s.CutoverAfterEvidence, &s.CapacityEvidence)
	var inventoryState string
	if e = tx.QueryRowContext(ctx, `SELECT state FROM search_projection_inventory_state WHERE singleton=1`).Scan(&inventoryState); e != nil {
		return s, xerrors.Errorf("read search projection inventory state: %w", e)
	}
	if s.State == "rebuilding" && inventoryState == "rebuilding" {
		s.Phase = "inventory"
	}
	if hook := d.afterStatusGenerationScopeRead; hook != nil {
		hook()
	}
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(decoded_bytes),0),COUNT(*) FROM search_projection_recent_documents WHERE generation_id=?`, activeGenerationID).Scan(&s.RecentBytes, &s.RecentDocuments); e != nil {
		return s, xerrors.Errorf("count recent documents: %w", e)
	}
	if e = measureRecentSourceBytesCache(ctx, tx, rebuildGenerationID, &s); e != nil {
		return s, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(summary_text AS BLOB))),0) FROM search_projection_session_summaries WHERE generation_id=?`, activeGenerationID).Scan(&s.SummarySessions, &s.SummaryLogicalBytes); e != nil {
		return s, xerrors.Errorf("count session summaries: %w", e)
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(keyword AS BLOB))),0) FROM search_projection_session_keywords WHERE generation_id=?`, activeGenerationID).Scan(&s.KeywordRows, &s.KeywordLogicalBytes); e != nil {
		return s, xerrors.Errorf("count session keywords: %w", e)
	}
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(generation_id AS BLOB))+length(CAST(event_id AS BLOB))+length(fingerprint)+16),0) FROM literal_search_fingerprints WHERE generation_id=?`, activeGenerationID).Scan(&s.FingerprintRows, &s.FingerprintLogicalBytes); e != nil {
		return s, xerrors.Errorf("count fingerprints: %w", e)
	}
	if e = measureSearchProjectionExclusions(ctx, tx, rebuildGenerationID, &s); e != nil {
		return s, e
	}
	if e = tx.Commit(); e != nil {
		return s, xerrors.Errorf("commit search projection status snapshot: %w", e)
	}
	if e = db.QueryRowContext(ctx, selectSearchProjectionFTSLogicalBytesSQL).Scan(&s.FTSLogicalBytes); e != nil {
		return s, xerrors.Errorf("measure fts logical bytes: %w", e)
	}
	probeStarted := time.Now()
	var ignored int
	if probeErr := db.QueryRowContext(ctx, `SELECT count(*) FROM search_projection_recent_fts WHERE search_projection_recent_fts MATCH 'traceary_projection_probe_no_payload_7f42'`).Scan(&ignored); probeErr != nil {
		return s, xerrors.Errorf("probe search projection fts: %w", probeErr)
	}
	s.MatchProbeMilliseconds = time.Since(probeStarted).Milliseconds()
	var page int64
	if db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&page) == nil {
		if e = db.QueryRowContext(ctx, selectSearchProjectionFamilyTotalSQL).Scan(&s.PhysicalBytes); e == nil {
			s.PhysicalEvidence = apptypes.CapacityEvidence{Status: "complete", Method: "dbstat"}
		} else {
			s.PhysicalEvidence = apptypes.CapacityEvidence{Status: "unavailable", Method: "pragma", Reason: "dbstat unavailable"}
			e = nil
		}
	}
	s.InspectionMilliseconds = time.Since(started).Milliseconds()
	return s, e
}

// measureRecentSourceBytesCache compares the eviction cache to SUM(decoded_bytes)
// for the generation the cache is written against (state.generation_id). That
// is not active_generation_id during a rebuild. Status never rewrites the cache.
func measureRecentSourceBytesCache(ctx context.Context, queryer statusQueryer, generationID string, s *apptypes.SearchProjectionStatus) error {
	if err := queryer.QueryRowContext(ctx, `SELECT recent_source_bytes FROM search_projection_state WHERE singleton=1`).Scan(&s.RecentSourceBytes); err != nil {
		return xerrors.Errorf("read recent_source_bytes cache: %w", err)
	}
	if err := queryer.QueryRowContext(ctx, `SELECT COALESCE(SUM(decoded_bytes),0) FROM search_projection_recent_documents WHERE generation_id=?`, generationID).Scan(&s.RecentSourceBytesMeasured); err != nil {
		return xerrors.Errorf("sum recent_source_bytes measured: %w", err)
	}
	s.RecentSourceBytesDelta = s.RecentSourceBytes - s.RecentSourceBytesMeasured
	s.RecentSourceBytesEvidence = apptypes.CapacityEvidence{Status: "complete", Method: "sum"}
	return nil
}

// searchProjectionMeasureTimeout bounds the cutover evidence measurement. The
// dbstat walk costs in proportion to the projection family's own page count —
// measured at 1.44s for an ~880MiB family — so it must never share a deadline
// with the transaction that publishes a completed generation. Exceeding this
// yields unavailable evidence, never a failed generation.
const searchProjectionMeasureTimeout = 3 * time.Second

// searchProjectionEvidenceWriteTimeout is the extra allowance the detached
// evidence context gets for its single-row write, on top of the walk.
const searchProjectionEvidenceWriteTimeout = time.Second

func (d *Database) measureTimeout() time.Duration {
	if d.searchProjectionMeasureTimeoutOverride > 0 {
		return d.searchProjectionMeasureTimeoutOverride
	}
	return searchProjectionMeasureTimeout
}

// measureSearchProjectionFamilyBytes returns total dbstat physical bytes for
// the bounded search projection family (recent + scoped non-recent + shared
// non-recent). Call sites that need the split use measureSearchProjectionFamilySplit.
func (d *Database) measureSearchProjectionFamilyBytes(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, apptypes.CapacityEvidence) {
	recent, scoped, shared, evidence := d.measureSearchProjectionFamilySplit(ctx, q)
	return recent + scoped + shared, evidence
}

// measureSearchProjectionFamilySplit returns recent-tier, generation-scoped
// non-recent, and permanent shared non-recent active b-tree allocation for the
// bounded search index family. Recent classification uses name and tbl_name
// with GLOB so FTS shadow tables and recent indexes land on the recent side.
// Scoped is an explicit tbl_name set so a future table is shared (conservative)
// rather than silently apportioned. Never returns an error: unmeasurable walks
// become unavailable evidence.
//
// Limitation (v0.34 follow-up): the dbstat walk and a later SUM(decoded_bytes)
// are not a consistent snapshot, so a concurrent eviction can record a wildly
// high amplification.
func (d *Database) measureSearchProjectionFamilySplit(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (recentBytes, nonRecentScoped, nonRecentShared int64, evidence apptypes.CapacityEvidence) {
	timeout := d.measureTimeout()
	// WithoutCancel deliberately detaches from the caller's deadline: the batch
	// context is sized for the write lock, and evidence must be measurable on
	// its own terms or reported unavailable — never able to fail the batch.
	measureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	err := q.QueryRowContext(measureCtx, measureSearchProjectionFamilySplitSQL).Scan(&recentBytes, &nonRecentScoped, &nonRecentShared)
	if err != nil {
		return 0, 0, 0, apptypes.CapacityEvidence{
			Status: searchProjectionEvidenceUnavailable,
			Method: "dbstat",
			Reason: truncateEvidenceReason(err.Error()),
		}
	}
	return recentBytes, nonRecentScoped, nonRecentShared, apptypes.CapacityEvidence{Status: searchProjectionEvidenceMeasured, Method: "dbstat"}
}

// deriveSearchProjectionCapacity converts the index-family budget into a
// source-text ceiling using a measured (or fallback) amplification ratio and a
// non-recent reserve (permanent shared + generation-scoped apportionment).
//
// Amplification sample is deliberately unscoped: recentPhysical /
// SUM(decoded_bytes) must cover the same physical/logical rows. Limitation
// (v0.34 follow-up): amplification is measured over all generations present,
// not the new one alone, so it is a blend of outgoing and incoming layouts.
// The scoped part of the reserve is apportioned to reserveGenerationID — the
// generation that will survive — so a rebuild does not double-count the
// outgoing generation's session-tier pages. Empty reserveGenerationID scopes
// the apportionment to the whole family. Permanent shared pages are never
// discounted by the generation ratio.
//
// A reserve at or above the budget yields SourceCeiling 0 (empty recent tier),
// never a hard failure: completing a rebuild is what shrinks the family, so
// refusing Start would deadlock.
func (d *Database) deriveSearchProjectionCapacity(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, budget, lastNonRecent int64, reserveGenerationID string) capacityDerivation {
	out := capacityDerivation{}
	recent, nonRecentScoped, nonRecentShared, splitEvidence := d.measureSearchProjectionFamilySplit(ctx, q)
	out.RecentBytes = recent
	out.NonRecentScoped = nonRecentScoped
	out.NonRecentShared = nonRecentShared
	out.NonRecentPhysical = nonRecentScoped + nonRecentShared
	out.SplitEvidence = splitEvidence
	// Amplification sample is unscoped: numerator and denominator must cover
	// the same rows across every generation present in the recent tier.
	_ = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(decoded_bytes),0) FROM search_projection_recent_documents`).Scan(&out.SampleSourceBytes)

	useFallback := splitEvidence.Status != searchProjectionEvidenceMeasured ||
		out.SampleSourceBytes < searchProjectionMinAmplificationSampleBytes ||
		out.RecentBytes <= 0
	if useFallback {
		out.AmplificationPPM = searchProjectionFallbackAmplificationPPM
		if splitEvidence.Status != searchProjectionEvidenceMeasured {
			// Keep the split's unavailable reason; non-recent falls back to
			// last persisted measured value (0 on a fresh store).
			out.Evidence = splitEvidence
			out.NonRecentBytes = lastNonRecent
		} else if out.SampleSourceBytes < searchProjectionMinAmplificationSampleBytes {
			out.Evidence = apptypes.CapacityEvidence{
				Status: searchProjectionEvidenceUnavailable,
				Method: "dbstat",
				Reason: "amplification sample below minimum",
			}
			out.NonRecentBytes = d.scopedNonRecentReserve(ctx, q, nonRecentScoped, nonRecentShared, reserveGenerationID)
		} else {
			out.Evidence = apptypes.CapacityEvidence{
				Status: searchProjectionEvidenceUnavailable,
				Method: "dbstat",
				Reason: "recent family empty; amplification unmeasurable",
			}
			out.NonRecentBytes = d.scopedNonRecentReserve(ctx, q, nonRecentScoped, nonRecentShared, reserveGenerationID)
		}
	} else {
		out.AmplificationPPM = mulDiv(out.RecentBytes, 1_000_000, out.SampleSourceBytes)
		if out.AmplificationPPM < 1_000_000 {
			out.AmplificationPPM = 1_000_000
		}
		out.Evidence = apptypes.CapacityEvidence{Status: searchProjectionEvidenceMeasured, Method: "dbstat"}
		out.NonRecentBytes = d.scopedNonRecentReserve(ctx, q, nonRecentScoped, nonRecentShared, reserveGenerationID)
	}
	if out.NonRecentBytes >= budget {
		// Permanent tiers that alone exceed the budget mean the recent tier
		// must be empty — the correct outcome, not a failure. Ceiling 0
		// empties every recent row (see ProjectionSnapshot docs).
		out.SourceCeiling = 0
		reason := "non-recent reserve at or above index-family budget"
		out.Evidence = apptypes.CapacityEvidence{
			Status: searchProjectionEvidenceUnavailable,
			Method: "dbstat",
			Reason: foldEvidenceReason(out.Evidence.Reason, reason),
		}
		slog.Warn("search projection non-recent reserve exhausts index-family budget; recent tier ceiling is 0",
			"non_recent_family_bytes", out.NonRecentBytes,
			"index_family_byte_limit", budget,
		)
		return out
	}
	out.SourceCeiling = mulDiv(budget-out.NonRecentBytes, 1_000_000, out.AmplificationPPM)
	if out.SourceCeiling < 0 {
		out.SourceCeiling = 0
	}
	return out
}

// scopedNonRecentReserve builds the non-recent reserve used for the source
// ceiling: permanent shared pages in full, plus generation-scoped pages
// apportioned as scoped * logical(target) / logical(all). Shared is never
// discounted by the generation ratio — those objects are not reclaimed by
// cleanup. Denominator 0 → treat the whole scoped figure as belonging to the
// target (shared is still added in full).
func (d *Database) scopedNonRecentReserve(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, nonRecentScoped, nonRecentShared int64, reserveGenerationID string) int64 {
	if nonRecentScoped < 0 {
		nonRecentScoped = 0
	}
	if nonRecentShared < 0 {
		nonRecentShared = 0
	}
	if nonRecentScoped == 0 {
		return nonRecentShared
	}
	var logicalTarget, logicalAll int64
	_ = q.QueryRowContext(ctx, selectSearchProjectionLogicalNonRecentBytesSQL, reserveGenerationID).Scan(&logicalTarget)
	_ = q.QueryRowContext(ctx, selectSearchProjectionLogicalNonRecentBytesSQL, "").Scan(&logicalAll)
	if logicalAll <= 0 {
		return nonRecentShared + nonRecentScoped
	}
	return nonRecentShared + mulDiv(nonRecentScoped, logicalTarget, logicalAll)
}

// deriveSearchProjectionRecentCutoff finds the created_at_norm at which the
// corpus walked newest-first crosses a loosened walk ceiling derived from
// sourceCeiling. The prefilter is a build-cost bound, not an enforcement
// mechanism: its byte unit (body_plaintext_bytes / stored envelopes) differs
// from decoded_bytes in both directions (thinking blocks inflate the
// prefilter; audit payloads can go the other way). Eviction enforces the
// exact ceiling. Empty cutoff with empty reason means the whole corpus fits
// under the walk ceiling (sql.ErrNoRows). Empty cutoff with a non-empty
// reason means the prefilter did not run (timeout or error) and the caller
// must fold the reason into capacity evidence.
//
// A ceiling of 0 returns searchProjectionCutoffRetainNothing instead of an
// empty cutoff. Empty means "everything qualifies", which at ceiling 0 would
// build the whole age window into the trigram index and then evict every row
// of it — maximum build cost to retain nothing, on exactly the stores already
// at their limit.
//
// Uses WithTimeout on the caller's context so a cancelled store open stops the
// walk; the timeout is the upper bound.
func (d *Database) deriveSearchProjectionRecentCutoff(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceCeiling int64) (cutoff string, reason string) {
	if sourceCeiling <= 0 {
		return searchProjectionCutoffRetainNothing, ""
	}
	// Walk against sourceCeiling * slack so systematic prefilter over-count
	// (thinking-heavy Claude Code transcripts) does not irreversibly shrink
	// the recent window below what the true ceiling allows.
	walkCeiling := mulDiv(sourceCeiling, searchProjectionCutoffSlackFactor, 1)
	cutoffCtx, cancel := context.WithTimeout(ctx, searchProjectionCutoffTimeout)
	defer cancel()
	err := q.QueryRowContext(cutoffCtx, selectSearchProjectionRecentCutoffSQL, walkCeiling).Scan(&cutoff)
	if err == nil {
		return cutoff, ""
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", ""
	}
	return "", "recent cutoff: " + truncateEvidenceReason(err.Error())
}

// foldEvidenceReason appends an additional capacity reason without inventing
// punctuation when the primary reason is empty.
func foldEvidenceReason(primary, additional string) string {
	switch {
	case additional == "":
		return primary
	case primary == "":
		return additional
	default:
		return primary + "; " + additional
	}
}

// recordSearchProjectionCapacityRederivation re-measures this generation's
// family and rewrites the source ceiling after the source→eviction transition
// is durable. Mirrors recordSearchProjectionCutoverEvidence: own bounded
// context, fenced write, log-and-leave-Start-ceiling on failure.
//
// The Start-time ceiling is the previous generation's estimate and can err in
// either direction; a single eviction batch may run against it if this loses
// the race with the next open. index_family_within_budget on the completion
// record surfaces whether the configured budget held.
func (d *Database) recordSearchProjectionCapacityRederivation(ctx context.Context, db *sql.DB, generationID string, now time.Time) {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.measureTimeout()+searchProjectionEvidenceWriteTimeout)
	defer cancel()
	var budget, lastNonRecent int64
	// Fence the read on phase='eviction' so a generation that has already
	// moved on is left alone (no Start-time figures to overwrite either).
	if err := db.QueryRowContext(recordCtx, `SELECT index_family_byte_limit,non_recent_family_bytes FROM search_projection_state WHERE generation_id=? AND phase='eviction'`, generationID).Scan(&budget, &lastNonRecent); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.Warn("search projection capacity re-derivation skipped; Start-time ceiling remains",
				"generation_id", generationID,
				"reason", truncateEvidenceReason(err.Error()),
			)
		}
		return
	}
	// At the transition the generation being built is the one whose rows just
	// filled the recent tier — scope the reserve to it.
	derivation := d.deriveSearchProjectionCapacity(recordCtx, db, budget, lastNonRecent, generationID)
	if derivation.Evidence.Status == searchProjectionEvidenceUnavailable {
		slog.Warn("search projection capacity re-derivation used fallback estimate",
			"generation_id", generationID,
			"reason", derivation.Evidence.Reason,
			"amplification_ppm", derivation.AmplificationPPM,
			"source_ceiling_bytes", derivation.SourceCeiling,
			"non_recent_family_bytes", derivation.NonRecentBytes,
		)
	}
	result, err := db.ExecContext(recordCtx, `
		UPDATE search_projection_state
		   SET recent_source_ceiling_bytes = ?,
		       recent_amplification_ppm = ?,
		       non_recent_family_bytes = ?,
		       capacity_evidence_status = ?,
		       capacity_evidence_reason = ?,
		       updated_at = ?
		 WHERE generation_id = ? AND phase = 'eviction'`,
		derivation.SourceCeiling, derivation.AmplificationPPM, derivation.NonRecentBytes,
		derivation.Evidence.Status, derivation.Evidence.Reason,
		formatTimestamp(now), generationID,
	)
	if err != nil {
		slog.Warn("search projection capacity re-derivation not recorded; Start-time ceiling remains",
			"generation_id", generationID,
			"reason", truncateEvidenceReason(err.Error()),
		)
		return
	}
	// A concurrent worker advancing the phase makes the fenced UPDATE a
	// silent no-op; surface that so operators are not left thinking the
	// re-measure landed.
	if n, rowErr := result.RowsAffected(); rowErr != nil || n == 0 {
		reason := "fenced update matched no row"
		if rowErr != nil {
			reason = truncateEvidenceReason(rowErr.Error())
		}
		slog.Warn("search projection capacity re-derivation not recorded; Start-time ceiling remains",
			"generation_id", generationID,
			"reason", reason,
		)
	}
}

const (
	searchProjectionEvidenceMeasured    = "measured"
	searchProjectionEvidenceUnavailable = "unavailable"
	maxEvidenceReasonBytes              = 200
)

// recordSearchProjectionCutoverEvidence measures the bounded family and stores
// the result against an already-durable completion. Failure here loses a
// diagnostic figure and nothing else, so it is logged rather than returned —
// the generation is complete either way.
//
// Both the walk and the write run on a context detached from the batch. The
// batch context is sized for one bounded unit of work (one second by default)
// and is routinely near expiry by the time a generation completes; a write that
// inherited it would fail exactly when the walk was slow enough to matter,
// leaving an unrecorded figure sitting at zero.
//
// Limitation (v0.34 follow-up): there is no corrective path when a completed
// generation is recorded over budget (index_family_within_budget=0); the next
// CatchUp returns already_complete and never corrects it.
func (d *Database) recordSearchProjectionCutoverEvidence(ctx context.Context, db *sql.DB, generationID string, now time.Time) {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.measureTimeout()+searchProjectionEvidenceWriteTimeout)
	defer cancel()
	bytes, evidence := d.measureSearchProjectionFamilyBytes(recordCtx, db)
	withinBudget := -1
	var budget int64
	if budgetErr := db.QueryRowContext(recordCtx, `SELECT index_family_byte_limit FROM search_projection_state WHERE singleton=1 AND generation_id=?`, generationID).Scan(&budget); budgetErr == nil && evidence.Status == searchProjectionEvidenceMeasured {
		if bytes <= budget {
			withinBudget = 1
		} else {
			withinBudget = 0
			slog.Warn("search projection index family exceeds configured budget after completion",
				"generation_id", generationID,
				"family_bytes", bytes,
				"index_family_byte_limit", budget,
			)
		}
	}
	// Fenced on generation_id as well as active_generation_id: another process
	// may have started a replacement generation since the commit, which moves
	// generation_id on but leaves active_generation_id pointing at this one.
	// Matching on the active pointer alone would overwrite the new generation's
	// before-evidence with this generation's after-evidence.
	if _, err := db.ExecContext(recordCtx, `
		UPDATE search_projection_state
		   SET cutover_family_bytes_after = ?,
		       cutover_after_evidence_status = ?,
		       cutover_after_evidence_reason = ?,
		       index_family_within_budget = ?,
		       updated_at = ?
		 WHERE singleton = 1 AND active_generation_id = ? AND generation_id = ?`,
		bytes, evidence.Status, evidence.Reason, withinBudget, formatTimestamp(now), generationID, generationID,
	); err != nil {
		slog.Warn("search projection cutover evidence not recorded; generation is complete regardless",
			"generation_id", generationID,
			"evidence_status", evidence.Status,
			"error", err,
		)
		return
	}
	if evidence.Status == searchProjectionEvidenceUnavailable {
		slog.Warn("search projection cutover evidence unavailable; family bytes after cutover are not reportable",
			"generation_id", generationID,
			"method", evidence.Method,
			"reason", evidence.Reason,
		)
	}
}

// truncateEvidenceReason keeps a diagnostic reason bounded so a pathological
// driver message cannot bloat the state row.
func truncateEvidenceReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) <= maxEvidenceReasonBytes {
		return trimmed
	}
	return trimmed[:maxEvidenceReasonBytes]
}

// VerifySearchProjectionSessionTier runs a real session-tier query against the
// generation under construction (not necessarily active_generation_id) and
// requires it to return the expected session. This is the pre-cleanup gate:
// reclaiming the previous generation is only safe once the new session tier
// answers. An empty generation (no joinable summaries) passes vacuously.
//
//nolint:wrapcheck // Typed projection errors cross this adapter unchanged.
func (d *Database) VerifySearchProjectionSessionTier(ctx context.Context, generationID string) error {
	if strings.TrimSpace(generationID) == "" {
		return &apptypes.SearchProjectionNoProgressError{Reason: "session tier verification requires a generation id"}
	}
	db, err := d.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return verifySearchProjectionSessionTier(ctx, db, generationID)
}

func verifySearchProjectionSessionTier(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, generationID string) error {
	var sessionID, summary string
	err := q.QueryRowContext(ctx, `
		SELECT sum.session_id, sum.summary_text
		  FROM search_projection_session_summaries sum
		  JOIN sessions s ON s.session_id = sum.session_id
		 WHERE sum.generation_id = ?
		   AND length(trim(sum.summary_text)) >= 3
		 ORDER BY sum.session_id
		 LIMIT 1`,
		generationID,
	).Scan(&sessionID, &summary)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No joinable session summary: either the corpus is empty or only
			// orphan events exist. Session tier has nothing to answer; do not
			// block reclaim of an empty previous generation.
			return nil
		}
		return xerrors.Errorf("select session tier verification candidate: %w", err)
	}
	term := sessionTierVerificationTerm(summary)
	if term == "" {
		return &apptypes.SearchProjectionNoProgressError{Reason: "session tier verification could not derive a query term"}
	}
	// Same contract as queryProjectionSessionHits: keyword equality or summary
	// LIKE, pinned to the generation under construction and scoped to the
	// probed session. The scope matters: sessions in one workspace share
	// vocabulary, so a term drawn from one summary routinely matches others.
	// The question this gate asks is whether the tier can answer for a session
	// it holds a summary for — not whether that session outranks its peers.
	// Ranking is a search concern, and demanding the top slot here would fail
	// every store whose summaries share a word.
	keyword := foldSearchASCII(term)
	likeQuery := "%" + escapeLikeQuery(term) + "%"
	var hitSession string
	err = q.QueryRowContext(ctx, `
		SELECT sum.session_id
		  FROM search_projection_session_summaries sum
		  JOIN sessions s ON s.session_id = sum.session_id
		 WHERE sum.generation_id = ?
		   AND sum.session_id = ?
		   AND (
		         EXISTS (
		           SELECT 1
		             FROM search_projection_session_keywords k
		            WHERE k.generation_id = sum.generation_id
		              AND k.session_id = sum.session_id
		              AND k.keyword = ?
		         )
		         OR sum.summary_text LIKE ? ESCAPE '\'
		       )
		 ORDER BY ts_norm(s.started_at) DESC, s.session_id DESC
		 LIMIT 1`,
		generationID, sessionID, keyword, likeQuery,
	).Scan(&hitSession)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &apptypes.SearchProjectionNoProgressError{
				Reason: "session tier verification query returned no hits for generation " + generationID,
			}
		}
		return xerrors.Errorf("run session tier verification query: %w", err)
	}
	if hitSession != sessionID {
		return &apptypes.SearchProjectionNoProgressError{
			Reason: "session tier verification returned unexpected session " + hitSession,
		}
	}
	return nil
}

// sessionTierVerificationTerm picks a stable ≥3-rune token from a summary so
// the verification query exercises the same LIKE path production search uses.
func sessionTierVerificationTerm(summary string) string {
	fields := strings.Fields(summary)
	for _, field := range fields {
		runes := []rune(strings.TrimSpace(field))
		if len(runes) >= 3 {
			return string(runes)
		}
	}
	runes := []rune(strings.TrimSpace(summary))
	if len(runes) < 3 {
		return ""
	}
	if len(runes) > 32 {
		runes = runes[:32]
	}
	return string(runes)
}

func projectionCutoff(timestamp time.Time) string {
	return timestamp.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

//nolint:wrapcheck,errcheck // SQL errors remain inside the SQLite adapter; rollback is best effort.
func markProjectionDrifted(ctx context.Context, db *sql.DB, generation string) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE search_projection_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, generation)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return &apptypes.SearchProjectionNoProgressError{Reason: "generation state changed concurrently"}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE search_projection_inventory_state SET state='drifted' WHERE singleton=1 AND generation_id=? AND state='rebuilding'`, generation); e != nil {
		return e
	}
	r, e = tx.ExecContext(ctx, `UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=? AND state='rebuilding'`, generation)
	if e != nil {
		return e
	}
	n, e = r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return &apptypes.SearchProjectionNoProgressError{Reason: "generation lifecycle changed concurrently"}
	}
	return tx.Commit()
}
