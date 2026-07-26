package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

//go:embed sql/select_bounded_event_bodies.sql
var selectBoundedEventBodiesQuery string

//go:embed sql/select_canonical_event_bodies.sql
var selectCanonicalEventBodiesQuery string

var _ queryservice.EventBoundedQueryService = (*EventDatasource)(nil)

// HydrateBounded projects bodies for the exact metadata page supplied by the
// caller. It deliberately performs no membership query, so a continuation page
// cannot drift between an initial metadata probe and bounded hydration.
func (d *EventDatasource) HydrateBounded(
	ctx context.Context,
	metadata []apptypes.EventMetadata,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedBodyLimit(bodyRuneLimit); err != nil {
		return nil, err
	}
	if len(metadata) == 0 {
		return []apptypes.BoundedEvent{}, nil
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "bounded event hydration")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)

	events, err := hydrateBoundedEvents(ctx, tx, metadata, bodyRuneLimit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish bounded event hydration: %w", err)
	}
	return events, nil
}

// ListRecentBounded selects body-free event metadata first, then hydrates only
// the requested visible-text prefix for those IDs under the same read snapshot.
func (d *EventDatasource) ListRecentBounded(
	ctx context.Context,
	criteria apptypes.EventListCriteria,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedBodyLimit(bodyRuneLimit); err != nil {
		return nil, err
	}
	if err := validateMetadataListCriteria(criteria, false); err != nil {
		return nil, err
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "bounded event listing")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)

	rows, err := queryRecentEventMetadataTx(
		ctx,
		tx,
		criteria,
		formatMetadataOptionalTimestamp(criteria.From()),
		formatMetadataOptionalTimestamp(criteria.To()),
		criteria.Limit(),
		criteria.Offset(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to query bounded event metadata: %w", err)
	}
	metadata, err := collectEventMetadata(rows, criteria.Limit(), "bounded event metadata")
	if err != nil {
		return nil, err
	}
	events, err := hydrateBoundedEvents(ctx, tx, metadata, bodyRuneLimit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish bounded event listing: %w", err)
	}
	return events, nil
}

// SearchBounded uses the existing FTS/legacy search planner only to select
// event IDs, then projects bounded visible text from authoritative event rows.
func (d *EventDatasource) SearchBounded(
	ctx context.Context,
	criteria apptypes.EventSearchCriteria,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedBodyLimit(bodyRuneLimit); err != nil {
		return nil, err
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}
	if !criteria.From().IsZero() && !criteria.To().IsZero() && criteria.From().After(criteria.To()) {
		return nil, xerrors.Errorf("from must be earlier than to")
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "bounded event search")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)

	searchSchemaAvailable, err := eventSearchSchemaAvailable(ctx, tx)
	if err != nil {
		return nil, err
	}
	var candidateIDs []string
	if searchSchemaAvailable {
		candidateIDs, err = selectEventSearchCandidateIDs(ctx, tx, criteria)
	} else {
		// A store initialized before migration 32 retains the pre-FTS literal
		// search behavior. This query returns IDs only; bounded body hydration
		// below remains independent from candidate content matching.
		candidateIDs, err = queryLegacyEventIDs(ctx, tx, criteria, criteria.Query())
	}
	if err != nil {
		return nil, err
	}
	metadata, err := hydrateEventSearchMetadataCandidates(ctx, tx, criteria, candidateIDs)
	if err != nil {
		return nil, err
	}
	events, err := hydrateBoundedEvents(ctx, tx, metadata, bodyRuneLimit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish bounded event search: %w", err)
	}
	return events, nil
}

// GetContextBounded selects body-free context membership first and then
// hydrates bounded visible text under one read snapshot.
func (d *EventDatasource) GetContextBounded(
	ctx context.Context,
	criteria apptypes.EventContextCriteria,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedBodyLimit(bodyRuneLimit); err != nil {
		return nil, err
	}
	if criteria.Limit() <= 0 {
		return nil, xerrors.Errorf("limit must be greater than or equal to 1")
	}
	if criteria.Offset() < 0 {
		return nil, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	if !criteria.PageAnchor().IsZero() && criteria.Offset() != 0 {
		return nil, xerrors.Errorf("event page anchor cannot be combined with offset")
	}
	db, tx, err := d.beginEventProjectionRead(ctx, "bounded event context")
	if err != nil {
		return nil, err
	}
	defer closeEventProjectionRead(db, tx)

	workspace := criteria.Workspace().String()
	sessionID := criteria.SessionID().String()
	query, args := contextEventMetadataQuery(criteria, workspace, sessionID)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, xerrors.Errorf("failed to query bounded context metadata: %w", err)
	}
	metadata, err := collectEventMetadata(rows, criteria.Limit(), "bounded context metadata")
	if err != nil {
		return nil, err
	}
	events, err := hydrateBoundedEvents(ctx, tx, metadata, bodyRuneLimit)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, xerrors.Errorf("failed to finish bounded event context: %w", err)
	}
	return events, nil
}

// LoadCanonicalBodies performs the explicit list-only compatibility fallback.
// Callers must supply only canonical, available, response-untruncated IDs and
// must revalidate each returned envelope before exposing body blocks.
func (d *EventDatasource) LoadCanonicalBodies(
	ctx context.Context,
	eventIDs []types.EventID,
) (map[types.EventID]string, error) {
	if len(eventIDs) == 0 {
		return map[types.EventID]string{}, nil
	}
	encoded, err := marshalBoundedEventIDs(eventIDs)
	if err != nil {
		return nil, err
	}
	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, xerrors.Errorf("failed to open DB for canonical event bodies: %w", err)
	}
	defer closeMetadataResource(db)
	rows, err := db.QueryContext(ctx, selectCanonicalEventBodiesQuery, encoded)
	if err != nil {
		return nil, xerrors.Errorf("failed to query canonical event bodies: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Debug("failed to close canonical event body rows", "error", err)
		}
	}()
	bodies := make(map[types.EventID]string, len(eventIDs))
	for rows.Next() {
		var eventIDValue, body string
		if err := rows.Scan(&eventIDValue, &body); err != nil {
			return nil, xerrors.Errorf("failed to scan canonical event body: %w", err)
		}
		eventID, err := types.EventIDFrom(eventIDValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore canonical event ID: %w", err)
		}
		bodies[eventID] = body
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate canonical event bodies: %w", err)
	}
	return bodies, nil
}

func (d *EventDatasource) beginEventProjectionRead(
	ctx context.Context,
	operation string,
) (*sql.DB, *sql.Tx, error) {
	db, err := d.db.openReadOnly(ctx)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to open DB for %s: %w", operation, err)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		closeMetadataResource(db)
		return nil, nil, xerrors.Errorf("failed to begin %s transaction: %w", operation, err)
	}
	return db, tx, nil
}

func closeEventProjectionRead(db *sql.DB, tx *sql.Tx) {
	if tx != nil {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			slog.Debug("failed to rollback bounded event transaction", "error", err)
		}
	}
	if db != nil {
		closeMetadataResource(db)
	}
}

func validateBoundedBodyLimit(bodyRuneLimit int) error {
	if bodyRuneLimit <= 0 {
		return xerrors.Errorf("body rune limit must be greater than or equal to 1")
	}
	return nil
}

type boundedEventBodyRow struct {
	body              string
	visibleBodyRunes  int
	bodyAvailability  types.BodyAvailability
	canonicalEnvelope bool
}

func hydrateBoundedEvents(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	metadata []apptypes.EventMetadata,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if len(metadata) == 0 {
		return []apptypes.BoundedEvent{}, nil
	}
	eventIDs := make([]types.EventID, 0, len(metadata))
	for _, event := range metadata {
		eventIDs = append(eventIDs, event.EventID())
	}
	encoded, err := marshalBoundedEventIDs(eventIDs)
	if err != nil {
		return nil, err
	}
	rows, err := queryer.QueryContext(ctx, selectBoundedEventBodiesQuery, encoded, bodyRuneLimit)
	if err != nil {
		return nil, xerrors.Errorf("failed to query bounded event bodies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	bodyRows := make(map[types.EventID]boundedEventBodyRow, len(metadata))
	for rows.Next() {
		var (
			eventIDValue      string
			body              string
			visibleRunesValue int64
			availabilityValue string
			canonicalEnvelope bool
		)
		if err := rows.Scan(
			&eventIDValue,
			&body,
			&visibleRunesValue,
			&availabilityValue,
			&canonicalEnvelope,
		); err != nil {
			return nil, xerrors.Errorf("failed to scan bounded event body: %w", err)
		}
		eventID, err := types.EventIDFrom(eventIDValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore bounded event ID: %w", err)
		}
		visibleRunes, err := checkedInt(visibleRunesValue, "visible body runes")
		if err != nil {
			return nil, err
		}
		availability, err := types.BodyAvailabilityFrom(availabilityValue)
		if err != nil {
			return nil, xerrors.Errorf("failed to restore bounded body availability: %w", err)
		}
		bodyRows[eventID] = boundedEventBodyRow{
			body:              body,
			visibleBodyRunes:  visibleRunes,
			bodyAvailability:  availability,
			canonicalEnvelope: canonicalEnvelope,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, xerrors.Errorf("failed to iterate bounded event bodies: %w", err)
	}

	events := make([]apptypes.BoundedEvent, 0, len(metadata))
	for _, eventMetadata := range metadata {
		bodyRow, ok := bodyRows[eventMetadata.EventID()]
		if !ok {
			return nil, xerrors.Errorf("bounded body is missing for event %s", eventMetadata.EventID())
		}
		event, err := apptypes.BoundedEventOf(
			eventMetadata,
			bodyRow.body,
			bodyRuneLimit,
			bodyRow.visibleBodyRunes,
			bodyRow.bodyAvailability,
			bodyRow.canonicalEnvelope,
		)
		if err != nil {
			return nil, xerrors.Errorf("failed to build bounded event %s: %w", eventMetadata.EventID(), err)
		}
		events = append(events, event)
	}
	return events, nil
}

func marshalBoundedEventIDs(eventIDs []types.EventID) (string, error) {
	values := make([]string, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		values = append(values, eventID.String())
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", xerrors.Errorf("failed to encode bounded event IDs: %w", err)
	}
	return string(encoded), nil
}
