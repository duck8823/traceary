package queryservice

import (
	"context"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// EventQueryService provides read-side operations for events.
type EventQueryService interface {
	// ListRecent returns events in descending time order.
	ListRecent(ctx context.Context, limit, offset int, kind types.EventKind, client types.Client, agent types.Agent, sessionID types.SessionID, workspace types.Workspace, failuresOnly bool, from, to time.Time, sourceHook string) ([]*model.Event, error)
	// ListWindow returns every event matching the criteria whose created_at
	// falls in [From, To) under a single read snapshot so concurrent writers
	// cannot cause the scan to drop events. Callers supply the batch size via
	// criteria.Limit(); offset is ignored.
	ListWindow(ctx context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error)
	// Search performs full-text search across events.
	Search(ctx context.Context, query string, workspace types.Workspace, sessionID types.SessionID, client types.Client, agent types.Agent, kind types.EventKind, from, to time.Time, limit, offset int, failuresOnly bool) ([]*model.Event, error)
	// GetContext returns recent events for context retrieval.
	GetContext(ctx context.Context, workspace types.Workspace, sessionID types.SessionID, limit int) ([]*model.Event, error)
	// GetDetails returns the details for a single event.
	GetDetails(ctx context.Context, eventID types.EventID) (apptypes.EventDetails, error)
	// ListTimelineBlocks returns work blocks separated by idle gaps.
	ListTimelineBlocks(ctx context.Context, workspace types.Workspace, from, to time.Time, gapSeconds, limit int) ([]apptypes.TimelineBlock, error)
}

// EventPageQueryService provides cursor-aware full event projections. It is a
// separate capability so existing non-page query implementations do not need
// to understand continuation anchors.
type EventPageQueryService interface {
	ListRecentPage(ctx context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error)
	SearchPage(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error)
	GetContextPage(ctx context.Context, criteria apptypes.EventContextCriteria) ([]*model.Event, error)
}

// EventReadQueryService is the complete read contract required by EventUsecase.
// Page-aware reads are mandatory instead of being discovered with a runtime
// capability assertion that could silently fall back to offset paging.
type EventReadQueryService interface {
	EventQueryService
	EventPageQueryService
}

// EventMetadataQueryService provides body-free event inspection operations.
// It is separate from EventQueryService so consumers cannot accidentally
// request a partial domain Event through an include-body flag.
type EventMetadataQueryService interface {
	ListRecentTimestampKinds(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventTimestampKind, error)
	// ListRecentMetadata returns body-free events in descending time order.
	ListRecentMetadata(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventMetadata, error)
	// ListWindowMetadata returns every matching body-free event under one
	// read snapshot. criteria.Limit() controls the internal page size.
	ListWindowMetadata(ctx context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventMetadata, error)
	// SearchMetadata searches event and command content in SQLite but returns
	// only body-free event metadata to the caller.
	SearchMetadata(ctx context.Context, criteria apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error)
	// GetContextMetadata returns body-free context membership in descending
	// time order.
	GetContextMetadata(ctx context.Context, criteria apptypes.EventContextCriteria) ([]apptypes.EventMetadata, error)
}

// EventPreviewQueryService provides bounded content reads for summary
// surfaces without materializing full stored event bodies.
type EventPreviewQueryService interface {
	ListRecentCommandPreviews(ctx context.Context, sessionID types.SessionID, limit, bodyRuneLimit int) ([]apptypes.EventBodyPreview, error)
}

// EventBoundedQueryService provides body-limited event reads without
// materializing full domain Events. Search documents may select candidate IDs,
// but bounded bodies always come from authoritative event rows.
type EventBoundedQueryService interface {
	ListRecentBounded(ctx context.Context, criteria apptypes.EventListCriteria, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	SearchBounded(ctx context.Context, criteria apptypes.EventSearchCriteria, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	GetContextBounded(ctx context.Context, criteria apptypes.EventContextCriteria, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	// HydrateBounded projects bodies for an already-selected metadata page.
	// It must not re-run membership filters; the supplied order is preserved.
	HydrateBounded(ctx context.Context, metadata []apptypes.EventMetadata, bodyRuneLimit int) ([]apptypes.BoundedEvent, error)
	// LoadCanonicalBodies is the explicit list-only compatibility fallback for
	// canonical envelopes whose visible body was not response-truncated.
	LoadCanonicalBodies(ctx context.Context, eventIDs []types.EventID) (map[types.EventID]string, error)
}

// ProjectionSessionSearchQuery returns session-tier hits from the bounded
// search projection. Callers that only need event rows do not depend on this
// port. Hits are never events: a session row means "the trail is here, open it".
// Sessions already represented by event hits in the same response should be
// passed in excludeSessionIDs so they are omitted.
type ProjectionSessionSearchQuery interface {
	SearchSessionHits(ctx context.Context, criteria apptypes.EventSearchCriteria, excludeSessionIDs []types.SessionID) ([]apptypes.SearchSessionHit, error)
}
