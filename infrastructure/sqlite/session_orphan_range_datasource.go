package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log/slog"
	"sort"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/insert_session_orphan_range.sql
var insertSessionOrphanRangeQuery string

//go:embed sql/list_recorded_orphan_ranges.sql
var listRecordedOrphanRangesQuery string

//go:embed sql/list_ended_or_stale_sessions_for_orphan.sql
var listEndedOrStaleSessionsForOrphanQuery string

//go:embed sql/select_latest_event_id_for_session.sql
var selectLatestEventIDForSessionQuery string

//go:embed sql/select_orphan_range_events.sql
var selectOrphanRangeEventsQuery string

//go:embed sql/select_orphan_range_commands.sql
var selectOrphanRangeCommandsQuery string

//go:embed sql/select_session_is_ended.sql
var selectSessionIsEndedQuery string

//go:embed sql/select_session_is_stale.sql
var selectSessionIsStaleQuery string

// SessionOrphanRangeDatasource is the SQLite-backed SessionOrphanRangeRepository.
type SessionOrphanRangeDatasource struct {
	db *Database
}

// NewSessionOrphanRangeDatasource creates a datasource bound to db.
func NewSessionOrphanRangeDatasource(db *Database) *SessionOrphanRangeDatasource {
	return &SessionOrphanRangeDatasource{db: db}
}

var _ model.SessionOrphanRangeRepository = (*SessionOrphanRangeDatasource)(nil)

// WithReadScope lets a caller that will run DiscoverCandidates and
// LoadMaterial together as one orphan-consolidation pass share a single
// read-only handle instead of paying setup+ping+compat once per candidate.
// fn receives a context carrying the scope; pass it (or a context derived
// from it) to the calls that should share the handle. Callers that never
// enter a scope see the current per-call behaviour on each call.
func (d *SessionOrphanRangeDatasource) WithReadScope(ctx context.Context, fn func(context.Context) error) error {
	return d.db.WithReadScope(ctx, fn)
}

// Record inserts an orphan range. Re-recording the same PK is a no-op.
func (d *SessionOrphanRangeDatasource) Record(ctx context.Context, orphan *model.SessionOrphanRange) error {
	if orphan == nil {
		return xerrors.Errorf("session orphan range must not be nil: %w", model.ErrInvalidSessionOrphanRange)
	}
	db, err := d.db.open(ctx)
	if err != nil {
		return xerrors.Errorf("failed to open DB for orphan range record: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Debug("failed to close resource", "error", err)
		}
	}()

	fromID := ""
	if from, ok := orphan.FromEventID().Value(); ok {
		fromID = from.String()
	}
	if _, err := db.ExecContext(
		ctx,
		insertSessionOrphanRangeQuery,
		orphan.SessionID().String(),
		fromID,
		orphan.ToEventID().String(),
		formatTimestamp(orphan.ObservedAt()),
	); err != nil {
		return xerrors.Errorf("failed to record session orphan range: %w", err)
	}
	return nil
}

// DiscoverCandidates finds orphan ranges still needing a degraded refinement.
// It collects at most limit+1 valid candidates so HasMore is computed from
// valid candidates, not raw SQL row counts.
func (d *SessionOrphanRangeDatasource) DiscoverCandidates(
	ctx context.Context,
	staleAfter time.Duration,
	now time.Time,
	limit int,
) (model.SessionOrphanCandidates, error) {
	if staleAfter <= 0 {
		return model.SessionOrphanCandidates{}, xerrors.Errorf("staleAfter must be greater than zero")
	}
	if limit <= 0 {
		return model.SessionOrphanCandidates{}, xerrors.Errorf("limit must be greater than zero")
	}
	var result model.SessionOrphanCandidates
	err := d.db.WithReadScope(ctx, func(ctx context.Context) error {
		db, err := d.openForDiscovery(ctx)
		if err != nil {
			return err
		}

		cutoff := formatTimestamp(now.UTC().Add(-staleAfter))
		seen := map[string]struct{}{}
		var candidates []*model.SessionOrphanRange
		target := limit + 1

		// Front-loaded shortcuts: recorded rows on sessions that are still active.
		// Ended/stale discovery below is the source of truth for those sessions.
		recorded, err := d.listRecorded(ctx, db)
		if err != nil {
			return err
		}
		for _, orphan := range recorded {
			if err := ctx.Err(); err != nil {
				return xerrors.Errorf("orphan candidate discovery cancelled: %w", err)
			}
			if len(candidates) >= target {
				break
			}
			covered, err := d.refinementCovers(ctx, db, orphan.SessionID(), orphan.ToEventID())
			if err != nil {
				return err
			}
			if covered {
				continue
			}
			endedOrStale, err := d.sessionEndedOrStale(ctx, db, orphan.SessionID(), cutoff)
			if err != nil {
				return err
			}
			if endedOrStale {
				// Will be rediscovered from covers_to below with the full gap.
				continue
			}
			key := orphan.SessionID().String() + "\x00" + orphan.ToEventID().String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, orphan)
		}

		// Source of truth: ended or stale sessions with material past covers_to.
		// Paginate by a composite (earliest_event_norm, session_id) keyset until
		// target valid candidates are collected or the scan is exhausted, so
		// folding proceeds oldest-first (#1721) and the cursor stays unique even
		// when two sessions share an earliest event time.
		if len(candidates) < target {
			cursorNorm := ""
			cursorSessionID := ""
			pageSize := limit + 1
			for len(candidates) < target {
				if err := ctx.Err(); err != nil {
					return xerrors.Errorf("orphan candidate discovery cancelled: %w", err)
				}
				page, err := d.listEndedOrStalePage(ctx, db, cutoff, cursorNorm, cursorSessionID, pageSize)
				if err != nil {
					return err
				}
				if len(page) == 0 {
					break
				}
				// Advance the cursor from the last SQL row even when every row was
				// filtered out in Go; otherwise a page of non-candidates loops forever.
				last := page[len(page)-1]
				cursorNorm = last.earliestEventNorm
				cursorSessionID = last.sessionID.String()
				for _, row := range page {
					if len(candidates) >= target {
						break
					}
					orphan, ok, err := d.gapPastCoverage(ctx, db, row.sessionID, row.earliestEventTime, now)
					if err != nil {
						return err
					}
					if !ok {
						continue
					}
					key := orphan.SessionID().String() + "\x00" + orphan.ToEventID().String()
					if _, exists := seen[key]; exists {
						continue
					}
					seen[key] = struct{}{}
					candidates = append(candidates, orphan)
				}
				if len(page) < pageSize {
					break
				}
			}
		}

		// Oldest-first across both sources (#1721). The ended/stale source is
		// already ordered this way; sorting the merged, still-bounded set is
		// cheap and keeps the recorded shortcut from disturbing the order.
		sort.SliceStable(candidates, func(i, j int) bool {
			ti, tj := candidates[i].EarliestEventTime(), candidates[j].EarliestEventTime()
			if !ti.Equal(tj) {
				return ti.Before(tj)
			}
			return candidates[i].SessionID().String() < candidates[j].SessionID().String()
		})

		hasMore := len(candidates) > limit
		if hasMore {
			candidates = candidates[:limit]
		}
		result = model.SessionOrphanCandidates{
			Ranges:  candidates,
			HasMore: hasMore,
		}
		return nil
	})
	if err != nil {
		return model.SessionOrphanCandidates{}, err
	}
	return result, nil
}

// LoadMaterial returns mechanical-summary inputs for a range.
func (d *SessionOrphanRangeDatasource) LoadMaterial(
	ctx context.Context,
	sessionID types.SessionID,
	fromExclusive types.Optional[types.EventID],
	toInclusive types.EventID,
) (model.SessionOrphanMaterial, error) {
	var material model.SessionOrphanMaterial
	err := d.db.WithReadScope(ctx, func(ctx context.Context) error {
		db, err := d.openForDiscovery(ctx)
		if err != nil {
			return err
		}

		fromID := ""
		if from, ok := fromExclusive.Value(); ok {
			fromID = from.String()
		}
		toID := toInclusive.String()

		rows, err := db.QueryContext(ctx, selectOrphanRangeEventsQuery, sessionID.String(), fromID, fromID, toID)
		if err != nil {
			return xerrors.Errorf("failed to list orphan range events: %w", err)
		}
		defer func() { _ = rows.Close() }()

		loaded := model.SessionOrphanMaterial{
			KindCounts: map[string]int{},
		}
		for rows.Next() {
			var id, kind, createdAtRaw string
			if err := rows.Scan(&id, &kind, &createdAtRaw); err != nil {
				return xerrors.Errorf("failed to scan orphan range event: %w", err)
			}
			createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
			if err != nil {
				return xerrors.Errorf("invalid orphan event created_at %q: %w", createdAtRaw, err)
			}
			if loaded.EventCount == 0 {
				loaded.FirstCreatedAt = createdAt
			}
			loaded.LastCreatedAt = createdAt
			loaded.KindCounts[kind]++
			loaded.EventCount++
		}
		if err := rows.Err(); err != nil {
			return xerrors.Errorf("failed to iterate orphan range events: %w", err)
		}
		if loaded.EventCount == 0 {
			return xerrors.Errorf(
				"orphan range %s..%s for session %s has no events: %w",
				fromID, toID, sessionID, model.ErrInvalidSessionOrphanRange,
			)
		}

		cmdRows, err := db.QueryContext(ctx, selectOrphanRangeCommandsQuery, sessionID.String(), fromID, fromID, toID)
		if err != nil {
			return xerrors.Errorf("failed to list orphan range commands: %w", err)
		}
		defer func() { _ = cmdRows.Close() }()
		for cmdRows.Next() {
			var eventID string
			if err := cmdRows.Scan(&eventID); err != nil {
				return xerrors.Errorf("failed to scan orphan range command event id: %w", err)
			}
			// command_text is codec-managed; decode through the shared boundary so
			// a zstd-compressed audit never surfaces as binary garbage here.
			command, err := hydrateAuditPayload(ctx, db, eventID, "command")
			if err != nil {
				return xerrors.Errorf("failed to decode orphan range command for %s: %w", eventID, err)
			}
			if command.Valid {
				loaded.Commands = append(loaded.Commands, command.String)
			}
		}
		if err := cmdRows.Err(); err != nil {
			return xerrors.Errorf("failed to iterate orphan range commands: %w", err)
		}
		material = loaded
		return nil
	})
	if err != nil {
		return model.SessionOrphanMaterial{}, err
	}
	return material, nil
}

// openForDiscovery opens the connection both orphan read paths use. They only
// ask questions, so the read-only connection is right for either caller: it
// avoids the journal-mode and schema side effects gc --dry-run promises not to
// have, and works on a store that is read-only at the filesystem level. The
// apply path writes through the refinement repository, not through here.
func (d *SessionOrphanRangeDatasource) openForDiscovery(ctx context.Context) (*sql.DB, error) {
	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for orphan range read: %w", err)
	}
	return db, nil
}

func (d *SessionOrphanRangeDatasource) listRecorded(ctx context.Context, db *sql.DB) ([]*model.SessionOrphanRange, error) {
	// Marker table is a front-loaded shortcut added in migration 47. Stores
	// still at 46 (or any pre-47 schema) have no session_orphan_ranges; treat
	// that as zero recorded rows so marker-free discovery can still run.
	// gc --dry-run skips Initialize/migrate, so this path must not fail on a
	// missing table.
	exists, err := databaseTableExists(ctx, db, "session_orphan_ranges")
	if err != nil {
		return nil, xerrors.Errorf("failed to probe session_orphan_ranges presence: %w", err)
	}
	if !exists {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx, listRecordedOrphanRangesQuery)
	if err != nil {
		return nil, xerrors.Errorf("failed to list recorded orphan ranges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*model.SessionOrphanRange
	for rows.Next() {
		orphan, err := scanSessionOrphanRange(rows)
		if err != nil {
			return nil, err
		}
		if orphan == nil {
			continue
		}
		result = append(result, orphan)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate recorded orphan ranges: %w", err)
	}
	return result, nil
}

// endedOrStaleCandidate is one row of the oldest-first ended/stale page: the
// session id and the earliest orphaned event's time, both as returned by the
// SQL (raw text and its normalized form, used as the next page's cursor).
type endedOrStaleCandidate struct {
	sessionID         types.SessionID
	earliestEventTime time.Time
	earliestEventNorm string
}

func (d *SessionOrphanRangeDatasource) listEndedOrStalePage(
	ctx context.Context,
	db *sql.DB,
	cutoff string,
	cursorNorm string,
	cursorSessionID string,
	limit int,
) ([]endedOrStaleCandidate, error) {
	rows, err := db.QueryContext(
		ctx, listEndedOrStaleSessionsForOrphanQuery,
		cutoff, cutoff, cursorNorm, cursorSessionID, limit,
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to list ended or stale sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []endedOrStaleCandidate
	for rows.Next() {
		var id, earliestRaw string
		if err := rows.Scan(&id, &earliestRaw); err != nil {
			return nil, xerrors.Errorf("failed to scan ended or stale session row: %w", err)
		}
		sessionID, err := types.SessionIDFrom(id)
		if err != nil {
			return nil, xerrors.Errorf("invalid stored session id: %w", err)
		}
		earliest, err := time.Parse(time.RFC3339Nano, earliestRaw)
		if err != nil {
			return nil, xerrors.Errorf("invalid orphan earliest_event_time %q: %w", earliestRaw, err)
		}
		result = append(result, endedOrStaleCandidate{
			sessionID:         sessionID,
			earliestEventTime: earliest,
			earliestEventNorm: normalizeRFC3339NanoForCompare(earliestRaw),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate ended or stale sessions: %w", err)
	}
	return result, nil
}

func (d *SessionOrphanRangeDatasource) sessionEndedOrStale(
	ctx context.Context,
	db *sql.DB,
	sessionID types.SessionID,
	cutoff string,
) (bool, error) {
	// Reuse the ended/stale listing predicate for one session via direct checks.
	var ended int
	err := db.QueryRowContext(ctx, selectSessionIsEndedQuery, sessionID.String()).Scan(&ended)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("failed to look up session end state: %w", err)
	}
	if ended == 1 {
		return true, nil
	}
	// Stale: started before cutoff and no activity at or after cutoff.
	var stale int
	err = db.QueryRowContext(ctx, selectSessionIsStaleQuery, cutoff, cutoff, sessionID.String()).Scan(&stale)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("failed to evaluate session staleness: %w", err)
	}
	return stale == 1, nil
}

func (d *SessionOrphanRangeDatasource) refinementCovers(
	ctx context.Context,
	db *sql.DB,
	sessionID types.SessionID,
	toEventID types.EventID,
) (bool, error) {
	var coversTo string
	err := db.QueryRowContext(ctx, `
SELECT covers_to_event_id
  FROM session_refinements
 WHERE session_id = ?
`, sessionID.String()).Scan(&coversTo)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("failed to look up session refinement coverage: %w", err)
	}
	if coversTo == toEventID.String() {
		return true, nil
	}
	// covers_to must be at or after to_event_id under canonical order.
	var after int
	err = db.QueryRowContext(ctx, selectEventIsStrictlyAfterQuery, toEventID.String(), coversTo).Scan(&after)
	if errors.Is(err, sql.ErrNoRows) {
		// Missing bound events: fail closed and treat as not covered so gc can
		// re-evaluate after data is repaired.
		return false, nil
	}
	if err != nil {
		return false, xerrors.Errorf("failed to compare refinement coverage: %w", err)
	}
	return after == 1, nil
}

func (d *SessionOrphanRangeDatasource) gapPastCoverage(
	ctx context.Context,
	db *sql.DB,
	sessionID types.SessionID,
	earliestEventTime time.Time,
	now time.Time,
) (*model.SessionOrphanRange, bool, error) {
	var latestID string
	err := db.QueryRowContext(ctx, selectLatestEventIDForSessionQuery, sessionID.String()).Scan(&latestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, xerrors.Errorf("failed to resolve latest event: %w", err)
	}
	latest, err := types.EventIDFrom(latestID)
	if err != nil {
		return nil, false, xerrors.Errorf("invalid latest event id: %w", err)
	}

	var coversTo string
	err = db.QueryRowContext(ctx, `
SELECT covers_to_event_id
  FROM session_refinements
 WHERE session_id = ?
`, sessionID.String()).Scan(&coversTo)
	if errors.Is(err, sql.ErrNoRows) {
		// No refinement: the whole session is orphaned.
		orphan, buildErr := model.NewSessionOrphanRange(
			sessionID,
			types.None[types.EventID](),
			latest,
			now.UTC(),
			earliestEventTime,
		)
		if buildErr != nil {
			return nil, false, xerrors.Errorf("failed to build whole-session orphan range: %w", buildErr)
		}
		return orphan, true, nil
	}
	if err != nil {
		return nil, false, xerrors.Errorf("failed to look up covers_to: %w", err)
	}
	if coversTo == latest.String() {
		return nil, false, nil
	}
	// Material remains only when latest is strictly after covers_to.
	var after int
	err = db.QueryRowContext(ctx, selectEventIsStrictlyAfterQuery, coversTo, latest.String()).Scan(&after)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, xerrors.Errorf("failed to compare covers_to with latest: %w", err)
	}
	if after != 1 {
		return nil, false, nil
	}
	from, err := types.EventIDFrom(coversTo)
	if err != nil {
		return nil, false, xerrors.Errorf("invalid covers_to event id: %w", err)
	}
	orphan, err := model.NewSessionOrphanRange(
		sessionID,
		types.Some(from),
		latest,
		now.UTC(),
		earliestEventTime,
	)
	if err != nil {
		return nil, false, xerrors.Errorf("failed to build orphan range past coverage: %w", err)
	}
	return orphan, true, nil
}

type orphanRangeScanner interface {
	Scan(dest ...any) error
}

func scanSessionOrphanRange(row orphanRangeScanner) (*model.SessionOrphanRange, error) {
	var (
		sessionID   string
		fromEventID string
		toEventID   string
		observedRaw string
		earliestRaw sql.NullString
	)
	if err := row.Scan(&sessionID, &fromEventID, &toEventID, &observedRaw, &earliestRaw); err != nil {
		return nil, xerrors.Errorf("failed to scan session orphan range: %w", err)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observedRaw)
	if err != nil {
		return nil, xerrors.Errorf("invalid orphan observed_at %q: %w", observedRaw, err)
	}
	if !earliestRaw.Valid {
		// The range's material was removed after the marker was recorded
		// (e.g. compacted away). Nothing left to fold or delete; drop it
		// rather than surface a range with an unknown earliest time.
		return nil, nil
	}
	earliestEventTime, err := time.Parse(time.RFC3339Nano, earliestRaw.String)
	if err != nil {
		return nil, xerrors.Errorf("invalid orphan earliest_event_time %q: %w", earliestRaw.String, err)
	}
	from := types.None[types.EventID]()
	if fromEventID != "" {
		parsed, parseErr := types.EventIDFrom(fromEventID)
		if parseErr != nil {
			return nil, xerrors.Errorf("invalid orphan from_event_id: %w", parseErr)
		}
		from = types.Some(parsed)
	}
	to, err := types.EventIDFrom(toEventID)
	if err != nil {
		return nil, xerrors.Errorf("invalid orphan to_event_id: %w", err)
	}
	orphan, err := model.SessionOrphanRangeOf(types.SessionID(sessionID), from, to, observedAt, earliestEventTime)
	if err != nil {
		return nil, xerrors.Errorf("failed to restore session orphan range: %w", err)
	}
	return orphan, nil
}
