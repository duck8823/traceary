package usecase

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	defaultReplaySessionLimit     = 10
	defaultReplayEventsPerSession = 20
	defaultReplayMemoryLimit      = 20
)

type replayUsecase struct {
	session SessionUsecase
	event   EventUsecase
	memory  MemoryUsecase
	now     func() time.Time
}

// NewReplayUsecase creates a ReplayUsecase backed by the existing
// session / event / memory write-side usecases. memory may be nil —
// the bundle will simply omit the memory panel in that case.
func NewReplayUsecase(session SessionUsecase, event EventUsecase, memory MemoryUsecase) ReplayUsecase {
	return &replayUsecase{
		session: session,
		event:   event,
		memory:  memory,
		now:     func() time.Time { return time.Now().UTC() },
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
	if u.session == nil || u.event == nil {
		return apptypes.ReplayBundle{}, xerrors.Errorf("replay usecase requires session and event usecases")
	}

	sessionLimit := criteria.SessionLimit()
	if sessionLimit <= 0 {
		sessionLimit = defaultReplaySessionLimit
	}
	eventsPerSession := criteria.EventsPerSession()
	if eventsPerSession <= 0 {
		eventsPerSession = defaultReplayEventsPerSession
	}
	memoryLimit := criteria.MemoryLimit()
	if memoryLimit < 0 {
		memoryLimit = defaultReplayMemoryLimit
	}

	sessionCriteria := apptypes.NewSessionListCriteriaBuilder(sessionLimit).Build()
	sessions, err := u.session.List(ctx, sessionCriteria)
	if err != nil {
		return apptypes.ReplayBundle{}, xerrors.Errorf("failed to list sessions for replay: %w", err)
	}

	bundleSessions := make([]apptypes.ReplayBundleSession, 0, len(sessions))
	workspaceSet := make(map[domtypes.Workspace]struct{})
	for _, session := range sessions {
		eventCriteria := apptypes.NewEventListCriteriaBuilder(eventsPerSession).
			SessionID(session.SessionID()).
			Build()
		events, err := u.event.List(ctx, eventCriteria)
		if err != nil {
			return apptypes.ReplayBundle{}, xerrors.Errorf("failed to list events for session %s: %w", session.SessionID().String(), err)
		}
		bundleSessions = append(bundleSessions, apptypes.ReplayBundleSessionOf(session, events))
		if workspace := session.Workspace(); workspace.String() != "" {
			workspaceSet[workspace] = struct{}{}
		}
	}

	var memories []apptypes.MemorySummary
	if u.memory != nil && memoryLimit > 0 && len(workspaceSet) > 0 {
		scopes := make([]domtypes.MemoryScope, 0, len(workspaceSet))
		for workspace := range workspaceSet {
			scopes = append(scopes, domtypes.WorkspaceScopeOf(workspace))
		}
		memCriteria := apptypes.NewMemoryListCriteriaBuilder(memoryLimit).
			Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted}).
			Scopes(scopes)
		if asOf, ok := criteria.MemoryAsOf().Value(); ok {
			memCriteria = memCriteria.AsOf(asOf)
		}
		memories, err = u.memory.List(ctx, memCriteria.Build())
		if err != nil {
			return apptypes.ReplayBundle{}, xerrors.Errorf("failed to list memories for replay: %w", err)
		}
	}

	return apptypes.ReplayBundleOf(u.now(), bundleSessions, memories), nil
}
