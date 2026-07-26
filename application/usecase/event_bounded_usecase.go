package usecase

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/types"
)

// EventBoundedUsecase exposes event reads whose visible body is capped before
// it crosses the persistence boundary.
type EventBoundedUsecase interface {
	// List preserves list_events body_blocks compatibility through an explicit
	// fallback only for canonical, available, response-untruncated rows.
	List(ctx context.Context, criteria apptypes.EventListCriteria, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	// Search returns bounded visible text without canonical body-block fallback.
	Search(ctx context.Context, criteria apptypes.EventSearchCriteria, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	// Context returns bounded visible text without canonical body-block fallback.
	Context(ctx context.Context, criteria apptypes.EventContextCriteria, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	// HydrateList projects the exact metadata candidates selected by list_events
	// and preserves its canonical body-block compatibility behavior.
	HydrateList(ctx context.Context, metadata []apptypes.EventMetadata, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	// HydrateSearch projects the exact metadata candidates selected by search.
	HydrateSearch(ctx context.Context, metadata []apptypes.EventMetadata, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	// HydrateContext projects the exact metadata candidates selected by
	// get_context.
	HydrateContext(ctx context.Context, metadata []apptypes.EventMetadata, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
}

type eventBoundedUsecase struct {
	query queryservice.EventBoundedQueryService
}

// NewEventBoundedUsecase creates the bounded event read usecase.
func NewEventBoundedUsecase(query queryservice.EventBoundedQueryService) EventBoundedUsecase {
	return &eventBoundedUsecase{query: query}
}

func (u *eventBoundedUsecase) List(
	ctx context.Context,
	criteria apptypes.EventListCriteria,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedRead(u.query, bodyRuneLimit); err != nil {
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

	events, err := u.query.ListRecentBounded(ctx, criteria, bodyRuneLimit)
	if err != nil {
		return nil, xerrors.Errorf("failed to list bounded events: %w", err)
	}
	return u.attachCanonicalBodyBlocks(ctx, events)
}

func (u *eventBoundedUsecase) HydrateList(
	ctx context.Context,
	metadata []apptypes.EventMetadata,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	events, err := u.hydrateBounded(ctx, metadata, bodyRuneLimit)
	if err != nil {
		return nil, err
	}
	return u.attachCanonicalBodyBlocks(ctx, events)
}

func (u *eventBoundedUsecase) HydrateSearch(
	ctx context.Context,
	metadata []apptypes.EventMetadata,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	return u.hydrateBounded(ctx, metadata, bodyRuneLimit)
}

func (u *eventBoundedUsecase) HydrateContext(
	ctx context.Context,
	metadata []apptypes.EventMetadata,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	return u.hydrateBounded(ctx, metadata, bodyRuneLimit)
}

func (u *eventBoundedUsecase) hydrateBounded(
	ctx context.Context,
	metadata []apptypes.EventMetadata,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedRead(u.query, bodyRuneLimit); err != nil {
		return nil, err
	}
	if len(metadata) == 0 {
		return []apptypes.BoundedEvent{}, nil
	}
	events, err := u.query.HydrateBounded(ctx, metadata, bodyRuneLimit)
	if err != nil {
		return nil, xerrors.Errorf("failed to hydrate bounded events: %w", err)
	}
	if len(events) != len(metadata) {
		return nil, xerrors.Errorf("bounded hydration changed the selected event count")
	}
	for index := range metadata {
		if events[index].Metadata().EventID() != metadata[index].EventID() {
			return nil, xerrors.Errorf("bounded hydration changed the selected event order")
		}
	}
	return events, nil
}

func (u *eventBoundedUsecase) attachCanonicalBodyBlocks(
	ctx context.Context,
	events []apptypes.BoundedEvent,
) ([]apptypes.BoundedEvent, error) {
	canonicalIDs := make([]types.EventID, 0)
	for _, event := range events {
		if event.BodyAvailability().IsAvailable() &&
			event.CanonicalEnvelope() &&
			!event.BodyResponseTruncated() {
			canonicalIDs = append(canonicalIDs, event.Metadata().EventID())
		}
	}
	if len(canonicalIDs) == 0 {
		return events, nil
	}

	bodies, err := u.query.LoadCanonicalBodies(ctx, canonicalIDs)
	if err != nil {
		return nil, xerrors.Errorf("failed to load canonical event bodies: %w", err)
	}
	for index, event := range events {
		body, ok := bodies[event.Metadata().EventID()]
		if !ok {
			continue
		}
		blocks, canonical := apptypes.DecodeCanonicalEnvelope(body)
		if !canonical || apptypes.ExtractPlainBody(body) != event.Body() {
			// A concurrent retention/body update can invalidate the earlier
			// projection. Omit blocks instead of attaching stale/raw content
			// whose visible-text form no longer matches this response.
			continue
		}
		withBlocks, err := event.WithCanonicalBodyBlocks(blocks)
		if err != nil {
			return nil, xerrors.Errorf("failed to attach canonical body blocks for %s: %w", event.Metadata().EventID(), err)
		}
		events[index] = withBlocks
	}
	return events, nil
}

func (u *eventBoundedUsecase) Search(
	ctx context.Context,
	criteria apptypes.EventSearchCriteria,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedRead(u.query, bodyRuneLimit); err != nil {
		return nil, err
	}
	if !hasSearchConstraint(criteria) {
		return nil, xerrors.Errorf("at least one search filter is required")
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
	resolvedKind, err := resolveOptionalSearchKind(criteria.Kind().String())
	if err != nil {
		return nil, err
	}
	resolvedCriteria := apptypes.NewEventSearchCriteriaBuilder(criteria.Limit()).
		Query(criteria.Query()).
		Workspace(criteria.Workspace()).
		SessionID(criteria.SessionID()).
		Client(criteria.Client()).
		Agent(criteria.Agent()).
		Kind(resolvedKind).
		From(criteria.From()).
		To(criteria.To()).
		Offset(criteria.Offset()).
		FailuresOnly(criteria.FailuresOnly()).
		PageAnchor(criteria.PageAnchor()).
		Build()

	events, err := u.query.SearchBounded(ctx, resolvedCriteria, bodyRuneLimit)
	if err != nil {
		return nil, xerrors.Errorf("failed to search bounded events: %w", err)
	}
	return events, nil
}

func (u *eventBoundedUsecase) Context(
	ctx context.Context,
	criteria apptypes.EventContextCriteria,
	bodyRuneLimit int,
) ([]apptypes.BoundedEvent, error) {
	if err := validateBoundedRead(u.query, bodyRuneLimit); err != nil {
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
	events, err := u.query.GetContextBounded(ctx, criteria, bodyRuneLimit)
	if err != nil {
		return nil, xerrors.Errorf("failed to get bounded context events: %w", err)
	}
	return events, nil
}

func validateBoundedRead(query queryservice.EventBoundedQueryService, bodyRuneLimit int) error {
	if query == nil {
		return xerrors.Errorf("event bounded query service is not configured")
	}
	if bodyRuneLimit <= 0 {
		return xerrors.Errorf("body rune limit must be greater than or equal to 1")
	}
	return nil
}
