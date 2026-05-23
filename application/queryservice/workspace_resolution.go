package queryservice

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// WorkspaceResolutionCriteria describes the common filters used when resolving
// a requested workspace to the session row that should back read-side context
// assembly. It deliberately stays independent of any specific usecase criteria
// type so session, event, and memory context surfaces can share the same
// parent/child fallback contract.
type WorkspaceResolutionCriteria struct {
	sessionID  domtypes.SessionID
	workspace  domtypes.Workspace
	activeOnly bool
}

// WorkspaceResolutionCriteriaOf creates criteria for ResolveSessionWorkspace.
func WorkspaceResolutionCriteriaOf(sessionID domtypes.SessionID, workspace domtypes.Workspace, activeOnly bool) WorkspaceResolutionCriteria {
	return WorkspaceResolutionCriteria{
		sessionID:  sessionID,
		workspace:  workspace,
		activeOnly: activeOnly,
	}
}

// SessionID returns the optional exact session ID filter.
func (c WorkspaceResolutionCriteria) SessionID() domtypes.SessionID { return c.sessionID }

// Workspace returns the originally requested workspace.
func (c WorkspaceResolutionCriteria) Workspace() domtypes.Workspace { return c.workspace }

// ActiveOnly reports whether session lookup should only consider active
// sessions.
func (c WorkspaceResolutionCriteria) ActiveOnly() bool { return c.activeOnly }

// WorkspaceResolution carries the outcome of canonical workspace resolution
// used by session, event, and memory query paths. matchedSession is the
// session selected for the requested workspace (either exact match or via
// ancestor fallback when the requested workspace is a child path). When
// fallbackApplied is true, matchedSession.Workspace() is an ancestor of the
// requested workspace and at least one event for the session was recorded
// under the requested workspace.
type WorkspaceResolution struct {
	matchedSession  apptypes.SessionSummary
	matchedFound    bool
	fallbackApplied bool
}

// MatchedSession returns the selected session summary.
func (r WorkspaceResolution) MatchedSession() apptypes.SessionSummary { return r.matchedSession }

// MatchedFound reports whether resolution selected a session.
func (r WorkspaceResolution) MatchedFound() bool { return r.matchedFound }

// FallbackApplied reports whether resolution selected an ancestor workspace
// session after finding event evidence under the originally requested
// workspace.
func (r WorkspaceResolution) FallbackApplied() bool { return r.fallbackApplied }

// ResolveSessionWorkspace selects the session that best matches the requested
// workspace. The contract is:
//
//  1. If the requested workspace has at least one matching session row, that
//     session wins (exact-match path — preserves existing behavior including
//     the github.com/org/repo git remote case).
//  2. Otherwise, when the requested workspace looks like a filesystem path,
//     walk its ancestors (closest parent first, up to "/"). For each ancestor
//     workspace, accept the most recent matching session whose event history
//     contains at least one event recorded under the requested workspace. This
//     is the parent/child fallback used when an agent recorded the session
//     under a parent project root but recorded events under a child subdir.
//  3. If no fallback candidate qualifies, return matchedFound=false so the
//     caller can render "no matching session".
//
// The helper deliberately ignores client/agent/label filters during fallback
// because dogfooding showed they are usually unset for handoff and the goal
// is to surface evidence of work under <requested>, regardless of which agent
// recorded the parent session.
func ResolveSessionWorkspace(
	ctx context.Context,
	sessionQuery SessionQueryService,
	eventQuery EventQueryService,
	criteria WorkspaceResolutionCriteria,
) (WorkspaceResolution, error) {
	if sessionQuery == nil {
		return WorkspaceResolution{}, xerrors.Errorf("session query service is not configured")
	}

	requested := criteria.Workspace()
	sessions, err := sessionQuery.ListSummaries(
		ctx,
		1,
		0,
		criteria.SessionID(),
		requested,
		domtypes.Client(""),
		domtypes.Agent(""),
		"",
		criteria.ActiveOnly(),
		domtypes.None[time.Time](),
		domtypes.None[time.Time](),
	)
	if err != nil {
		return WorkspaceResolution{}, xerrors.Errorf("failed to list sessions for workspace resolution: %w", err)
	}
	if len(sessions) > 0 {
		return WorkspaceResolution{matchedSession: sessions[0], matchedFound: true}, nil
	}

	if eventQuery == nil || !requested.IsLocalPath() {
		return WorkspaceResolution{}, nil
	}

	for _, ancestor := range requested.AncestorWorkspaces() {
		const ancestorCandidatePageSize = 50
		for offset := 0; ; offset += ancestorCandidatePageSize {
			candidates, err := sessionQuery.ListSummaries(
				ctx,
				ancestorCandidatePageSize,
				offset,
				criteria.SessionID(),
				ancestor,
				domtypes.Client(""),
				domtypes.Agent(""),
				"",
				criteria.ActiveOnly(),
				domtypes.None[time.Time](),
				domtypes.None[time.Time](),
			)
			if err != nil {
				return WorkspaceResolution{}, xerrors.Errorf("failed to list ancestor sessions for workspace resolution: %w", err)
			}
			if len(candidates) == 0 {
				break
			}
			for _, candidate := range candidates {
				hasEvidence, err := sessionHasEventsUnderWorkspace(ctx, eventQuery, candidate.SessionID(), requested)
				if err != nil {
					return WorkspaceResolution{}, err
				}
				if hasEvidence {
					return WorkspaceResolution{
						matchedSession:  candidate,
						matchedFound:    true,
						fallbackApplied: true,
					}, nil
				}
			}
			if len(candidates) < ancestorCandidatePageSize {
				break
			}
		}
	}

	return WorkspaceResolution{}, nil
}

// sessionHasEventsUnderWorkspace reports whether any event for sessionID was
// recorded under the supplied workspace. We pull one row because existence is
// the only signal needed for fallback acceptance.
func sessionHasEventsUnderWorkspace(
	ctx context.Context,
	eventQuery EventQueryService,
	sessionID domtypes.SessionID,
	workspace domtypes.Workspace,
) (bool, error) {
	events, err := eventQuery.ListRecent(
		ctx,
		1,
		0,
		domtypes.EventKind(""),
		domtypes.Client(""),
		domtypes.Agent(""),
		sessionID,
		workspace,
		false,
		time.Time{},
		time.Time{},
		"",
	)
	if err != nil {
		return false, xerrors.Errorf("failed to verify event evidence under requested workspace: %w", err)
	}
	return len(events) > 0, nil
}
