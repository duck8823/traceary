//nolint:wrapcheck // SQLite search errors are contextualized at query boundaries.
package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
)

func validateSearchCriteriaForAuthority(criteria apptypes.EventSearchCriteria) error {
	if criteria.Limit() <= 0 {
		return xerrors.New("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return xerrors.New("offset must be greater than or equal to 0")
	}
	maxWindow := apptypes.DeepLiteralSearchBudget.SourceRows
	if criteria.Limit() > maxWindow || criteria.Offset() > maxWindow-criteria.Limit() {
		return xerrors.Errorf("offset plus limit must not exceed bounded search window %d", maxWindow)
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return xerrors.New("event page anchor cannot be combined with offset")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return xerrors.New("from must be earlier than to")
	}
	return nil
}

// searchMetadataByPersistedAuthority is the shared projection-neutral boundary
// for full, metadata, and bounded normal search surfaces.
func (d *EventDatasource) searchMetadataByPersistedAuthority(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "persisted search authority")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	metadata, err := d.searchMetadataByPersistedAuthorityTx(ctx, tx, criteria)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish persisted search: %w", err)
	}
	return metadata, nil
}

func (d *EventDatasource) searchMetadataByPersistedAuthorityTx(ctx context.Context, tx *sql.Tx, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	var authority string
	if err := tx.QueryRowContext(ctx, `SELECT authority FROM search_maintenance_control WHERE singleton=1`).Scan(&authority); err != nil {
		return nil, xerrors.Errorf("read explicit persisted search authority: %w", err)
	}
	if err := d.db.runSearchMaintenanceHook("authority-after-read"); err != nil {
		return nil, err
	}
	switch authority {
	case "legacy":
		available, err := eventSearchSchemaAvailable(ctx, tx)
		if err != nil {
			return nil, err
		}
		var ids []string
		if available {
			ids, err = selectEventSearchCandidateIDs(ctx, tx, criteria)
		} else {
			ids, err = queryLegacyEventIDs(ctx, tx, criteria, criteria.Query())
		}
		if err != nil {
			return nil, err
		}
		return hydrateEventSearchMetadataCandidates(ctx, tx, criteria, ids)
	case "tiered":
		return d.searchTieredMetadataTx(ctx, tx, criteria)
	default:
		return nil, xerrors.Errorf("unsupported persisted search authority %q", authority)
	}
}

func (d *EventDatasource) searchFullByPersistedAuthority(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	db, tx, err := d.beginEventProjectionRead(ctx, "full persisted search")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)
	var authority string
	if err = tx.QueryRowContext(ctx, `SELECT authority FROM search_maintenance_control WHERE singleton=1`).Scan(&authority); err != nil {
		return nil, xerrors.Errorf("read explicit persisted search authority: %w", err)
	}
	var events []*model.Event
	switch authority {
	case "legacy":
		available, schemaErr := eventSearchSchemaAvailable(ctx, tx)
		if schemaErr != nil {
			return nil, schemaErr
		}
		var ids []string
		if available {
			ids, err = selectEventSearchCandidateIDs(ctx, tx, criteria)
		} else {
			ids, err = queryLegacyEventIDs(ctx, tx, criteria, criteria.Query())
		}
		if err == nil {
			events, err = hydrateEventSearchCandidates(ctx, tx, ids)
		}
	case "tiered":
		var metadata []apptypes.EventMetadata
		metadata, err = d.searchTieredMetadataTx(ctx, tx, criteria)
		if err == nil {
			events, err = hydrateFullEventMetadata(ctx, tx, metadata)
		}
	default:
		return nil, xerrors.Errorf("unsupported persisted search authority %q", authority)
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, xerrors.Errorf("finish full persisted search: %w", err)
	}
	return events, nil
}

func (d *EventDatasource) searchTieredMetadataTx(ctx context.Context, tx *sql.Tx, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	if criteria.Offset() < 0 {
		return nil, xerrors.New("offset must be greater than or equal to 0")
	}
	var literalState, literalGeneration, boundedState, boundedGeneration string
	var literalHigh, sourceHigh int64
	if err := tx.QueryRowContext(ctx, `SELECT generation_id,high_water,state FROM literal_search_projection_state WHERE singleton=1`).Scan(&literalGeneration, &literalHigh, &literalState); err == nil {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(active_generation_id,''),state FROM search_projection_state WHERE singleton=1`).Scan(&boundedGeneration, &boundedState)
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM search_projection_source_sequence`).Scan(&sourceHigh)
		}
		if err != nil {
			return nil, xerrors.Errorf("read tiered authority projection state: %w", err)
		}
	} else {
		return nil, xerrors.Errorf("read tiered authority projection state: %w", err)
	}
	if literalState != "complete" || boundedState != "complete" || literalGeneration == "" || literalGeneration != boundedGeneration || literalHigh != sourceHigh {
		return nil, xerrors.New("tiered search projection is incomplete or stale")
	}
	// Empty queries are structural-only searches. They do not require literal
	// traversal and remain available after the legacy FTS objects are retired.
	if strings.TrimSpace(criteria.Query()) == "" {
		candidateIDs, queryErr := queryStructuralEventIDs(ctx, tx, criteria)
		if queryErr != nil {
			return nil, queryErr
		}
		return hydrateEventSearchMetadataCandidates(ctx, tx, criteria, candidateIDs)
	}
	return d.searchTieredTopKMetadataTx(ctx, tx, criteria, literalGeneration, literalHigh)
}

// searchTieredTopKMetadataTx separates the source-work budget from the public
// result limit. Candidates are visited in the public order, so only
// offset+limit matches need to be retained; broad queries do not become
// unavailable merely because more than MaxLiteralSearchLimit rows match.
func (d *EventDatasource) searchTieredTopKMetadataTx(ctx context.Context, tx *sql.Tx, criteria apptypes.EventSearchCriteria, generation string, highWater int64) ([]apptypes.EventMetadata, error) {
	query := apptypes.CharacterizeLiteralQuery(criteria.Query())
	var builder strings.Builder
	builder.WriteString(`SELECT e.id,COALESCE(e.body_encoded_bytes,length(e.body),0)+COALESCE(a.command_encoded_bytes,length(a.command_text),0)+COALESCE(a.input_encoded_bytes,length(a.input_text),0)+COALESCE(a.output_encoded_bytes,length(a.output_text),0),COALESCE(e.body_plaintext_bytes,length(e.body),0)+COALESCE(a.command_plaintext_bytes,length(a.command_text),0)+COALESCE(a.input_plaintext_bytes,length(a.input_text),0)+COALESCE(a.output_plaintext_bytes,length(a.output_text),0) FROM search_projection_source_sequence q JOIN events e ON e.id=q.event_id LEFT JOIN command_audits a ON a.event_id=e.id WHERE q.sequence<=?`)
	args := []any{highWater}
	args = appendEventSearchFilters(&builder, args, criteria)
	if query.Filterable() {
		fingerprints := query.Fingerprints()
		builder.WriteString(" AND (NOT EXISTS(SELECT 1 FROM literal_search_fingerprints known WHERE known.generation_id=? AND known.event_id=e.id AND known.fingerprint_version=1) OR (SELECT COUNT(DISTINCT matched.fingerprint) FROM literal_search_fingerprints matched WHERE matched.generation_id=? AND matched.event_id=e.id AND matched.fingerprint_version=1 AND matched.fingerprint IN (")
		args = append(args, generation, generation)
		for i, fingerprint := range fingerprints {
			if i > 0 {
				builder.WriteByte(',')
			}
			builder.WriteByte('?')
			args = append(args, []byte(fingerprint))
		}
		builder.WriteString("))=?)")
		args = append(args, len(fingerprints))
	}
	builder.WriteString(" ORDER BY e.created_at_norm DESC,e.id DESC LIMIT ?")
	args = append(args, apptypes.DeepLiteralSearchBudget.SourceRows+1)
	rows, err := tx.QueryContext(ctx, builder.String(), args...)
	if err != nil {
		return nil, xerrors.Errorf("query ordered tiered candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	wanted := criteria.Offset() + criteria.Limit()
	matched := make([]string, 0, wanted)
	var examined int
	var storedBytes, decodedBytes int64
	for rows.Next() {
		var id string
		var stored, decoded int64
		if err = rows.Scan(&id, &stored, &decoded); err != nil {
			return nil, xerrors.Errorf("scan ordered tiered candidate: %w", err)
		}
		if examined == apptypes.DeepLiteralSearchBudget.SourceRows || storedBytes+stored > apptypes.DeepLiteralSearchBudget.StoredBytes || decodedBytes+decoded > apptypes.DeepLiteralSearchBudget.DecodedBytes {
			return nil, &queryservice.EventSearchUnavailableError{Reason: queryservice.EventSearchUnavailableIndexIncomplete, CandidateLimit: apptypes.DeepLiteralSearchBudget.SourceRows}
		}
		examined++
		storedBytes += stored
		decodedBytes += decoded
		ok, matchErr := decodedEventSearchMatch(ctx, tx, id, query.Canonical())
		if matchErr != nil {
			return nil, matchErr
		}
		if !ok {
			continue
		}
		matched = append(matched, id)
		if len(matched) == wanted {
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, xerrors.Errorf("iterate ordered tiered candidates: %w", err)
	}
	start := min(criteria.Offset(), len(matched))
	return hydrateEventSearchMetadataCandidates(ctx, tx, criteria, matched[start:])
}
