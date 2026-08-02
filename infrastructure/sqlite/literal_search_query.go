package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

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
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("literal search budget must be positive")
	}
	query := apptypes.CharacterizeLiteralQuery(request.Criteria.Query())
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
	if err := tx.QueryRowContext(ctx, `SELECT generation_id,high_water,query_revision,state FROM literal_search_projection_state WHERE singleton=1`).Scan(&generation, &highWater, &revision, &state); err != nil {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("read literal search projection: %w", err)
	}
	// The source sequence is authoritative and can be ahead of a missing/stale
	// candidate projection. Bind cursors to its high-water for honest coverage.
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&highWater); err != nil {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("read literal search source high-water: %w", err)
	}
	criteriaHash := literalCriteriaHash(request.Criteria, query.Canonical(), request.Budget)
	var after int64
	if request.Continuation != "" {
		cursor, decodeErr := apptypes.DecodeLiteralSearchCursor(request.Continuation)
		if decodeErr != nil || cursor.Validate(criteriaHash, generation, highWater, revision) != nil {
			return apptypes.LiteralSearchPage{}, apptypes.ErrLiteralSearchCursorMismatch
		}
		after = cursor.LastSequence
	}
	useFingerprints := state == "complete" && query.Filterable()
	rows, err := queryLiteralSources(ctx, tx, request.Criteria, after, request.Budget.SourceRows+1, generation, query.Fingerprints(), useFingerprints)
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
	var usedStored, usedDecoded int64
	last := after
	partialReason := ""
	for i, s := range sources {
		if i >= request.Budget.SourceRows {
			partialReason = "source_rows"
			break
		}
		if usedStored+s.stored > request.Budget.StoredBytes {
			partialReason = "stored_bytes"
			break
		}
		if usedDecoded+s.decoded > request.Budget.DecodedBytes {
			partialReason = "decoded_bytes"
			break
		}
		ok, matchErr := decodedEventSearchMatch(ctx, tx, s.id, query.Canonical())
		if matchErr != nil {
			return apptypes.LiteralSearchPage{}, matchErr
		}
		usedStored += s.stored
		usedDecoded += s.decoded
		last = s.sequence
		if ok {
			matched = append(matched, s.id)
			if len(matched) >= request.Criteria.Limit() {
				if i+1 < len(sources) {
					partialReason = "result_limit"
				}
				break
			}
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
	events, err := hydrateBoundedEvents(ctx, tx, metadata, int(min(request.Budget.DecodedBytes, int64(^uint(0)>>1))))
	if err != nil {
		return apptypes.LiteralSearchPage{}, err
	}
	tier := apptypes.LiteralSearchTierBoundedVerification
	if useFingerprints {
		tier = apptypes.LiteralSearchTierFingerprint
	}
	page := apptypes.LiteralSearchPage{Events: events, Tier: tier, Coverage: apptypes.LiteralSearchCoverage{ProcessedSources: last, HighWater: highWater, Complete: complete}, PartialReason: partialReason}
	if !complete {
		page.Continuation, err = (apptypes.LiteralSearchCursor{Version: apptypes.LiteralSearchCursorVersion, LastSequence: last, CriteriaHash: criteriaHash, Generation: generation, HighWater: highWater, QueryRevision: revision}).Encode()
		if err != nil {
			return apptypes.LiteralSearchPage{}, xerrors.Errorf("encode literal search continuation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return apptypes.LiteralSearchPage{}, xerrors.Errorf("finish tiered literal search: %w", err)
	}
	return page, nil
}

func queryLiteralSources(ctx context.Context, tx *sql.Tx, criteria apptypes.EventSearchCriteria, after int64, limit int, generation string, fingerprints []string, useFingerprints bool) (*sql.Rows, error) {
	var b strings.Builder
	b.WriteString(`SELECT q.sequence,e.id,COALESCE(e.body_encoded_bytes,length(e.body),0)+COALESCE(a.command_encoded_bytes,length(a.command_text),0)+COALESCE(a.input_encoded_bytes,length(a.input_text),0)+COALESCE(a.output_encoded_bytes,length(a.output_text),0),COALESCE(e.body_plaintext_bytes,length(e.body),0)+COALESCE(a.command_plaintext_bytes,length(a.command_text),0)+COALESCE(a.input_plaintext_bytes,length(a.input_text),0)+COALESCE(a.output_plaintext_bytes,length(a.output_text),0) FROM search_projection_source_sequence q JOIN events e ON e.id=q.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE q.sequence>?`)
	args := []any{after}
	args = appendEventSearchFilters(&b, args, criteria)
	if useFingerprints {
		b.WriteString(" AND (NOT EXISTS(SELECT 1 FROM literal_search_fingerprints known WHERE known.generation_id=? AND known.event_id=e.id AND known.fingerprint_version=1) OR (SELECT COUNT(DISTINCT matched.fingerprint) FROM literal_search_fingerprints matched WHERE matched.generation_id=? AND matched.event_id=e.id AND matched.fingerprint_version=1 AND matched.fingerprint IN (")
		args = append(args, generation, generation)
		for i, fingerprint := range fingerprints {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('?')
			args = append(args, []byte(fingerprint))
		}
		b.WriteString("))=?)")
		args = append(args, len(fingerprints))
	}
	b.WriteString(" ORDER BY q.sequence LIMIT ?")
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, xerrors.Errorf("query literal verification sources: %w", err)
	}
	return rows, nil
}

func literalCriteriaHash(c apptypes.EventSearchCriteria, canonical string, budget apptypes.LiteralSearchBudget) string {
	raw := fmt.Sprintf("v1\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%d\x00%d\x00%d", canonical, c.Workspace(), c.SessionID(), c.Client(), c.Agent(), c.Kind(), c.From().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), c.To().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), c.FailuresOnly(), budget.SourceRows, budget.StoredBytes, budget.DecodedBytes)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
