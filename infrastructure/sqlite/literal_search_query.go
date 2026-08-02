package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
)

var _ queryservice.TieredEventSearchQuery = (*EventDatasource)(nil)

// SearchLiteralPage is an additive bounded verification lane. Migration 39's
// fingerprints are deliberately treated as an optional candidate accelerator:
// until a complete matching generation exists, every source remains eligible.
func (d *EventDatasource) SearchLiteralPage(ctx context.Context, request apptypes.LiteralSearchRequest) (apptypes.LiteralSearchPage, error) {
	if !request.Budget.Valid() {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("%w: budget must be positive", apptypes.ErrLiteralSearchInvalidRequest)
	}
	if request.Criteria.Limit() <= 0 {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("%w: limit must be positive", apptypes.ErrLiteralSearchInvalidRequest)
	}
	if !request.Criteria.From().IsZero() && !request.Criteria.To().IsZero() && request.Criteria.From().After(request.Criteria.To()) {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("%w: from must not be after to", apptypes.ErrLiteralSearchInvalidRequest)
	}
	if request.Criteria.Offset() != 0 || !request.Criteria.PageAnchor().IsZero() {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("%w: offset and page anchor are unsupported", apptypes.ErrLiteralSearchInvalidRequest)
	}
	query := apptypes.CharacterizeLiteralQuery(request.Criteria.Query())
	if len(query.Canonical()) > apptypes.MaxLiteralSearchQueryBytes {
		return apptypes.LiteralSearchPage{}, apptypes.ErrLiteralSearchQueryTooLarge
	}
	if query.Canonical() == "" {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("literal search query must not be empty")
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "tiered literal search")
	if err != nil {
		return apptypes.LiteralSearchPage{}, err
	}
	defer closeEventProjectionRead(db, tx)

	var generation, state string
	var highWater, revision int64
	var cursorKey []byte
	if err := tx.QueryRowContext(ctx, `SELECT generation_id,high_water,query_revision,state,cursor_key FROM literal_search_projection_state WHERE singleton=1`).Scan(&generation, &highWater, &revision, &state, &cursorKey); err != nil {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("read literal search projection: %w", err)
	}
	// The source sequence is authoritative and can be ahead of a missing/stale
	// candidate projection. Bind cursors to its high-water for honest coverage.
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&highWater); err != nil {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("read literal search source high-water: %w", err)
	}
	criteriaHash := literalCriteriaHash(request.Criteria, query.Canonical(), request.BodyRuneLimit)
	var after int64
	if request.Continuation != "" {
		cursor, decodeErr := apptypes.DecodeAuthenticatedLiteralSearchCursor(request.Continuation, cursorKey)
		if decodeErr != nil || cursor.Validate(criteriaHash, generation, highWater, revision) != nil {
			return apptypes.LiteralSearchPage{}, apptypes.ErrLiteralSearchCursorMismatch
		}
		after = cursor.LastSequence
	}
	useFingerprints := state == "complete" && query.Filterable()
	rows, err := queryLiteralSources(ctx, tx, after, request.Budget.SourceRows+1)
	if err != nil {
		return apptypes.LiteralSearchPage{}, err
	}
	defer func() { _ = rows.Close() }()
	type source struct {
		sequence        int64
		id              string
		stored, decoded int64
	}
	sources := make([]source, 0, request.Budget.SourceRows+1)
	for rows.Next() {
		var s source
		if err := rows.Scan(&s.sequence, &s.id, &s.stored, &s.decoded); err != nil {
			return apptypes.LiteralSearchPage{}, xerrors.Errorf("scan literal verification source: %w", err)
		}
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("iterate literal verification sources: %w", err)
	}

	matched := make([]string, 0, request.Criteria.Limit())
	matchContinuations := make([]string, 0, request.Criteria.Limit())
	var usedStored, usedDecoded int64
	last := after
	var examined int64
	partialReason := ""
	for i, s := range sources {
		if i >= request.Budget.SourceRows {
			partialReason = "source_rows"
			break
		}
		examined++
		before := last
		eligible, eligibleErr := literalSourceEligible(ctx, tx, s.id, request.Criteria, generation, query.Fingerprints(), useFingerprints)
		if eligibleErr != nil {
			return apptypes.LiteralSearchPage{}, eligibleErr
		}
		if !eligible {
			last = s.sequence
			continue
		}
		if usedStored+s.stored > request.Budget.StoredBytes {
			if usedStored == 0 {
				return apptypes.LiteralSearchPage{}, &apptypes.SearchProjectionOversizeError{Class: "stored_bytes", Bytes: s.stored, Limit: request.Budget.StoredBytes}
			}
			partialReason = "stored_bytes"
			break
		}
		if usedDecoded+s.decoded > request.Budget.DecodedBytes {
			if usedDecoded == 0 {
				return apptypes.LiteralSearchPage{}, &apptypes.SearchProjectionOversizeError{Class: "decoded_bytes", Bytes: s.decoded, Limit: request.Budget.DecodedBytes}
			}
			partialReason = "decoded_bytes"
			break
		}
		ok, matchErr := decodedEventSearchMatch(ctx, tx, s.id, query.Canonical())
		if matchErr != nil {
			return apptypes.LiteralSearchPage{}, matchErr
		}
		usedStored += s.stored
		usedDecoded += s.decoded
		if ok {
			if usedStored+s.stored > request.Budget.StoredBytes || usedDecoded+s.decoded > request.Budget.DecodedBytes {
				if before == after {
					return apptypes.LiteralSearchPage{}, &apptypes.SearchProjectionOversizeError{Class: "verified_hydration_bytes", Bytes: max(usedStored+s.stored, usedDecoded+s.decoded), Limit: max(request.Budget.StoredBytes, request.Budget.DecodedBytes)}
				}
				partialReason = "verified_hydration_bytes"
				break
			}
			usedStored += s.stored
			usedDecoded += s.decoded
			resume, encodeErr := (apptypes.LiteralSearchCursor{Version: apptypes.LiteralSearchCursorVersion, LastSequence: before, CriteriaHash: criteriaHash, Generation: generation, HighWater: highWater, QueryRevision: revision}).EncodeAuthenticated(cursorKey)
			if encodeErr != nil {
				return apptypes.LiteralSearchPage{}, xerrors.Errorf("encode match continuation: %w", encodeErr)
			}
			matched = append(matched, s.id)
			matchContinuations = append(matchContinuations, resume)
			last = s.sequence
			if len(matched) >= request.Criteria.Limit() {
				if i+1 < len(sources) {
					partialReason = "result_limit"
				}
				break
			}
		} else {
			last = s.sequence
		}
	}
	complete := last >= highWater
	if !complete && partialReason == "" {
		partialReason = "source_rows"
	}
	metadata, err := hydrateEventSearchMetadataCandidates(ctx, tx, request.Criteria, matched)
	if err != nil {
		return apptypes.LiteralSearchPage{}, err
	}
	metadata = orderLiteralMetadata(metadata, matched)
	bodyLimit := request.BodyRuneLimit
	if bodyLimit <= 0 {
		bodyLimit = 500
	}
	events, err := hydrateBoundedEvents(ctx, tx, metadata, bodyLimit)
	if err != nil {
		return apptypes.LiteralSearchPage{}, err
	}
	tier := apptypes.LiteralSearchTierBoundedVerification
	if useFingerprints {
		tier = apptypes.LiteralSearchTierFingerprint
	}
	page := apptypes.LiteralSearchPage{Events: events, Tier: tier, Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: last, ExaminedSources: examined, HighWater: highWater, Complete: complete}, PartialReason: partialReason, MatchContinuations: matchContinuations}
	if !complete {
		page.Continuation, err = (apptypes.LiteralSearchCursor{Version: apptypes.LiteralSearchCursorVersion, LastSequence: last, CriteriaHash: criteriaHash, Generation: generation, HighWater: highWater, QueryRevision: revision}).EncodeAuthenticated(cursorKey)
		if err != nil {
			return apptypes.LiteralSearchPage{}, xerrors.Errorf("encode literal search continuation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("finish tiered literal search: %w", err)
	}
	return page, nil
}

func orderLiteralMetadata(metadata []apptypes.EventMetadata, ids []string) []apptypes.EventMetadata {
	byID := make(map[string]apptypes.EventMetadata, len(metadata))
	for _, item := range metadata {
		byID[item.EventID().String()] = item
	}
	ordered := make([]apptypes.EventMetadata, 0, len(metadata))
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			ordered = append(ordered, item)
		}
	}
	return ordered
}

func queryLiteralSources(ctx context.Context, tx *sql.Tx, after int64, limit int) (*sql.Rows, error) {
	var b strings.Builder
	b.WriteString(`SELECT q.sequence,COALESCE(e.id,''),COALESCE(e.body_encoded_bytes,length(e.body),0)+COALESCE(a.command_encoded_bytes,length(a.command_text),0)+COALESCE(a.input_encoded_bytes,length(a.input_text),0)+COALESCE(a.output_encoded_bytes,length(a.output_text),0),COALESCE(e.body_plaintext_bytes,length(e.body),0)+COALESCE(a.command_plaintext_bytes,length(a.command_text),0)+COALESCE(a.input_plaintext_bytes,length(a.input_text),0)+COALESCE(a.output_plaintext_bytes,length(a.output_text),0) FROM search_projection_source_sequence q LEFT JOIN events e ON e.id=q.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE q.sequence>?`)
	args := []any{after}
	b.WriteString(" ORDER BY q.sequence LIMIT ?")
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, xerrors.Errorf("query literal verification sources: %w", err)
	}
	return rows, nil
}

func literalSourceEligible(ctx context.Context, tx *sql.Tx, eventID string, criteria apptypes.EventSearchCriteria, generation string, fingerprints []string, useFingerprints bool) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	var b strings.Builder
	b.WriteString("SELECT EXISTS(SELECT 1 FROM events e LEFT JOIN command_audits a ON a.event_id=e.id WHERE e.id=?")
	args := []any{eventID}
	args = appendEventSearchFilters(&b, args, criteria)
	if useFingerprints {
		b.WriteString(" AND (NOT EXISTS(SELECT 1 FROM literal_search_fingerprints known WHERE known.generation_id=? AND known.event_id=e.id AND known.fingerprint_version=1) OR (SELECT COUNT(DISTINCT matched.fingerprint) FROM literal_search_fingerprints matched WHERE matched.generation_id=? AND matched.event_id=e.id AND matched.fingerprint_version=1 AND matched.fingerprint IN (")
		args = append(args, generation, generation)
		for i, fp := range fingerprints {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('?')
			args = append(args, []byte(fp))
		}
		b.WriteString("))=?)")
		args = append(args, len(fingerprints))
	}
	b.WriteByte(')')
	var eligible int
	if err := tx.QueryRowContext(ctx, b.String(), args...).Scan(&eligible); err != nil {
		return false, xerrors.Errorf("classify literal source eligibility: %w", err)
	}
	return eligible != 0, nil
}

func literalCriteriaHash(c apptypes.EventSearchCriteria, canonical string, bodyLimit int) string {
	raw := fmt.Sprintf("v2\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%d\x00%d\x00%d", canonical, c.Workspace(), c.SessionID(), c.Client(), c.Agent(), c.Kind(), c.From().UTC().Format(time.RFC3339Nano), c.To().UTC().Format(time.RFC3339Nano), c.FailuresOnly(), c.Limit(), c.Offset(), bodyLimit)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
