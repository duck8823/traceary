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
	// Empty queries are structural-only searches. They never traverse the
	// literal projection, so they are answered before its state is even read.
	if strings.TrimSpace(criteria.Query()) == "" {
		candidateIDs, queryErr := queryStructuralEventIDs(ctx, tx, criteria)
		if queryErr != nil {
			return nil, queryErr
		}
		return hydrateEventSearchMetadataCandidates(ctx, tx, criteria, candidateIDs)
	}
	generation, err := usableLiteralFingerprintGeneration(ctx, tx)
	if err != nil {
		return nil, err
	}
	return d.searchTieredTopKMetadataTx(ctx, tx, criteria, generation)
}

// usableLiteralFingerprintGeneration reports the generation whose fingerprints
// may be trusted as a pre-filter, or "" when none may be.
//
// There is no projection state in which the tiered path cannot answer. The
// walk below decides every candidate by decoding it; fingerprints only let it
// skip decoding rows they prove cannot match. So an unusable projection costs
// work, not correctness, and this returns "" rather than refusing.
//
// The conditions are the ones a coherent, finished generation satisfies.
// Literal 'stale' is included because migration 039 flips it on every
// events/command_audits write, including ordinary appends whose absent
// fingerprints already fail open. Mutation of an already-projected row is
// caught by the bounded side instead: the search_projection_complete_*
// triggers set bounded state='drifted' and clear active_generation_id, so
// such a generation stops being usable here and the walk decodes live content.
//
// A rebuild in progress is not answered from the previous generation's
// fingerprints, and the cost of that is real: searches decode every candidate
// until the rebuild completes, so a wide query on a large store exhausts the
// budget and reports index_incomplete.
//
// Those rows do survive. Start only repoints the literal singleton
// (search_projection_rebuild.go:82), fingerprint writes are additive inserts
// keyed by generation (:624), and old-generation rows are removed only in the
// new generation's cleanup phase (:445, :633). So the previous generation is
// still identifiable through active_generation_id and still readable.
//
// Using it safely needs more than identifying it: the rebuild must not have
// entered cleanup, and no canonical mutation may have landed since the old
// generation completed, or its fingerprints describe rows that have changed.
// That is a distinct piece of work with its own failure modes, tracked
// separately, so this deliberately takes the slow, always-correct path.
func usableLiteralFingerprintGeneration(ctx context.Context, tx *sql.Tx) (string, error) {
	var literalState, literalGeneration, boundedState, boundedGeneration string
	if err := tx.QueryRowContext(ctx, `SELECT generation_id,state FROM literal_search_projection_state WHERE singleton=1`).Scan(&literalGeneration, &literalState); err != nil {
		return "", xerrors.Errorf("read tiered authority projection state: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(active_generation_id,''),state FROM search_projection_state WHERE singleton=1`).Scan(&boundedGeneration, &boundedState); err != nil {
		return "", xerrors.Errorf("read tiered authority projection state: %w", err)
	}
	literalReady := literalState == "complete" || literalState == "stale"
	if !literalReady || boundedState != "complete" || literalGeneration == "" || literalGeneration != boundedGeneration {
		return "", nil
	}
	return literalGeneration, nil
}

// searchTieredTopKMetadataTx separates the source-work budget from the public
// result limit. Candidates are visited in the public order across the full
// source sequence (including rows appended after the generation completed), so
// only offset+limit matches need to be retained; broad queries do not become
// unavailable merely because more than MaxLiteralSearchLimit rows match.
// Post-cutover rows are newest, so they are examined first and consume budget first.
func (d *EventDatasource) searchTieredTopKMetadataTx(ctx context.Context, tx *sql.Tx, criteria apptypes.EventSearchCriteria, generation string) ([]apptypes.EventMetadata, error) {
	if err := validateSearchCriteriaForAuthority(criteria); err != nil {
		return nil, err
	}
	query := apptypes.CharacterizeLiteralQuery(criteria.Query())
	var builder strings.Builder
	// The candidate set is events, not search_projection_source_sequence.
	// That table is the projection's own checkpoint ledger: it is populated by
	// the migration-038 insert trigger and, for rows that predate it, only by
	// the rebuild's inventory phase (search_projection_rebuild.go:271). An
	// upgraded store that has never completed a generation therefore has no
	// sequence row for any of its history, and joining through it would drop
	// that history from every search. It also outlives deleted events, since
	// nothing removes a row when its event goes away.
	//
	// There is no sequence bound either: post-completion rows form the live
	// tail and must participate in the same ordered walk as projected events.
	// The fingerprint pre-filter below is fail-open for events with no rows
	// for the generation, so tail rows are decided on their decoded content.
	builder.WriteString(`SELECT e.id,COALESCE(e.body_encoded_bytes,length(e.body),0)+COALESCE(a.command_encoded_bytes,length(a.command_text),0)+COALESCE(a.input_encoded_bytes,length(a.input_text),0)+COALESCE(a.output_encoded_bytes,length(a.output_text),0),COALESCE(e.body_plaintext_bytes,length(e.body),0)+COALESCE(a.command_plaintext_bytes,length(a.command_text),0)+COALESCE(a.input_plaintext_bytes,length(a.input_text),0)+COALESCE(a.output_plaintext_bytes,length(a.output_text),0) FROM events e LEFT JOIN command_audits a ON a.event_id=e.id WHERE 1=1`)
	args := []any{}
	args = appendEventSearchFilters(&builder, args, criteria)
	// An empty generation means no trustworthy fingerprints exist for this
	// read. Skip the clause outright rather than relying on it matching
	// nothing: three correlated subqueries per candidate row are not free,
	// and the fail-open behaviour should not depend on an invariant about
	// which generation ids can appear in literal_search_fingerprints.
	if generation != "" && query.Filterable() {
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
