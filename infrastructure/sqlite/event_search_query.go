package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const hydrateEventSearchCandidatesQuery = `
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.body, e.body_availability, e.source_hook, e.created_at,
       ca.command_wrapper, ca.command_name,
       ca.input_truncated, ca.output_truncated,
       ca.input_original_bytes, ca.output_original_bytes, ca.exit_code, ca.failed, ca.failure_reason, ca.output_metadata
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

type eventSearchQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// searchProjectionReadReady reports whether the bounded search projection is
// complete with a published active generation. Session-tier search uses this
// to decide whether projection session rows may be returned.
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
		// Missing table on pre-038 stores is treated as not ready.
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

// eventSearchFTSPhrase quotes a query for FTS5 MATCH against projection FTS
// tables (trigram, ASCII-folded). Kept as a package helper for rebuild tests
// and any remaining projection MATCH probes.
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
	var hasOutputMetadata int
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('command_audits') WHERE name='output_metadata')`).Scan(&hasOutputMetadata); err != nil {
		return nil, xerrors.Errorf("inspect command audit output metadata column: %w", err)
	}
	rows, err := queryer.QueryContext(ctx, auditQueryWithOptionalOutputMetadata(hydrateEventSearchCandidatesQuery, hasOutputMetadata != 0), encoded)
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
