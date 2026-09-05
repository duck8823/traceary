package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

const twoTierFallbackScanSelect = `
SELECT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace,
       e.source_hook, e.created_at,
       CASE WHEN length(CAST(e.body AS BLOB)) <= ? THEN e.body END,
       a.event_id,
       CASE WHEN length(CAST(a.command_text AS BLOB)) <= ? THEN a.command_text END,
       CASE WHEN length(CAST(a.input_text AS BLOB)) <= ? THEN a.input_text END,
       CASE WHEN length(CAST(a.output_text AS BLOB)) <= ? THEN a.output_text END,
       s.started_at
  FROM events e
  LEFT JOIN command_audits a ON a.event_id = e.id
  LEFT JOIN sessions s ON s.session_id = e.session_id
 WHERE 1 = 1`

type scannedPayload struct {
	stored []byte
}

func (p *scannedPayload) destinations() []any {
	return []any{&p.stored}
}

func (p scannedPayload) decode(rowID, field string) ([]byte, error) {
	_ = rowID
	_ = field
	return p.stored, nil
}

func buildTwoTierFallbackScanQuery(criteria apptypes.EventSearchCriteria) (string, []any) {
	var builder strings.Builder
	builder.WriteString(twoTierFallbackScanSelect)
	args := []any{maxStoredPayloadBytes, maxStoredPayloadBytes, maxStoredPayloadBytes, maxStoredPayloadBytes}
	args = appendEventSearchFilters(&builder, args, twoTierFilterCriteria(criteria))
	builder.WriteString(" ORDER BY e.created_at_norm DESC, e.id DESC")
	return builder.String(), args
}

func scanTwoTierFallback(
	ctx context.Context,
	queryer eventSearchQueryer,
	criteria apptypes.EventSearchCriteria,
	phrase apptypes.SearchPhrase,
	refinementSessionIDs map[string]struct{},
	includeSessionRows bool,
) ([]twoTierHit, []twoTierHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, xerrors.Errorf("two-tier fallback scan cancelled: %w", err)
	}
	sqlText, args := buildTwoTierFallbackScanQuery(criteria)
	rows, err := queryer.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, nil, xerrors.Errorf("query two-tier fallback scan: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]twoTierHit, 0)
	sessions := make([]twoTierHit, 0)
	seenSessions := make(map[string]struct{})
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, nil, xerrors.Errorf("two-tier fallback scan cancelled: %w", err)
		}
		matched, eventHit, sessionHit, scanErr := scanTwoTierFallbackRow(
			rows, phrase, refinementSessionIDs, seenSessions, includeSessionRows,
		)
		if scanErr != nil {
			return nil, nil, scanErr
		}
		if !matched {
			continue
		}
		events = append(events, eventHit)
		if includeSessionRows && sessionHit.sessionID != "" {
			seenSessions[sessionHit.sessionID] = struct{}{}
			sessions = append(sessions, sessionHit)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, xerrors.Errorf("iterate two-tier fallback scan: %w", err)
	}
	return events, sessions, nil
}

func scanTwoTierFallbackRow(
	rows *sql.Rows,
	phrase apptypes.SearchPhrase,
	refinementSessionIDs map[string]struct{},
	seenSessions map[string]struct{},
	includeSessionRows bool,
) (bool, twoTierHit, twoTierHit, error) {
	var (
		eventID, kind, client, agent, sessionID, workspace string
		createdAtRaw                                       string
		sourceHook                                         sql.NullString
		body                                               scannedPayload
		auditEventID                                       sql.NullString
		command, input, output                             scannedPayload
		startedAtRaw                                       sql.NullString
	)
	dest := []any{
		&eventID, &kind, &client, &agent, &sessionID, &workspace,
		&sourceHook, &createdAtRaw,
	}
	dest = append(dest, body.destinations()...)
	dest = append(dest, &auditEventID)
	dest = append(dest, command.destinations()...)
	dest = append(dest, input.destinations()...)
	dest = append(dest, output.destinations()...)
	dest = append(dest, &startedAtRaw)
	if err := rows.Scan(dest...); err != nil {
		return false, twoTierHit{}, twoTierHit{}, xerrors.Errorf("scan two-tier fallback row: %w", err)
	}
	_ = kind
	_ = client
	_ = agent
	_ = workspace
	_ = sourceHook.String

	matched, err := twoTierFallbackRowMatches(eventID, body, auditEventID.Valid, command, input, output, phrase)
	if err != nil {
		return false, twoTierHit{}, twoTierHit{}, err
	}
	if !matched {
		return false, twoTierHit{}, twoTierHit{}, nil
	}

	createdAt, err := parseTwoTierTime(createdAtRaw, "events.created_at")
	if err != nil {
		return false, twoTierHit{}, twoTierHit{}, err
	}
	eventHit := twoTierHit{
		resultTime: createdAt,
		tier:       apptypes.SearchHitTierFallback,
		kind:       twoTierResultKindEvent,
		sessionID:  sessionID,
		eventID:    eventID,
	}

	var sessionHit twoTierHit
	if !includeSessionRows || sessionID == "" {
		return true, eventHit, sessionHit, nil
	}
	if _, seen := seenSessions[sessionID]; seen {
		return true, eventHit, sessionHit, nil
	}
	if _, suppressed := refinementSessionIDs[sessionID]; suppressed {
		return true, eventHit, sessionHit, nil
	}
	if !startedAtRaw.Valid || strings.TrimSpace(startedAtRaw.String) == "" {
		return true, eventHit, sessionHit, nil
	}
	startedAt, err := parseTwoTierTime(startedAtRaw.String, "sessions.started_at")
	if err != nil {
		return false, twoTierHit{}, twoTierHit{}, err
	}
	sessionHit = twoTierHit{
		resultTime: startedAt,
		tier:       apptypes.SearchHitTierFallback,
		kind:       twoTierResultKindSession,
		sessionID:  sessionID,
		session: apptypes.SearchSessionHitOf(
			types.SessionID(sessionID),
			"",
			0,
			startedAt,
		).WithTier(apptypes.SearchHitTierFallback),
	}
	return true, eventHit, sessionHit, nil
}

func twoTierFallbackRowMatches(
	eventID string,
	body scannedPayload,
	hasAudit bool,
	command, input, output scannedPayload,
	phrase apptypes.SearchPhrase,
) (bool, error) {
	plain, err := body.decode(eventID, "body")
	if err != nil {
		return false, xerrors.Errorf("decode two-tier event %s body: %w", eventID, err)
	}
	visible, _ := visibleEventBody(string(plain))
	if phrase.Matches(visible) {
		return true, nil
	}
	if !hasAudit {
		return false, nil
	}
	for _, part := range []struct {
		payload scannedPayload
		field   string
	}{
		{command, "command"},
		{input, "input"},
		{output, "output"},
	} {
		plain, err := part.payload.decode(eventID, part.field)
		if err != nil {
			return false, xerrors.Errorf("decode two-tier audit %s %s: %w", eventID, part.field, err)
		}
		if phrase.Matches(string(plain)) {
			return true, nil
		}
	}
	return false, nil
}
