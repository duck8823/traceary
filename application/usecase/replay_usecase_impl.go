package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	defaultReplaySessionLimit     = 10
	defaultReplayEventsPerSession = 20
)

type replayUsecase struct {
	sessionQuery queryservice.SessionQueryService
	eventQuery   queryservice.EventQueryService
	memoryQuery  queryservice.MemoryQueryService
	now          func() time.Time
}

// NewReplayUsecase creates a ReplayUsecase backed by the read-side
// query services. memoryQuery may be nil — the bundle will simply
// omit the memory panel in that case.
//
// Using query services directly (instead of write-side usecases)
// keeps the replay path on the read-only surface, consistent with
// ContextUsecase and the other cross-aggregate assemblers in this
// package.
func NewReplayUsecase(
	sessionQuery queryservice.SessionQueryService,
	eventQuery queryservice.EventQueryService,
	memoryQuery queryservice.MemoryQueryService,
) ReplayUsecase {
	return &replayUsecase{
		sessionQuery: sessionQuery,
		eventQuery:   eventQuery,
		memoryQuery:  memoryQuery,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

// Bundle implements ReplayUsecase.
//
// Memory retrieval is scoped to the set of workspaces that appear in
// the loaded sessions, so the replay HTML cannot mix in a memory from
// an unrelated repository. When no sessions carry a workspace (for
// example an empty store or a local-only quick test), the memory
// panel is skipped rather than falling back to a global query — the
// CLI previously joined on "all accepted memories" and that broke the
// bundle invariant the replay UX advertises.
func (u *replayUsecase) Bundle(ctx context.Context, criteria apptypes.ReplayCriteria) (apptypes.ReplayBundle, error) {
	if u.sessionQuery == nil || u.eventQuery == nil {
		return apptypes.ReplayBundle{}, xerrors.Errorf("replay usecase requires session and event query services")
	}

	sessionLimit := criteria.SessionLimit()
	if sessionLimit <= 0 {
		sessionLimit = defaultReplaySessionLimit
	}
	eventsPerSession := criteria.EventsPerSession()
	if eventsPerSession <= 0 {
		eventsPerSession = defaultReplayEventsPerSession
	}

	sessions, err := u.sessionQuery.ListSummaries(
		ctx,
		sessionLimit,
		0,
		domtypes.SessionID(""),
		domtypes.Workspace(""),
		domtypes.Client(""),
		domtypes.Agent(""),
		"",
		domtypes.None[time.Time](),
		domtypes.None[time.Time](),
	)
	if err != nil {
		return apptypes.ReplayBundle{}, xerrors.Errorf("failed to list sessions for replay: %w", err)
	}

	bundleSessions := make([]apptypes.ReplayBundleSession, 0, len(sessions))
	workspaceSet := make(map[domtypes.Workspace]struct{})
	for _, session := range sessions {
		events, err := u.eventQuery.ListRecent(
			ctx,
			eventsPerSession,
			0,
			domtypes.EventKind(""),
			domtypes.Client(""),
			domtypes.Agent(""),
			session.SessionID(),
			domtypes.Workspace(""),
			false,
			time.Time{},
			time.Time{},
		)
		if err != nil {
			return apptypes.ReplayBundle{}, xerrors.Errorf("failed to list events for session %s: %w", session.SessionID().String(), err)
		}
		bundleSessions = append(bundleSessions, apptypes.ReplayBundleSessionOf(session, events))
		if workspace := session.Workspace(); workspace.String() != "" {
			workspaceSet[workspace] = struct{}{}
		}
	}

	// Non-positive memoryLimit is a skip signal (matches the
	// ReplayCriteria.MemoryLimit contract).
	var memories []apptypes.MemorySummary
	if u.memoryQuery != nil && criteria.MemoryLimit() > 0 && len(workspaceSet) > 0 {
		scopes := make([]domtypes.MemoryScope, 0, len(workspaceSet))
		for workspace := range workspaceSet {
			scopes = append(scopes, domtypes.WorkspaceScopeOf(workspace))
		}
		memCriteria := apptypes.NewMemoryListCriteriaBuilder(criteria.MemoryLimit()).
			Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted}).
			Scopes(scopes)
		if asOf, ok := criteria.MemoryAsOf().Value(); ok {
			memCriteria = memCriteria.AsOf(asOf)
		}
		memories, err = u.memoryQuery.List(ctx, memCriteria.Build())
		if err != nil {
			return apptypes.ReplayBundle{}, xerrors.Errorf("failed to list memories for replay: %w", err)
		}
	}

	return apptypes.ReplayBundleOf(u.now(), bundleSessions, memories), nil
}
