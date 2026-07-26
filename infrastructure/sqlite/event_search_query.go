package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

const eventSearchLegacyCandidateLimit = 10_000

const hydrateEventSearchCandidatesQuery = `
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.body, e.body_availability, e.source_hook, e.created_at
  FROM events e
 WHERE e.id IN (SELECT CAST(value AS TEXT) FROM json_each(?))
 ORDER BY e.created_at_norm DESC, e.id DESC`

// This result projection intentionally contains no e.body reference. Candidate
// selection is shared with full search, but metadata hydration cannot
// accidentally materialize stored event bodies.
const hydrateEventSearchMetadataCandidatesQuery = `
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       e.body_original_bytes, e.body_stored_bytes,
       e.body_ingest_truncated, e.body_storage_truncated,
       e.body_metadata_version,
       a.event_id, a.exit_code, a.failed
  FROM events e
  LEFT JOIN command_audits a ON a.event_id = e.id
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

	complete, err := eventSearchBackfillComplete(ctx, queryer)
	if err != nil {
		return nil, err
	}
	shortQuery := utf8.RuneCountInString(queryValue) < 3
	needsLegacyCompleteness := shortQuery || !complete
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

	if shortQuery {
		return queryLegacyEventIDs(ctx, queryer, criteria, queryValue)
	}
	if complete {
		return queryFTSEventIDs(ctx, queryer, criteria, queryValue)
	}
	return queryIncompleteFTSEventIDs(ctx, queryer, criteria, queryValue)
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

func queryIncompleteFTSEventIDs(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	queryValue string,
) ([]string, error) {
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
	return collectEventSearchIDs(ctx, queryer, builder.String(), args, "incomplete indexed event search")
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
		event, err := scanEvent(rows)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore event search candidate: %w", err)
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
