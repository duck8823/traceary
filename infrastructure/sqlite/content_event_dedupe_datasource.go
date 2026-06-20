package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

// contentEventDedupeProximityWindow bounds how close in time two identity-
// matching prompt/transcript rows must be to count as a likely hook double-write
// rather than a deliberate repeat. It mirrors the content-event-reliability
// doctor diagnostic's window (presentation/cli.contentEventDuplicateProximityWindow)
// so the maintenance command and the diagnostic agree on what "near-simultaneous"
// means. Strict mode ignores this window and treats every exact duplicate group
// as one cluster.
const contentEventDedupeProximityWindow = 10 * time.Second

const (
	contentEventDedupeReasonProximity = "near-simultaneous hook duplicate"
	contentEventDedupeReasonStrict    = "strict exact duplicate"
	contentEventDedupeReasonMalformed = "skipped: malformed or unparseable created_at"
)

// dedupeCandidateRow is one eligible hook content event read from the store.
type dedupeCandidateRow struct {
	id         string
	kind       string
	client     string
	agent      string
	sessionID  string
	workspace  string
	body       string // original body, preserved verbatim for archive/restore
	createdAt  string // original RFC3339Nano text, preserved verbatim
	sourceHook sql.NullString
	parsedAt   time.Time
	parseOK    bool
}

// dedupeGroupKey is the duplicate-identity tuple. It intentionally matches the
// content-event-reliability doctor diagnostic — kind, client, agent, session_id,
// workspace, source_hook, and the whitespace-trimmed body — so the maintenance
// command and the diagnostic agree on what counts as a duplicate. This is
// deliberately NOT the same identity as the write-side guard
// (isDedupEligibleHookContentEvent / hookContentEventDuplicateExists), which
// compares the exact body without trimming; docs/changelog call out that the
// reversible dedupe identity follows the diagnostic, not the write-side guard.
type dedupeGroupKey struct {
	kind       string
	client     string
	agent      string
	sessionID  string
	workspace  string
	sourceHook string
	normBody   string
}

func newDedupeGroupKey(row dedupeCandidateRow) dedupeGroupKey {
	return dedupeGroupKey{
		kind:       row.kind,
		client:     row.client,
		agent:      row.agent,
		sessionID:  row.sessionID,
		workspace:  row.workspace,
		sourceHook: row.sourceHook.String,
		// Same normalization as the doctor diagnostic: trim surrounding
		// whitespace only, so trailing-newline noise does not split a pair, but
		// genuinely different prompts stay distinct.
		normBody: strings.TrimSpace(row.body),
	}
}

// forensicKey renders a stable, compact identity string for archive metadata.
// The body is hashed so the key stays bounded regardless of body size.
func (k dedupeGroupKey) forensicKey() string {
	sum := sha256.Sum256([]byte(k.normBody))
	hook := k.sourceHook
	if hook == "" {
		hook = "-"
	}
	return strings.Join([]string{
		k.kind, k.client, k.agent, k.sessionID, k.workspace, hook,
		"body:" + hex.EncodeToString(sum[:8]),
	}, "|")
}

// DedupeContentEvents reports (dry-run) or quarantines (apply) historical hook-
// originated prompt/transcript duplicate rows. It targets only events with
// client='hook' and kind in ('prompt','transcript'); command audits are never
// touched. Apply is transactionally safe and idempotent: a second apply finds no
// duplicates left in events for an already-cleaned group, so it moves nothing.
func (d *StoreManagementDatasource) DedupeContentEvents(
	ctx context.Context,
	params apptypes.ContentEventDedupeParams,
) (apptypes.ContentEventDedupeResult, error) {
	if params.Apply {
		if strings.TrimSpace(params.RunID) == "" {
			return apptypes.ContentEventDedupeResult{}, xerrors.Errorf("apply requires a non-empty dedupe run id")
		}
		if params.Now.IsZero() {
			return apptypes.ContentEventDedupeResult{}, xerrors.Errorf("apply requires a non-zero timestamp")
		}
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.ContentEventDedupeResult{}, xerrors.Errorf("failed to open DB for content-event dedupe: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.ContentEventDedupeResult{}, xerrors.Errorf("failed to begin content-event dedupe transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if err := tx.Rollback(); err != nil {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	rows, err := d.loadDedupeCandidates(ctx, tx, strings.TrimSpace(params.Agent))
	if err != nil {
		return apptypes.ContentEventDedupeResult{}, err
	}

	plan := planContentEventDedupe(rows, params.Strict)

	if params.Apply {
		for _, group := range plan.groups {
			for _, dup := range group.duplicates {
				if err := archiveDedupeRow(ctx, tx, dup, group.keptID, params.RunID, params.Now, group.forensicKey, group.reason); err != nil {
					return apptypes.ContentEventDedupeResult{}, err
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return apptypes.ContentEventDedupeResult{}, xerrors.Errorf("failed to commit content-event dedupe transaction: %w", err)
		}
		committed = true
	}

	result := apptypes.ContentEventDedupeResult{
		RunID:        params.RunID,
		Applied:      params.Apply,
		ScannedCount: len(rows),
	}
	for _, group := range plan.groups {
		dupIDs := make([]string, 0, len(group.duplicates))
		for _, dup := range group.duplicates {
			dupIDs = append(dupIDs, dup.id)
		}
		result.Groups = append(result.Groups, apptypes.ContentEventDedupeGroup{
			KeptEventID:       group.keptID,
			DuplicateEventIDs: dupIDs,
			Kind:              group.kind,
			Agent:             group.agent,
			SourceHook:        group.sourceHook,
			GroupKey:          group.forensicKey,
		})
	}
	result.Skipped = plan.skipped
	return result, nil
}

// loadDedupeCandidates reads every eligible hook content event. Eligibility is
// enforced in SQL (client='hook', kind in prompt/transcript) so command audits
// and non-hook writes never enter the maintenance path. created_at is parsed in
// Go (RFC3339Nano) rather than ordered lexically in SQL, because formatTimestamp
// emits variable-width fractional seconds that are not lexically time-ordered.
func (d *StoreManagementDatasource) loadDedupeCandidates(
	ctx context.Context,
	tx *sql.Tx,
	agent string,
) ([]dedupeCandidateRow, error) {
	query := `SELECT id, kind, client, agent, session_id, workspace, body, created_at, source_hook
	            FROM events
	           WHERE client = 'hook'
	             AND kind IN ('prompt', 'transcript')`
	args := []any{}
	if agent != "" {
		query += "\n             AND agent = ?"
		args = append(args, agent)
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("failed to query content-event dedupe candidates: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var candidates []dedupeCandidateRow
	for rows.Next() {
		var row dedupeCandidateRow
		if err := rows.Scan(
			&row.id,
			&row.kind,
			&row.client,
			&row.agent,
			&row.sessionID,
			&row.workspace,
			&row.body,
			&row.createdAt,
			&row.sourceHook,
		); err != nil {
			return nil, xerrors.Errorf("failed to scan content-event dedupe candidate: %w", err)
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, row.createdAt)
		row.parsedAt = parsed
		row.parseOK = parseErr == nil
		candidates = append(candidates, row)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate content-event dedupe candidates: %w", err)
	}
	return candidates, nil
}

// dedupeGroupPlan is one resolved duplicate cluster: the canonical row to keep
// and the rows to quarantine.
type dedupeGroupPlan struct {
	keptID      string
	kind        string
	agent       string
	sourceHook  string
	forensicKey string
	reason      string
	duplicates  []dedupeCandidateRow
}

type dedupePlan struct {
	groups  []dedupeGroupPlan
	skipped []apptypes.ContentEventDedupeSkip
}

// planContentEventDedupe groups eligible rows by identity and resolves each
// group into kept/duplicate sets. Groups containing a row with a malformed
// created_at are skipped wholesale (a canonical row cannot be chosen safely) and
// reported. By default rows are clustered by time proximity so only near-
// simultaneous writes are eligible; strict mode treats the whole group as one
// cluster. The canonical row is the earliest parsed created_at, tie-broken by
// the smallest event id.
func planContentEventDedupe(rows []dedupeCandidateRow, strict bool) dedupePlan {
	grouped := map[dedupeGroupKey][]dedupeCandidateRow{}
	order := []dedupeGroupKey{}
	for _, row := range rows {
		key := newDedupeGroupKey(row)
		if _, ok := grouped[key]; !ok {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], row)
	}

	var plan dedupePlan
	for _, key := range order {
		members := grouped[key]
		if len(members) <= 1 {
			continue
		}
		// Skip any identity group that contains an unparseable timestamp: the
		// canonical-row choice depends on time ordering, so an ambiguous member
		// makes the whole group unsafe to mutate.
		if hasMalformedTimestamp(members) {
			plan.skipped = append(plan.skipped, apptypes.ContentEventDedupeSkip{
				GroupKey: key.forensicKey(),
				EventIDs: sortedEventIDs(members),
				Reason:   contentEventDedupeReasonMalformed,
			})
			continue
		}

		// clusterByProximity and the canonical-row choice below both require
		// ascending (parsedAt, id) order, but loadDedupeCandidates issues no
		// ORDER BY (SQL row order is unspecified). Sorting here is what makes the
		// result correct regardless of how the store returned these rows: the
		// earliest created_at becomes the kept row and proximity gaps are measured
		// against the true time-ascending sequence.
		sort.Slice(members, func(i, j int) bool {
			if !members[i].parsedAt.Equal(members[j].parsedAt) {
				return members[i].parsedAt.Before(members[j].parsedAt)
			}
			return members[i].id < members[j].id
		})

		reason := contentEventDedupeReasonProximity
		clusters := clusterByProximity(members, contentEventDedupeProximityWindow)
		if strict {
			reason = contentEventDedupeReasonStrict
			clusters = [][]dedupeCandidateRow{members}
		}

		for _, cluster := range clusters {
			if len(cluster) < 2 {
				continue
			}
			// cluster is already ascending by (parsedAt, id): first = canonical.
			kept := cluster[0]
			plan.groups = append(plan.groups, dedupeGroupPlan{
				keptID:      kept.id,
				kind:        key.kind,
				agent:       key.agent,
				sourceHook:  key.sourceHook,
				forensicKey: key.forensicKey(),
				reason:      reason,
				duplicates:  cluster[1:],
			})
		}
	}
	return plan
}

func hasMalformedTimestamp(rows []dedupeCandidateRow) bool {
	for _, row := range rows {
		if !row.parseOK {
			return true
		}
	}
	return false
}

func sortedEventIDs(rows []dedupeCandidateRow) []string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.id
	}
	sort.Strings(ids)
	return ids
}

// clusterByProximity splits a time-ascending run into clusters where each
// consecutive pair is within window. It mirrors the doctor diagnostic's
// proximity clustering so the two paths report the same near-simultaneous
// groups.
func clusterByProximity(sorted []dedupeCandidateRow, window time.Duration) [][]dedupeCandidateRow {
	if len(sorted) == 0 {
		return nil
	}
	var clusters [][]dedupeCandidateRow
	run := []dedupeCandidateRow{sorted[0]}
	for _, row := range sorted[1:] {
		if row.parsedAt.Sub(run[len(run)-1].parsedAt) <= window {
			run = append(run, row)
			continue
		}
		clusters = append(clusters, run)
		run = []dedupeCandidateRow{row}
	}
	clusters = append(clusters, run)
	return clusters
}

// archiveDedupeRow moves a single duplicate row out of events and into the
// quarantine archive within the supplied transaction. The original body and
// created_at text are preserved verbatim so restore is exact.
func archiveDedupeRow(
	ctx context.Context,
	tx *sql.Tx,
	row dedupeCandidateRow,
	keptID string,
	runID string,
	now time.Time,
	forensicKey string,
	reason string,
) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO event_content_dedupe_archive
		    (id, kind, client, agent, session_id, workspace, body, created_at,
		     source_hook, kept_event_id, dedupe_run_id, archived_at, group_key, reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.id,
		row.kind,
		row.client,
		row.agent,
		row.sessionID,
		row.workspace,
		row.body,
		row.createdAt,
		row.sourceHook,
		keptID,
		runID,
		formatTimestamp(now),
		forensicKey,
		reason,
	); err != nil {
		return xerrors.Errorf("failed to archive duplicate event %s: %w", row.id, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, row.id); err != nil {
		return xerrors.Errorf("failed to remove archived duplicate event %s: %w", row.id, err)
	}
	return nil
}

// RestoreContentEventDedupeRun moves the rows quarantined by the given dedupe
// run back into events. It is all-or-nothing: if any original event id already
// exists in events, the whole restore fails (no overwrite) and nothing changes.
func (d *StoreManagementDatasource) RestoreContentEventDedupeRun(
	ctx context.Context,
	runID string,
) (apptypes.ContentEventDedupeRestoreResult, error) {
	trimmed := strings.TrimSpace(runID)
	if trimmed == "" {
		return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("dedupe run id must not be empty")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to open DB for dedupe restore: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to begin dedupe restore transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if err := tx.Rollback(); err != nil {
			slog.Debug("failed to rollback transaction", "error", err)
		}
	}()

	rows, err := tx.QueryContext(
		ctx,
		`SELECT id, kind, client, agent, session_id, workspace, body, created_at, source_hook
		   FROM event_content_dedupe_archive
		  WHERE dedupe_run_id = ?`,
		trimmed,
	)
	if err != nil {
		return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to query dedupe archive run: %w", err)
	}

	type archivedRow struct {
		id         string
		kind       string
		client     string
		agent      string
		sessionID  string
		workspace  string
		body       string
		createdAt  string
		sourceHook sql.NullString
	}
	var archived []archivedRow
	for rows.Next() {
		var r archivedRow
		if err := rows.Scan(
			&r.id, &r.kind, &r.client, &r.agent, &r.sessionID,
			&r.workspace, &r.body, &r.createdAt, &r.sourceHook,
		); err != nil {
			if closeErr := rows.Close(); closeErr != nil {
				slog.Debug("failed to close resource", "error", closeErr)
			}
			return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to scan dedupe archive row: %w", err)
		}
		archived = append(archived, r)
	}
	if err := rows.Err(); err != nil {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Debug("failed to close resource", "error", closeErr)
		}
		return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to iterate dedupe archive rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		slog.Debug("failed to close resource", "error", err)
	}

	if len(archived) == 0 {
		return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("no quarantined rows found for dedupe run %q", trimmed)
	}

	for _, r := range archived {
		var exists int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM events WHERE id = ?`, r.id).Scan(&exists); err {
		case nil:
			return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf(
				"refusing to restore dedupe run %q: event %s already exists in events", trimmed, r.id)
		case sql.ErrNoRows:
			// expected: the row was quarantined, so it must be absent
		default:
			return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to check existing event %s: %w", r.id, err)
		}

		if _, err := tx.ExecContext(
			ctx,
			insertEventQuery,
			r.id, r.kind, r.client, r.agent, r.sessionID, r.workspace, r.body, r.createdAt, r.sourceHook,
		); err != nil {
			return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to restore event %s: %w", r.id, err)
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM event_content_dedupe_archive WHERE dedupe_run_id = ? AND id = ?`,
			trimmed, r.id,
		); err != nil {
			return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to clear archive row %s: %w", r.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return apptypes.ContentEventDedupeRestoreResult{}, xerrors.Errorf("failed to commit dedupe restore transaction: %w", err)
	}
	committed = true

	return apptypes.ContentEventDedupeRestoreResult{
		RunID:         trimmed,
		RestoredCount: len(archived),
	}, nil
}
