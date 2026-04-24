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
