package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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

// dedupeMemberRef is one row's participation in an identity group.
//
// The body is deliberately absent. A body is needed for exactly two things: to
// establish identity (hashed during identification and then dropped) and to copy
// into the quarantine archive (re-read one batch at a time during apply). Keeping
// every decoded body resident is what made a full-store apply unrunnable: the
// eligible set is ~246k rows and ~3.3 GiB of stored payload.
type dedupeMemberRef struct {
	id        string
	createdAt string // original RFC3339Nano text, preserved verbatim
	parsedAt  time.Time
	parseOK   bool
}

// dedupeGroupKey is the duplicate-identity tuple. It intentionally matches the
// content-event-reliability doctor diagnostic — kind, client, agent, session_id,
// workspace, source_hook, and the whitespace-trimmed body — so the maintenance
// command and the diagnostic agree on what counts as a duplicate. This is
// deliberately NOT a runtime delivery identity: current writes require a
// stable host-native ID and never infer redelivery from body equality. The
// reversible maintenance identity follows the heuristic diagnostic instead.
//
// The trimmed body is held as its SHA-256 digest rather than as text so the key
// stays a small comparable value and identification never retains body content.
type dedupeGroupKey struct {
	kind       string
	client     string
	agent      string
	sessionID  string
	workspace  string
	sourceHook string
	bodyDigest [sha256.Size]byte
}

func newDedupeGroupKey(kind, client, agent, sessionID, workspace, sourceHook, body string) dedupeGroupKey {
	// Same normalization as the doctor diagnostic: trim surrounding whitespace
	// only, so trailing-newline noise does not split a pair, but genuinely
	// different prompts stay distinct.
	return dedupeGroupKey{
		kind:       kind,
		client:     client,
		agent:      agent,
		sessionID:  sessionID,
		workspace:  workspace,
		sourceHook: sourceHook,
		bodyDigest: sha256.Sum256([]byte(strings.TrimSpace(body))),
	}
}

// forensicKey renders a stable, compact identity string for archive metadata.
// The body contributes as a digest prefix so the key stays bounded regardless of
// body size.
func (k dedupeGroupKey) forensicKey() string {
	hook := k.sourceHook
	if hook == "" {
		hook = "-"
	}
	return strings.Join([]string{
		k.kind, k.client, k.agent, k.sessionID, k.workspace, hook,
		"body:" + hex.EncodeToString(k.bodyDigest[:8]),
	}, "|")
}

// stringInterner collapses the small set of repeated agent/hook/workspace values
// that identification would otherwise allocate once per scanned row.
type stringInterner map[string]string

func (s stringInterner) intern(value string) string {
	if existing, ok := s[value]; ok {
		return existing
	}
	s[value] = value
	return value
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
	if params.MaxScanRows < 0 {
		return apptypes.ContentEventDedupeResult{}, xerrors.Errorf("dedupe scan bound must not be negative")
	}
	if params.Apply && params.MaxScanRows > 0 {
		return apptypes.ContentEventDedupeResult{}, xerrors.Errorf("bounded content-event dedupe cannot be applied")
	}
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

	agent := strings.TrimSpace(params.Agent)

	// Identification is read-only and holds no write transaction. Apply then
	// commits in bounded batches (see applyDedupeGroups), so an interrupted run
	// leaves a consistent store instead of rolling the whole repair back.
	survey, err := d.identifyDedupeGroups(ctx, db, agent, params.MaxScanRows)
	if err != nil {
		return apptypes.ContentEventDedupeResult{}, err
	}

	plan := planContentEventDedupe(survey, params.Strict)

	if params.Apply {
		if err := d.applyDedupeGroups(ctx, db, plan, params); err != nil {
			return apptypes.ContentEventDedupeResult{}, err
		}
	}

	result := apptypes.ContentEventDedupeResult{
		RunID:              params.RunID,
		Applied:            params.Apply,
		TotalEligibleCount: survey.totalEligible,
		ScannedCount:       survey.scannedCount,
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
	result.Sources = contentEventDedupeSourceStats(survey.scannedBySource, result.Groups)
	return result, nil
}

// dedupeSourceKey identifies one (agent, source_hook) reporting bucket.
type dedupeSourceKey struct {
	agent string
	hook  string
}

func contentEventDedupeSourceStats(
	scannedBySource map[dedupeSourceKey]int,
	groups []apptypes.ContentEventDedupeGroup,
) []apptypes.ContentEventDedupeSourceStat {
	type sourceKey = dedupeSourceKey
	stats := map[sourceKey]*apptypes.ContentEventDedupeSourceStat{}
	for key, scanned := range scannedBySource {
		stats[key] = &apptypes.ContentEventDedupeSourceStat{
			Agent: key.agent, SourceHook: key.hook, ScannedCount: scanned,
		}
	}
	for _, group := range groups {
		key := sourceKey{agent: group.Agent, hook: group.SourceHook}
		item := stats[key]
		if item == nil {
			item = &apptypes.ContentEventDedupeSourceStat{Agent: key.agent, SourceHook: key.hook}
			stats[key] = item
		}
		item.GroupCount++
		item.CandidateCount += group.DuplicateCount()
	}
	result := make([]apptypes.ContentEventDedupeSourceStat, 0, len(stats))
	for _, item := range stats {
		item.CandidateRate = ratio(item.CandidateCount, item.ScannedCount)
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Agent != result[j].Agent {
			return result[i].Agent < result[j].Agent
		}
		return result[i].SourceHook < result[j].SourceHook
	})
	return result
}

// dedupeSurvey is the outcome of the read-only identification pass.
type dedupeSurvey struct {
	groups          map[dedupeGroupKey][]dedupeMemberRef
	order           []dedupeGroupKey
	scannedBySource map[dedupeSourceKey]int
	scannedCount    int
	totalEligible   int
}

// identifyDedupeGroups streams every eligible hook content event and records
// which identity group each row belongs to, without retaining any body.
// Eligibility is enforced in SQL (client='hook', kind in prompt/transcript) so
// command audits and non-hook writes never enter the maintenance path.
//
// created_at is parsed in Go (RFC3339Nano) rather than ordered lexically in SQL,
// because formatTimestamp emits variable-width fractional seconds that are not
// lexically time-ordered.
//
// The payload metadata columns are probed once and, when present, selected
// inline so each row decodes from the values already scanned. The previous
// implementation called loadEventPlaintext per row, which issued two extra
// queries for every candidate.
func (d *StoreManagementDatasource) identifyDedupeGroups(
	ctx context.Context,
	db *sql.DB,
	agent string,
	maxRows int,
) (dedupeSurvey, error) {
	survey := dedupeSurvey{
		groups:          map[dedupeGroupKey][]dedupeMemberRef{},
		scannedBySource: map[dedupeSourceKey]int{},
	}

	hasCodec, err := databaseColumnExists(ctx, db, "events", "body_codec")
	if err != nil {
		return dedupeSurvey{}, err
	}
	scope, err := resolveDedupeEligibilityScope(ctx, db)
	if err != nil {
		return dedupeSurvey{}, err
	}

	if maxRows > 0 {
		survey.totalEligible, err = d.countDedupeCandidates(ctx, db, agent, scope)
		if err != nil {
			return dedupeSurvey{}, err
		}
	}

	payloadColumns := `CASE WHEN length(CAST(body AS BLOB)) <= ? THEN body END, length(CAST(body AS BLOB))`
	args := []any{int64(maxDecodedPayloadBytes)}
	if hasCodec {
		payloadColumns = `CASE WHEN length(CAST(body AS BLOB)) <= ? THEN body END,
		       body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256,
		       length(CAST(body AS BLOB))`
		args = []any{int64(maxStoredPayloadBytes)}
	}
	query := `SELECT id, kind, client, agent, session_id, workspace, created_at, source_hook,
	       ` + payloadColumns + `
	            FROM events
	           WHERE ` + dedupeEligibilityFilter(scope)
	if agent != "" {
		query += "\n             AND agent = ?"
		args = append(args, agent)
	}
	if maxRows > 0 {
		// This order selects a deterministic diagnostic sample only. Canonical
		// duplicate resolution still parses timestamps in planContentEventDedupe;
		// it never relies on SQLite's lexical timestamp ordering.
		query += "\n           ORDER BY created_at DESC, id DESC LIMIT ?"
		args = append(args, maxRows)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return dedupeSurvey{}, xerrors.Errorf("failed to query content-event dedupe candidates: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	interner := stringInterner{}
	for rows.Next() {
		var (
			id, kind, client, rowAgent, sessionID, workspace, createdAt string
			sourceHook                                                  sql.NullString
			payload                                                     payloadRow
			storedLength                                                sql.NullInt64
		)
		destinations := []any{&id, &kind, &client, &rowAgent, &sessionID, &workspace, &createdAt, &sourceHook}
		if hasCodec {
			destinations = append(destinations, payload.scanDestinations()...)
		} else {
			destinations = append(destinations, &payload.Stored)
		}
		destinations = append(destinations, &storedLength)
		if err := rows.Scan(destinations...); err != nil {
			return dedupeSurvey{}, xerrors.Errorf("failed to scan content-event dedupe candidate: %w", err)
		}

		body, err := decodeDedupeCandidateBody(payload, storedLength, hasCodec, id)
		if err != nil {
			return dedupeSurvey{}, err
		}

		key := newDedupeGroupKey(
			interner.intern(kind),
			interner.intern(client),
			interner.intern(rowAgent),
			sessionID,
			interner.intern(workspace),
			interner.intern(sourceHook.String),
			body,
		)
		parsed, parseErr := time.Parse(time.RFC3339Nano, createdAt)
		if _, seen := survey.groups[key]; !seen {
			survey.order = append(survey.order, key)
		}
		survey.groups[key] = append(survey.groups[key], dedupeMemberRef{
			id:        id,
			createdAt: createdAt,
			parsedAt:  parsed,
			parseOK:   parseErr == nil,
		})
		survey.scannedBySource[dedupeSourceKey{agent: key.agent, hook: key.sourceHook}]++
		survey.scannedCount++
	}
	if err := rows.Err(); err != nil {
		return dedupeSurvey{}, xerrors.Errorf("failed to iterate content-event dedupe candidates: %w", err)
	}
	if maxRows == 0 {
		survey.totalEligible = survey.scannedCount
	}
	return survey, nil
}

// decodeDedupeCandidateBody mirrors loadEventPlaintext's contract for a row whose
// payload columns were already scanned: an oversized stored payload is a payload
// integrity error rather than a silently truncated body.
func decodeDedupeCandidateBody(
	payload payloadRow,
	storedLength sql.NullInt64,
	hasCodec bool,
	eventID string,
) (string, error) {
	limit := int64(maxDecodedPayloadBytes)
	codec := payloadCodecIdentity
	if hasCodec {
		limit = maxStoredPayloadBytes
		codec = payload.Codec.String
	}
	if storedLength.Valid && storedLength.Int64 > limit {
		return "", xerrors.Errorf("decode dedupe candidate %s: %w", eventID,
			&PayloadIntegrityError{Codec: codec, RowID: eventID, Field: "body", Reason: "stored length exceeds limit"})
	}
	if !hasCodec {
		return string(payload.Stored), nil
	}
	plain, err := payload.decode(maxDecodedPayloadBytes)
	if err != nil {
		return "", xerrors.Errorf("decode dedupe candidate %s: %w", eventID, annotatePayloadError(err, eventID, "body"))
	}
	return string(plain), nil
}

// dedupeEligibilityScope records which optional retention schema this store
// carries, so the eligibility filter degrades cleanly on stores predating
// migration 26 instead of assuming the columns and tables exist.
type dedupeEligibilityScope struct {
	hasBodyAvailability bool
	hasRetentionLedger  bool
}

func resolveDedupeEligibilityScope(ctx context.Context, db *sql.DB) (dedupeEligibilityScope, error) {
	hasBodyAvailability, err := databaseColumnExists(ctx, db, "events", "body_availability")
	if err != nil {
		return dedupeEligibilityScope{}, err
	}
	hasRetentionLedger, err := databaseTableExists(ctx, db, "raw_body_retention_entries")
	if err != nil {
		return dedupeEligibilityScope{}, err
	}
	return dedupeEligibilityScope{
		hasBodyAvailability: hasBodyAvailability,
		hasRetentionLedger:  hasRetentionLedger,
	}, nil
}

// dedupeEligibilityFilter is the WHERE fragment shared by the candidate count
// and the identification scan.
//
// Two classes of row are excluded, both because of raw-body retention.
//
// Rows the pruner has emptied: pruning replaces the body with one fixed marker
// string for every row it touches, without regard to client or kind, so an
// emptied prompt and an emptied transcript from unrelated sessions hash to the
// same identity. Including them would collapse unrelated rows into one enormous
// group and quarantine rows that never duplicated anything.
//
// Rows that carry a retention ledger entry, restored or not:
// raw_body_retention_entries.event_id is ON DELETE RESTRICT, and a retention
// restore puts body_availability back to 'available' while deliberately keeping
// its ledger row. Such a row looks ordinary but cannot be deleted — apply would
// abort mid-batch on a raw foreign-key error that tells the operator nothing.
// Skipping it also preserves what the RESTRICT is there to protect: the ledger's
// provenance must keep pointing at a real event.
func dedupeEligibilityFilter(scope dedupeEligibilityScope) string {
	filter := `client = 'hook'
	             AND kind IN ('prompt', 'transcript')`
	if scope.hasBodyAvailability {
		filter += "\n	             AND body_availability = 'available'"
	}
	if scope.hasRetentionLedger {
		filter += "\n	             AND NOT EXISTS (SELECT 1 FROM raw_body_retention_entries r WHERE r.event_id = events.id)"
	}
	return filter
}

func (d *StoreManagementDatasource) countDedupeCandidates(
	ctx context.Context,
	q queryRowContexter,
	agent string,
	scope dedupeEligibilityScope,
) (int, error) {
	query := `SELECT COUNT(*)
	            FROM events
	           WHERE ` + dedupeEligibilityFilter(scope)
	args := []any{}
	if agent != "" {
		query += "\n             AND agent = ?"
		args = append(args, agent)
	}
	var count int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, xerrors.Errorf("failed to count content-event dedupe candidates: %w", err)
	}
	return count, nil
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
	duplicates  []dedupeMemberRef
	// atomic marks a group whose duplicates must be archived in one
	// transaction or not at all. Only proximity groups are atomic: their
	// membership is decided from the gaps between *surviving* rows, so
	// archiving part of one widens those gaps and can split it into pieces a
	// re-run will never collapse. A strict group ignores gaps entirely — its
	// membership is the identity tuple alone and its canonical row is the
	// earliest, which archiving duplicates never removes — so a partially
	// archived strict group re-plans to exactly the same decision and may be
	// split across transactions.
	atomic bool
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
func planContentEventDedupe(survey dedupeSurvey, strict bool) dedupePlan {
	var plan dedupePlan
	for _, key := range survey.order {
		members := survey.groups[key]
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
		// ascending (parsedAt, id) order, but identifyDedupeGroups issues no
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
		atomic := true
		clusters := clusterByProximity(members, contentEventDedupeProximityWindow)
		if strict {
			reason = contentEventDedupeReasonStrict
			atomic = false
			clusters = [][]dedupeMemberRef{members}
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
				atomic:      atomic,
			})
		}
	}
	return plan
}

func hasMalformedTimestamp(rows []dedupeMemberRef) bool {
	for _, row := range rows {
		if !row.parseOK {
			return true
		}
	}
	return false
}

func sortedEventIDs(rows []dedupeMemberRef) []string {
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
func clusterByProximity(sorted []dedupeMemberRef, window time.Duration) [][]dedupeMemberRef {
	if len(sorted) == 0 {
		return nil
	}
	var clusters [][]dedupeMemberRef
	run := []dedupeMemberRef{sorted[0]}
	for _, row := range sorted[1:] {
		if row.parsedAt.Sub(run[len(run)-1].parsedAt) <= window {
			run = append(run, row)
			continue
		}
		clusters = append(clusters, run)
		run = []dedupeMemberRef{row}
	}
	clusters = append(clusters, run)
	return clusters
}

// dedupeArchiveTarget is one row the plan resolved as a duplicate, carried with
// the provenance the archive needs. The body is not carried: it is re-read from
// events inside the batch that archives it.
type dedupeArchiveTarget struct {
	id          string
	keptID      string
	forensicKey string
	reason      string
}

// applyDedupeGroups quarantines the planned duplicates in bounded, separately
// committed batches, never splitting a proximity cluster across two commits.
//
// Committing per batch is what makes an interrupted repair safe and resumable,
// but for proximity groups only because the cluster is the atomic unit.
// Proximity clustering measures the gap between *consecutive surviving* rows, so
// archiving part of a cluster widens the gaps inside it and can split what was
// one cluster into several, each keeping its own canonical row. A cluster of
// t=0s, 9s, 18s under a 10s window is one cluster keeping only t=0; archive t=9
// alone and the remaining 0→18 gap exceeds the window, leaving two singleton
// clusters that no re-run will ever collapse. Committing whole clusters removes
// that state entirely: an interrupted run leaves every cluster either fully
// archived or untouched, so re-planning reproduces exactly what a clean run
// would have decided.
//
// Strict groups carry no such constraint and are split at the batch size — see
// dedupeGroupPlan.atomic.
//
// Resumption therefore needs no checkpoint. The rows a previous run archived are
// gone from events, so a re-run does not see them at all, and what it does see
// re-plans to the same decision.
//
// A proximity cluster larger than the batch size becomes its own batch:
// correctness of the clustering decision outranks the transaction-size target.
func (d *StoreManagementDatasource) applyDedupeGroups(
	ctx context.Context,
	db *sql.DB,
	plan dedupePlan,
	params apptypes.ContentEventDedupeParams,
) error {
	archivedAt := formatTimestamp(params.Now)
	for _, batch := range partitionDedupeTargets(plan, params.BatchSize) {
		if err := d.archiveDedupeBatch(ctx, db, batch, params.RunID, archivedAt); err != nil {
			return err
		}
	}
	return nil
}

// partitionDedupeTargets splits a plan into the transactions apply will commit.
//
// The invariant it exists to make testable: an *atomic* group is never split
// across two partitions. For those groups batchSize is a target, not a bound —
// one with more duplicates than batchSize becomes a single oversized partition
// rather than being divided. See applyDedupeGroups for why splitting one is
// unsafe, and dedupeGroupPlan.atomic for which groups are.
//
// A non-atomic group is split at batchSize like any other work. Strict mode
// produces exactly one group per identity tuple — on the live store the largest
// is over 36,000 rows — so honouring batchSize there is what keeps
// `--strict --apply` from becoming the single unresumable transaction batching
// exists to prevent.
func partitionDedupeTargets(plan dedupePlan, batchSize int) [][]dedupeArchiveTarget {
	if batchSize <= 0 {
		batchSize = apptypes.DefaultContentEventDedupeBatchSize
	}

	batches := [][]dedupeArchiveTarget{}
	current := []dedupeArchiveTarget{}
	flush := func() {
		if len(current) > 0 {
			batches = append(batches, current)
			current = []dedupeArchiveTarget{}
		}
	}
	for _, group := range plan.groups {
		if len(group.duplicates) == 0 {
			continue
		}
		if group.atomic && len(current) > 0 && len(current)+len(group.duplicates) > batchSize {
			flush()
		}
		for _, dup := range group.duplicates {
			if !group.atomic && len(current) >= batchSize {
				flush()
			}
			current = append(current, dedupeArchiveTarget{
				id: dup.id, keptID: group.keptID,
				forensicKey: group.forensicKey, reason: group.reason,
			})
		}
	}
	flush()
	return batches
}

// archiveDedupeBatch moves one batch of duplicate rows out of events and into the
// quarantine archive in a single transaction. The original body and created_at
// text are preserved verbatim so restore is exact.
func (d *StoreManagementDatasource) archiveDedupeBatch(
	ctx context.Context,
	db *sql.DB,
	targets []dedupeArchiveTarget,
	runID string,
	archivedAt string,
) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return xerrors.Errorf("failed to begin content-event dedupe transaction: %w", err)
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

	for _, target := range targets {
		// A row absent from events was already archived by an interrupted earlier
		// run; the repair is idempotent, so skip rather than fail.
		source, found, err := readDedupeArchiveSource(ctx, tx, target.id)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO event_content_dedupe_archive
			    (id, kind, client, agent, session_id, workspace, body, created_at,
			     source_hook, kept_event_id, dedupe_run_id, archived_at, group_key, reason)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			     ON CONFLICT(dedupe_run_id, id) DO NOTHING`,
			source.id, source.kind, source.client, source.agent, source.sessionID, source.workspace,
			source.body, source.createdAt, source.sourceHook,
			target.keptID, runID, archivedAt, target.forensicKey, target.reason,
		); err != nil {
			return xerrors.Errorf("failed to archive duplicate event %s: %w", target.id, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, target.id); err != nil {
			return xerrors.Errorf("failed to remove archived duplicate event %s: %w", target.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return xerrors.Errorf("failed to commit content-event dedupe transaction: %w", err)
	}
	committed = true
	return nil
}

// dedupeArchiveSource is an events row as the quarantine archive stores it. body
// is the *decoded* plaintext, matching what RestoreContentEventDedupeRun expects
// to re-encode; copying the raw stored column instead would corrupt a
// codec-encoded payload on restore.
type dedupeArchiveSource struct {
	id         string
	kind       string
	client     string
	agent      string
	sessionID  string
	workspace  string
	createdAt  string
	sourceHook sql.NullString
	body       string
}

// readDedupeArchiveSource reads one row about to be quarantined. A missing row is
// reported as not-found rather than as an error so a resumed run can skip rows an
// earlier interrupted run already archived.
func readDedupeArchiveSource(ctx context.Context, tx *sql.Tx, eventID string) (dedupeArchiveSource, bool, error) {
	var source dedupeArchiveSource
	err := tx.QueryRowContext(
		ctx,
		`SELECT id, kind, client, agent, session_id, workspace, created_at, source_hook
		   FROM events WHERE id = ?`,
		eventID,
	).Scan(
		&source.id, &source.kind, &source.client, &source.agent,
		&source.sessionID, &source.workspace, &source.createdAt, &source.sourceHook,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return dedupeArchiveSource{}, false, nil
	case err != nil:
		return dedupeArchiveSource{}, false, xerrors.Errorf("failed to read duplicate event %s: %w", eventID, err)
	}
	plain, err := loadEventPlaintext(ctx, tx, eventID)
	if err != nil {
		return dedupeArchiveSource{}, false, xerrors.Errorf("decode duplicate event %s: %w", eventID, err)
	}
	source.body = string(plain)
	return source, true, nil
}

// PurgeContentEventDedupeRun drops the rows a dedupe run quarantined, ending that
// run's rollback window. Until a run is purged its bodies still occupy the store,
// so quarantine alone relocates duplicates rather than reclaiming them. Purge is
// deliberately a separate operator step from apply — the reversibility of apply
// is the point of the archive.
func (d *StoreManagementDatasource) PurgeContentEventDedupeRun(
	ctx context.Context,
	runID string,
) (apptypes.ContentEventDedupePurgeResult, error) {
	trimmed := strings.TrimSpace(runID)
	if trimmed == "" {
		return apptypes.ContentEventDedupePurgeResult{}, xerrors.Errorf("dedupe run id must not be empty")
	}

	db, err := d.db.open(ctx)
	if err != nil {
		return apptypes.ContentEventDedupePurgeResult{}, xerrors.Errorf("failed to open DB for dedupe purge: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	var (
		rowCount int
		byteSum  sql.NullInt64
	)
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*), SUM(length(CAST(body AS BLOB)))
		   FROM event_content_dedupe_archive WHERE dedupe_run_id = ?`,
		trimmed,
	).Scan(&rowCount, &byteSum); err != nil {
		return apptypes.ContentEventDedupePurgeResult{}, xerrors.Errorf("failed to measure dedupe archive run: %w", err)
	}
	if rowCount == 0 {
		return apptypes.ContentEventDedupePurgeResult{}, xerrors.Errorf("no quarantined rows found for dedupe run %q", trimmed)
	}

	if _, err := db.ExecContext(
		ctx,
		`DELETE FROM event_content_dedupe_archive WHERE dedupe_run_id = ?`,
		trimmed,
	); err != nil {
		return apptypes.ContentEventDedupePurgeResult{}, xerrors.Errorf("failed to purge dedupe archive run: %w", err)
	}

	return apptypes.ContentEventDedupePurgeResult{
		RunID:        trimmed,
		PurgedCount:  rowCount,
		ReleasedBody: byteSum.Int64,
	}, nil
}

// ListContentEventDedupeRuns reports every quarantine run still held in the
// archive, newest first.
//
// This is what keeps an interrupted apply recoverable. Apply commits in batches,
// so a run killed after its first commit has already quarantined rows under a
// run id that was never printed; listing is the only way to find it again and
// decide between `--restore` and `--purge`.
func (d *StoreManagementDatasource) ListContentEventDedupeRuns(
	ctx context.Context,
) ([]apptypes.ContentEventDedupeRun, error) {
	db, err := d.db.open(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for dedupe run listing: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	rows, err := db.QueryContext(ctx, `
		SELECT dedupe_run_id, MAX(archived_at), COUNT(*), SUM(length(CAST(body AS BLOB)))
		  FROM event_content_dedupe_archive
		 GROUP BY dedupe_run_id`)
	if err != nil {
		return nil, xerrors.Errorf("failed to query dedupe archive runs: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	runs := []apptypes.ContentEventDedupeRun{}
	for rows.Next() {
		var (
			run        apptypes.ContentEventDedupeRun
			archivedAt sql.NullString
			bodyBytes  sql.NullInt64
		)
		if err := rows.Scan(&run.RunID, &archivedAt, &run.QuarantinedRows, &bodyBytes); err != nil {
			return nil, xerrors.Errorf("failed to scan dedupe archive run: %w", err)
		}
		run.ArchivedAt = archivedAt.String
		run.BodyBytes = bodyBytes.Int64
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate dedupe archive runs: %w", err)
	}
	sortDedupeRunsNewestFirst(runs)
	return runs, nil
}

// sortDedupeRunsNewestFirst orders by parsed instant rather than letting SQLite
// sort the strings. archived_at is RFC3339Nano, whose text form is not
// order-preserving: a fractional second sorts before a whole one within the same
// second, because '.' precedes 'Z'. An operator reading the list to pick the run
// to restore must see the true newest first.
func sortDedupeRunsNewestFirst(runs []apptypes.ContentEventDedupeRun) {
	sort.SliceStable(runs, func(i, j int) bool {
		left, leftOK := time.Parse(time.RFC3339Nano, runs[i].ArchivedAt)
		right, rightOK := time.Parse(time.RFC3339Nano, runs[j].ArchivedAt)
		if leftOK == nil && rightOK == nil && !left.Equal(right) {
			return left.After(right)
		}
		// Unparsable timestamps keep a stable, deterministic position rather than
		// floating: fall back to the run id, which is unique per run.
		if runs[i].ArchivedAt != runs[j].ArchivedAt {
			return runs[i].ArchivedAt > runs[j].ArchivedAt
		}
		return runs[i].RunID > runs[j].RunID
	})
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

		payload, err := encodePayload([]byte(r.body), payloadCodecIdentity)
		if err != nil {
			return apptypes.ContentEventDedupeRestoreResult{}, err
		}
		hasCodec, err := transactionColumnExists(ctx, tx, "events", "body_codec")
		if err != nil {
			return apptypes.ContentEventDedupeRestoreResult{}, err
		}
		query := insertEventQuery
		args := []any{r.id, r.kind, r.client, r.agent, r.sessionID, r.workspace, string(payload.Bytes), r.createdAt, r.sourceHook}
		if hasCodec {
			query = `INSERT INTO events(id, kind, client, agent, session_id, workspace, body, created_at, source_hook,
body_codec, body_format_version, body_plaintext_bytes, body_encoded_bytes, body_sha256) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
			args = append(args, payload.Codec, payload.FormatVersion, payload.PlaintextBytes, payload.StoredBytes, payload.SHA256)
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
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
