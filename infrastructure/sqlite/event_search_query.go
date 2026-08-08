package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const eventSearchLegacyCandidateLimit = 10_000

const hydrateEventSearchCandidatesQuery = `
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.body, e.body_availability, e.source_hook, e.created_at,
       ca.command_text, ca.command_wrapper, ca.command_name,
       ca.input_text, ca.output_text, ca.input_truncated, ca.output_truncated,
       ca.input_original_bytes, ca.output_original_bytes, ca.exit_code, ca.failed, ca.failure_reason
  FROM events e
  LEFT JOIN command_audits ca ON ca.event_id = e.id
 WHERE e.id IN (SELECT CAST(value AS TEXT) FROM json_each(?))
 ORDER BY e.created_at_norm DESC, e.id DESC`

// Candidate selection is shared with full search, but metadata hydration uses
// the persistent projection so it cannot open body-bearing event rows.
const hydrateEventSearchMetadataCandidatesQuery = `
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       e.command_audit_event_id, e.command_exit_code, e.command_failed
  FROM event_metadata_projection e
 WHERE e.id IN (SELECT CAST(value AS TEXT) FROM json_each(?))
 ORDER BY e.created_at_norm DESC, e.id DESC`

// eventSearchLegacyContentPredicate deliberately remains the same literal LIKE
// projection used before FTS. It is evaluated only after a metadata-only
// candidate count proves the scope contains at most
// eventSearchLegacyCandidateLimit rows.
const eventSearchLegacyContentPredicate = `
(
    (CASE
        WHEN e.body_availability = 'unavailable_retention' THEN ''
        WHEN json_valid(e.body)
             AND json_type(e.body, '$') = 'object'
             AND json_type(e.body, '$.blocks') = 'array'
             AND NOT EXISTS (
                 SELECT 1
                   FROM json_each(json_extract(e.body, '$.blocks'))
                  WHERE typeof(json_extract(value, '$.type')) != 'text'
                     OR typeof(json_extract(value, '$.text')) != 'text'
             )
        THEN COALESCE(
            (
                SELECT group_concat(json_extract(value, '$.text'), X'0A0A')
                  FROM json_each(json_extract(e.body, '$.blocks'))
                 WHERE json_extract(value, '$.type') = 'text'
            ),
            ''
        )
        ELSE e.body
     END) LIKE ? ESCAPE '\' OR
    COALESCE(a.command_text, '') LIKE ? ESCAPE '\' OR
    COALESCE(a.input_text, '') LIKE ? ESCAPE '\' OR
    COALESCE(a.output_text, '') LIKE ? ESCAPE '\'
)`

type eventSearchQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func eventSearchSchemaAvailable(ctx context.Context, queryer eventSearchQueryer) (bool, error) {
	var count int
	if err := queryer.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM sqlite_master
		 WHERE type = 'table'
		   AND name IN ('event_search_documents', 'event_search_fts', 'event_search_backfill_state')`,
	).Scan(&count); err != nil {
		return false, xerrors.Errorf("failed to inspect event search schema: %w", err)
	}
	return count == 3, nil
}

func selectEventSearchCandidateIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
) ([]string, error) {
	queryValue := strings.TrimSpace(criteria.Query())
	if queryValue == "" {
		return queryStructuralEventIDs(ctx, queryer, criteria)
	}

	// Both index families tokenise with trigram, so neither can match a query
	// shorter than three characters. Short queries therefore resolve through the
	// bounded decoded scan whichever tier is authoritative; routing them into the
	// projection would return silently-empty results.
	if utf8.RuneCountInString(queryValue) < 3 {
		return selectShortQueryCandidateIDs(ctx, queryer, criteria, queryValue)
	}

	// Bounded projection is authoritative when a complete generation is active.
	// Stores that have never rebuilt (every field store today) fall through to
	// the legacy path silently — never error solely because the projection is idle.
	projectionReady, err := searchProjectionReadReady(ctx, queryer)
	if err != nil {
		return nil, err
	}
	if projectionReady {
		ids, servedByProjection, err := selectProjectionEventSearchCandidateIDs(ctx, queryer, criteria, queryValue)
		if err != nil {
			return nil, err
		}
		if servedByProjection {
			return ids, nil
		}
		// The projection is too far behind to answer within the tail budget.
		// Fall back rather than return a knowingly incomplete page.
	}
	return selectLegacyEventSearchCandidateIDs(ctx, queryer, criteria, queryValue)
}

// selectProjectionEventSearchCandidateIDs answers from the bounded projection
// plus the events appended since the generation completed.
//
// A complete generation is a snapshot: migration 038's insert trigger records
// the source sequence of a new event but does not add it to the projection, and
// an insert does not drift a complete generation. Reading the projection alone
// would therefore stop returning anything recorded after the last rebuild —
// silently, and for as long as nobody rebuilds. The tail closes that window.
//
// Returns false when the tail exceeds the candidate budget, meaning the
// generation is too stale to answer cheaply and the caller should fall back.
func selectProjectionEventSearchCandidateIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, bool, error) {
	highWater, err := searchProjectionHighWater(ctx, queryer)
	if err != nil {
		return nil, false, err
	}
	tailIDs, withinBudget, err := selectProjectionTailEventIDs(ctx, queryer, criteria, queryValue, highWater)
	if err != nil {
		return nil, false, err
	}
	if !withinBudget {
		return nil, false, nil
	}

	// Any projection row ranked below this position cannot reach the requested
	// page even after the tail is merged in, so fetching more is wasted work.
	fetch := criteria.Limit() + len(tailIDs)
	if criteria.PageAnchor().IsZero() {
		fetch += criteria.Offset()
	}
	query, args := buildProjectionFTSEventIDsQuery(criteria, queryValue, fetch)
	projectionIDs, err := collectEventSearchIDs(ctx, queryer, query, args, "projection event search")
	if err != nil {
		return nil, false, err
	}

	merged := mergeEventSearchCandidateIDs(tailIDs, projectionIDs)
	if len(merged) == 0 {
		return []string{}, true, nil
	}
	ordered, err := orderEventSearchCandidatePage(ctx, queryer, criteria, merged)
	if err != nil {
		return nil, false, err
	}
	return ordered, true, nil
}

func searchProjectionHighWater(ctx context.Context, queryer eventSearchQueryer) (int64, error) {
	var highWater int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT high_water
		  FROM search_projection_state
		 WHERE singleton = 1`,
	).Scan(&highWater); err != nil {
		return 0, xerrors.Errorf("failed to read search projection high water: %w", err)
	}
	return highWater, nil
}

// selectProjectionTailEventIDs literal-matches the events appended after the
// active generation completed. It decodes payloads through the same adapter
// boundary as normal reads, so it stays correct once bodies are compressed.
func selectProjectionTailEventIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
	highWater int64,
) ([]string, bool, error) {
	var builder strings.Builder
	builder.WriteString("SELECT e.id FROM events e INDEXED BY ")
	builder.WriteString(eventSearchScopeIndex(criteria))
	builder.WriteString(` LEFT JOIN command_audits a ON a.event_id=e.id
		 WHERE EXISTS (
		       SELECT 1
		         FROM search_projection_source_sequence q
		        WHERE q.event_id = e.id
		          AND q.sequence > ?
		 )`)
	args := []any{highWater}
	args = appendEventSearchFilters(&builder, args, criteria)
	builder.WriteString(" ORDER BY e.created_at_norm DESC, e.id DESC LIMIT ?")
	args = append(args, eventSearchLegacyCandidateLimit+1)

	ids, err := collectEventSearchIDs(ctx, queryer, builder.String(), args, "projection tail event search")
	if err != nil {
		return nil, false, err
	}
	if len(ids) > eventSearchLegacyCandidateLimit {
		return nil, false, nil
	}

	matched := make([]string, 0)
	for _, eventID := range ids {
		matches, err := decodedEventSearchMatch(ctx, queryer, eventID, queryValue)
		if err != nil {
			return nil, false, err
		}
		if matches {
			matched = append(matched, eventID)
		}
	}
	return matched, true, nil
}

func mergeEventSearchCandidateIDs(left []string, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	merged := make([]string, 0, len(left)+len(right))
	for _, ids := range [][]string{left, right} {
		for _, id := range ids {
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			merged = append(merged, id)
		}
	}
	return merged
}

// orderEventSearchCandidatePage restores the canonical ordering and applies the
// requested page across candidates that came from two different sources.
func orderEventSearchCandidatePage(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	ids []string,
) ([]string, error) {
	encoded, err := marshalEventSearchCandidateIDs(ids)
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	builder.WriteString(`
		SELECT e.id
		  FROM events e
		 WHERE e.id IN (SELECT CAST(value AS TEXT) FROM json_each(?))`)
	args := []any{encoded}
	appendEventSearchOrderAndPage(&builder, criteria)
	args = appendEventSearchPageArgs(args, criteria)
	return collectEventSearchIDs(ctx, queryer, builder.String(), args, "projection page ordering")
}

// selectShortQueryCandidateIDs answers queries no trigram index can serve. It
// reads only events and command_audits, so it stays correct after the legacy
// search family is retired. The bounded-scope guard is the same one the legacy
// path applies, because an unbounded decoded scan is what it exists to prevent.
func selectShortQueryCandidateIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, error) {
	if !hasBoundedLegacySearchScope(criteria) {
		return nil, &queryservice.EventSearchUnavailableError{
			Reason:         queryservice.EventSearchUnavailableScopeTooBroad,
			CandidateLimit: eventSearchLegacyCandidateLimit,
		}
	}
	candidateCount, err := countBoundedLegacyCandidates(ctx, queryer, criteria)
	if err != nil {
		return nil, err
	}
	if candidateCount > eventSearchLegacyCandidateLimit {
		return nil, &queryservice.EventSearchUnavailableError{
			Reason:         queryservice.EventSearchUnavailableScopeTooBroad,
			CandidateLimit: eventSearchLegacyCandidateLimit,
			CandidateCount: candidateCount,
		}
	}
	return queryDecodedLegacyEventIDs(ctx, queryer, criteria, queryValue)
}

// selectLegacyEventSearchCandidateIDs is the migration-032 index path used by
// SearchLegacyPage and as the automatic fallback when no projection generation
// is active. It must not consult search_projection_*.
func selectLegacyEventSearchCandidateIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, error) {
	complete, err := eventSearchBackfillComplete(ctx, queryer)
	if err != nil {
		return nil, err
	}
	shortQuery := utf8.RuneCountInString(queryValue) < 3
	missingProjection := false
	nonIdentityPayload := false
	if complete {
		if hasBoundedLegacySearchScope(criteria) {
			missingProjection, err = boundedSearchProjectionMissing(ctx, queryer, criteria)
			if err != nil {
				return nil, err
			}
		}
		nonIdentityPayload, err = boundedSearchHasNonIdentityPayload(ctx, queryer, criteria)
		if err != nil {
			return nil, err
		}
	}
	needsLegacyCompleteness := shortQuery || !complete || missingProjection || nonIdentityPayload
	if needsLegacyCompleteness {
		if !hasBoundedLegacySearchScope(criteria) {
			reason := queryservice.EventSearchUnavailableScopeTooBroad
			if !shortQuery {
				reason = queryservice.EventSearchUnavailableIndexIncomplete
			}
			return nil, &queryservice.EventSearchUnavailableError{
				Reason:         reason,
				CandidateLimit: eventSearchLegacyCandidateLimit,
			}
		}
		candidateCount, err := countBoundedLegacyCandidates(ctx, queryer, criteria)
		if err != nil {
			return nil, err
		}
		if candidateCount > eventSearchLegacyCandidateLimit {
			return nil, &queryservice.EventSearchUnavailableError{
				Reason:         queryservice.EventSearchUnavailableScopeTooBroad,
				CandidateLimit: eventSearchLegacyCandidateLimit,
				CandidateCount: candidateCount,
			}
		}
	}

	if needsLegacyCompleteness {
		return queryDecodedLegacyEventIDs(ctx, queryer, criteria, queryValue)
	}
	if complete {
		return queryFTSEventIDs(ctx, queryer, criteria, queryValue)
	}
	return queryIncompleteFTSEventIDs(ctx, queryer, criteria, queryValue)
}

// searchProjectionReadReady reports whether the bounded search projection is
// the active event-search authority: state is complete and a non-empty
// active_generation_id is published.
func searchProjectionReadReady(ctx context.Context, queryer eventSearchQueryer) (bool, error) {
	var state string
	var activeGenerationID sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT state, active_generation_id
		  FROM search_projection_state
		 WHERE singleton = 1`,
	).Scan(&state, &activeGenerationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		// Missing table on pre-038 stores is treated as not ready so callers
		// keep using the legacy index without a hard failure.
		if isMissingSQLiteObject(err) {
			return false, nil
		}
		return false, xerrors.Errorf("failed to read search projection state: %w", err)
	}
	if state != "complete" {
		return false, nil
	}
	if !activeGenerationID.Valid {
		return false, nil
	}
	return strings.TrimSpace(activeGenerationID.String) != "", nil
}

func isMissingSQLiteObject(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") || strings.Contains(message, "no such column")
}

func boundedSearchHasNonIdentityPayload(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
) (bool, error) {
	var codecColumns int
	if err := queryer.QueryRowContext(ctx, `
		SELECT count(*) FROM pragma_table_info('events')
		 WHERE name='body_codec'`).Scan(&codecColumns); err != nil {
		return false, xerrors.Errorf("failed to inspect event payload codec column: %w", err)
	}
	if codecColumns == 0 {
		return false, nil
	}
	if !hasBoundedLegacySearchScope(criteria) {
		found, err := globalNonIdentityPayload(ctx, queryer)
		if err != nil {
			return false, xerrors.Errorf("failed to inspect global payload codecs: %w", err)
		}
		return found, nil
	}
	var builder strings.Builder
	builder.WriteString(`SELECT EXISTS(
		SELECT 1 FROM events e LEFT JOIN command_audits a ON a.event_id=e.id
		 WHERE (COALESCE(e.body_codec, 'identity') <> 'identity'
		    OR COALESCE(a.command_codec, 'identity') <> 'identity'
		    OR COALESCE(a.input_codec, 'identity') <> 'identity'
		    OR COALESCE(a.output_codec, 'identity') <> 'identity')`)
	args := appendEventSearchFilters(&builder, nil, criteria)
	builder.WriteString(")")
	var found int
	if err := queryer.QueryRowContext(ctx, builder.String(), args...).Scan(&found); err != nil {
		return false, xerrors.Errorf("failed to inspect bounded payload codecs: %w", err)
	}
	return found != 0, nil
}

func boundedSearchProjectionMissing(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
) (bool, error) {
	var builder strings.Builder
	builder.WriteString(`SELECT EXISTS(
		SELECT 1 FROM events e LEFT JOIN command_audits a ON a.event_id=e.id
		 WHERE NOT EXISTS (SELECT 1 FROM event_search_documents d WHERE d.event_id=e.id)`)
	args := appendEventSearchFilters(&builder, nil, criteria)
	builder.WriteString(")")
	var missing int
	if err := queryer.QueryRowContext(ctx, builder.String(), args...).Scan(&missing); err != nil {
		return false, xerrors.Errorf("failed to inspect bounded event search projection: %w", err)
	}
	return missing != 0, nil
}

func hasBoundedLegacySearchScope(criteria apptypes.EventSearchCriteria) bool {
	return strings.TrimSpace(criteria.Workspace().String()) != "" ||
		strings.TrimSpace(criteria.SessionID().String()) != "" ||
		(!criteria.From().IsZero() && !criteria.To().IsZero())
}

func countBoundedLegacyCandidates(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
) (int, error) {
	var builder strings.Builder
	builder.WriteString("SELECT e.id FROM events e INDEXED BY ")
	builder.WriteString(eventSearchScopeIndex(criteria))
	builder.WriteString(" LEFT JOIN command_audits a ON a.event_id = e.id WHERE 1 = 1")
	args := appendEventSearchFilters(&builder, nil, criteria)
	builder.WriteString(" ORDER BY e.created_at_norm DESC, e.id DESC LIMIT ?")
	args = append(args, eventSearchLegacyCandidateLimit+1)

	rows, err := queryer.QueryContext(ctx, builder.String(), args...)
	if err != nil {
		return 0, xerrors.Errorf("failed to count bounded legacy event search candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, xerrors.Errorf("failed to iterate bounded legacy event search candidates: %w", err)
	}
	return count, nil
}

func eventSearchScopeIndex(criteria apptypes.EventSearchCriteria) string {
	hasWorkspace := strings.TrimSpace(criteria.Workspace().String()) != ""
	hasSession := strings.TrimSpace(criteria.SessionID().String()) != ""
	switch {
	case hasWorkspace && hasSession:
		return "idx_events_workspace_session_created_at_norm_id_desc"
	case hasWorkspace:
		return "idx_events_workspace_created_at_norm_id_desc"
	case hasSession:
		return "idx_events_session_created_at_norm_id_desc"
	default:
		return "idx_events_created_at_norm_id_desc"
	}
}

func queryStructuralEventIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
) ([]string, error) {
	var builder strings.Builder
	builder.WriteString(`
		SELECT e.id
		  FROM events e
		  LEFT JOIN command_audits a ON a.event_id = e.id
		 WHERE 1 = 1`)
	args := appendEventSearchFilters(&builder, nil, criteria)
	appendEventSearchOrderAndPage(&builder, criteria)
	args = appendEventSearchPageArgs(args, criteria)
	return collectEventSearchIDs(ctx, queryer, builder.String(), args, "structural event search")
}

func queryLegacyEventIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, error) {
	var builder strings.Builder
	builder.WriteString(`
		SELECT e.id
		  FROM events e
		  LEFT JOIN command_audits a ON a.event_id = e.id
		 WHERE 1 = 1`)
	args := appendEventSearchFilters(&builder, nil, criteria)
	builder.WriteString(" AND ")
	builder.WriteString(eventSearchLegacyContentPredicate)
	likeQuery := "%" + escapeLikeQuery(queryValue) + "%"
	args = append(args, likeQuery, likeQuery, likeQuery, likeQuery)
	appendEventSearchOrderAndPage(&builder, criteria)
	args = appendEventSearchPageArgs(args, criteria)
	return collectEventSearchIDs(ctx, queryer, builder.String(), args, "bounded legacy event search")
}

// queryDecodedLegacyEventIDs keeps fallback search independent of the physical
// payload representation. SQL selects only a bounded set of IDs; payloads are
// then decoded through the same adapter boundary as normal reads before
// literal matching and pagination are applied.
func queryDecodedLegacyEventIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, error) {
	var builder strings.Builder
	builder.WriteString("SELECT e.id FROM events e INDEXED BY ")
	builder.WriteString(eventSearchScopeIndex(criteria))
	builder.WriteString(" LEFT JOIN command_audits a ON a.event_id=e.id WHERE 1=1")
	args := appendEventSearchFilters(&builder, nil, criteria)
	builder.WriteString(" ORDER BY e.created_at_norm DESC, e.id DESC LIMIT ?")
	args = append(args, eventSearchLegacyCandidateLimit+1)
	ids, err := collectEventSearchIDs(ctx, queryer, builder.String(), args, "bounded decoded event search")
	if err != nil {
		return nil, err
	}

	matched := make([]string, 0)
	for _, eventID := range ids {
		matches, err := decodedEventSearchMatch(ctx, queryer, eventID, queryValue)
		if err != nil {
			return nil, err
		}
		if matches {
			matched = append(matched, eventID)
		}
	}
	start := criteria.Offset()
	if start >= len(matched) {
		return []string{}, nil
	}
	end := min(start+criteria.Limit(), len(matched))
	return matched[start:end], nil
}

func decodedEventSearchMatch(
	ctx context.Context,
	queryer eventSearchQueryer,
	eventID string,
	queryValue string,
) (bool, error) {
	var availability string
	if err := queryer.QueryRowContext(ctx, `SELECT body_availability FROM events WHERE id=?`, eventID).Scan(&availability); err != nil {
		return false, xerrors.Errorf("read event search body availability: %w", err)
	}
	parts := make([]string, 0, 4)
	if availability != "unavailable_retention" {
		body, err := loadEventPlaintext(ctx, queryer, eventID)
		if err != nil {
			return false, xerrors.Errorf("decode event search body: %w", err)
		}
		visible, _ := visibleEventBody(string(body), types.BodyAvailability(availability))
		parts = append(parts, visible)
	}
	for _, column := range []string{"command", "input", "output"} {
		value, err := hydrateAuditPayload(ctx, queryer, eventID, column)
		if err != nil {
			return false, err
		}
		if value.Valid {
			parts = append(parts, value.String)
		}
	}
	needle := foldSearchASCII(queryValue)
	for _, part := range parts {
		if strings.Contains(foldSearchASCII(part), needle) {
			return true, nil
		}
	}
	return false, nil
}

func foldSearchASCII(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
}

func queryFTSEventIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, error) {
	query, args := buildFTSEventIDsQuery(criteria, queryValue)
	return collectEventSearchIDs(ctx, queryer, query, args, "indexed event search")
}

func buildFTSEventIDsQuery(
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) (string, []any) {
	var builder strings.Builder
	builder.WriteString(`
		SELECT e.id
		  FROM event_search_fts
		  JOIN event_search_documents d
		    ON d.search_document_id = event_search_fts.rowid
		  JOIN events e ON e.id = d.event_id
		  LEFT JOIN command_audits a ON a.event_id = e.id
		 WHERE event_search_fts MATCH ?`)
	args := []any{eventSearchFTSPhrase(queryValue)}
	args = appendEventSearchFilters(&builder, args, criteria)
	appendEventSearchOrderAndPage(&builder, criteria)
	args = appendEventSearchPageArgs(args, criteria)
	return builder.String(), args
}

// buildProjectionFTSEventIDsQuery takes an explicit fetch bound instead of the
// requested page, because the caller merges these rows with the tail before
// paging.
func buildProjectionFTSEventIDsQuery(
	criteria apptypes.EventSearchCriteria,
	queryValue string,
	fetch int,
) (string, []any) {
	var builder strings.Builder
	builder.WriteString(`
		SELECT e.id
		  FROM search_projection_recent_fts
		  JOIN search_projection_recent_documents d
		    ON d.document_id = search_projection_recent_fts.rowid
		  JOIN events e ON e.id = d.event_id
		  LEFT JOIN command_audits a ON a.event_id = e.id
		 WHERE search_projection_recent_fts MATCH ?
		   AND d.generation_id = (
		         SELECT active_generation_id
		           FROM search_projection_state
		          WHERE singleton = 1
		       )`)
	args := []any{eventSearchFTSPhrase(queryValue)}
	args = appendEventSearchFilters(&builder, args, criteria)
	builder.WriteString(" ORDER BY e.created_at_norm DESC, e.id DESC LIMIT ?")
	args = append(args, fetch)
	return builder.String(), args
}

func queryIncompleteFTSEventIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, error) {
	query, args := buildIncompleteFTSEventIDsQuery(criteria, queryValue)
	return collectEventSearchIDs(ctx, queryer, query, args, "incomplete indexed event search")
}

func buildIncompleteFTSEventIDsQuery(criteria apptypes.EventSearchCriteria, queryValue string) (string, []any) {
	var builder strings.Builder
	builder.WriteString(`
		WITH matching_event_ids(event_id) AS (
		    SELECT d.event_id
		      FROM event_search_fts
		      JOIN event_search_documents d
		        ON d.search_document_id = event_search_fts.rowid
		      JOIN events e ON e.id = d.event_id
		      LEFT JOIN command_audits a ON a.event_id = e.id
		     WHERE event_search_fts MATCH ?`)
	args := []any{eventSearchFTSPhrase(queryValue)}
	args = appendEventSearchFilters(&builder, args, criteria)
	builder.WriteString(`
		    UNION
		    SELECT e.id
		      FROM events e
		      LEFT JOIN command_audits a ON a.event_id = e.id
		     WHERE NOT EXISTS (
		           SELECT 1
		             FROM event_search_documents missing_document
		            WHERE missing_document.event_id = e.id
		     )`)
	args = appendEventSearchFilters(&builder, args, criteria)
	builder.WriteString(" AND ")
	builder.WriteString(eventSearchLegacyContentPredicate)
	likeQuery := "%" + escapeLikeQuery(queryValue) + "%"
	args = append(args, likeQuery, likeQuery, likeQuery, likeQuery)
	builder.WriteString(`
		)
		SELECT e.id
		  FROM matching_event_ids matching
		  JOIN events e ON e.id = matching.event_id`)
	appendEventSearchOrderAndPage(&builder, criteria)
	args = appendEventSearchPageArgs(args, criteria)
	return builder.String(), args
}

func appendEventSearchFilters(
	builder *strings.Builder,
	args []any,
	criteria apptypes.EventSearchCriteria,
) []any {
	if value := strings.TrimSpace(criteria.Workspace().String()); value != "" {
		builder.WriteString(" AND e.workspace = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(criteria.SessionID().String()); value != "" {
		builder.WriteString(" AND e.session_id = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(criteria.Client().String()); value != "" {
		builder.WriteString(" AND e.client = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(criteria.Agent().String()); value != "" {
		builder.WriteString(" AND e.agent = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(criteria.Kind().String()); value != "" {
		builder.WriteString(" AND e.kind = ?")
		args = append(args, value)
	}
	if !criteria.From().IsZero() {
		builder.WriteString(" AND e.created_at_norm >= ?")
		args = append(args, normalizeRFC3339NanoForCompare(formatTimestamp(criteria.From())))
	}
	if !criteria.To().IsZero() {
		builder.WriteString(" AND e.created_at_norm < ?")
		args = append(args, normalizeRFC3339NanoForCompare(formatTimestamp(criteria.To())))
	}
	if anchor := criteria.PageAnchor(); !anchor.IsZero() {
		createdAt := normalizeRFC3339NanoForCompare(formatTimestamp(anchor.CreatedAt()))
		builder.WriteString(" AND (e.created_at_norm, e.id) < (?, ?)")
		args = append(args, createdAt, anchor.EventID().String())
	}
	if criteria.FailuresOnly() {
		builder.WriteString(" AND (a.failed = 1 OR (a.exit_code IS NOT NULL AND a.exit_code != 0))")
	}
	return args
}

func appendEventSearchOrderAndPage(builder *strings.Builder, criteria apptypes.EventSearchCriteria) {
	builder.WriteString(" ORDER BY e.created_at_norm DESC, e.id DESC LIMIT ?")
	if criteria.PageAnchor().IsZero() {
		builder.WriteString(" OFFSET ?")
	}
}

func appendEventSearchPageArgs(args []any, criteria apptypes.EventSearchCriteria) []any {
	args = append(args, criteria.Limit())
	if criteria.PageAnchor().IsZero() {
		args = append(args, criteria.Offset())
	}
	return args
}

func collectEventSearchIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	query string,
	args []any,
	operation string,
) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("failed to query %s candidates: %w", operation, err)
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			return nil, xerrors.Errorf("failed to scan %s candidate: %w", operation, err)
		}
		ids = append(ids, eventID)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate %s candidates: %w", operation, err)
	}
	return ids, nil
}

func eventSearchFTSPhrase(query string) string {
	lowerASCII := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, strings.TrimSpace(query))
	return `"` + strings.ReplaceAll(lowerASCII, `"`, `""`) + `"`
}

func marshalEventSearchCandidateIDs(ids []string) (string, error) {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return "", xerrors.Errorf("failed to encode event search candidate IDs: %w", err)
	}
	return string(encoded), nil
}

func hydrateEventSearchCandidates(
	ctx context.Context,
	queryer eventSearchQueryer,
	ids []string,
) ([]*model.Event, error) {
	if len(ids) == 0 {
		return []*model.Event{}, nil
	}
	encoded, err := marshalEventSearchCandidateIDs(ids)
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, hydrateEventSearchCandidatesQuery, encoded)
	if err != nil {
		return nil, xerrors.Errorf("failed to hydrate event search candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]*model.Event, 0, len(ids))
	for rows.Next() {
		event, err := scanListedEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore event search candidate: %w", err)
		}
		event, err = hydrateListedEvent(ctx, queryer, event)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate event search candidates: %w", err)
	}
	return events, nil
}

func hydrateEventSearchMetadataCandidates(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	ids []string,
) ([]apptypes.EventMetadata, error) {
	if len(ids) == 0 {
		return []apptypes.EventMetadata{}, nil
	}
	encoded, err := marshalEventSearchCandidateIDs(ids)
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, hydrateEventSearchMetadataCandidatesQuery, encoded)
	if err != nil {
		return nil, xerrors.Errorf("failed to hydrate event metadata search candidates: %w", err)
	}
	return collectEventMetadata(rows, criteria.Limit(), "event metadata search candidates")
}
