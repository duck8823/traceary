package usecase

import (
	"context"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const (
	defaultReplaySessionLimit     = 10
	defaultReplayEventsPerSession = 20
	// defaultReplayTimelineGapSeconds mirrors the CLI `traceary
	// timeline` default so the replay export and the CLI render
	// agree on block boundaries.
	defaultReplayTimelineGapSeconds = 15 * 60
	// defaultReplayHotspotLookback caps the failure-hotspot query to
	// recent history — a week is long enough to expose a flaky test
	// without dragging in ancient noise.
	defaultReplayHotspotLookback = 7 * 24 * time.Hour
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
		false,
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
			"",
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

	timelineBlocks, err := u.loadTimelineBlocks(ctx, criteria)
	if err != nil {
		return apptypes.ReplayBundle{}, err
	}

	failureHotspots, err := u.loadFailureHotspots(ctx, criteria)
	if err != nil {
		return apptypes.ReplayBundle{}, err
	}

	return apptypes.ReplayBundleOf(u.now(), bundleSessions, memories, timelineBlocks, failureHotspots), nil
}

// loadTimelineBlocks populates the timeline panel by delegating to the
// same queryservice path the `traceary timeline` CLI command uses.
// Non-positive TimelineLimit skips the panel entirely so callers that
// only want the session / memory view do not pay for the CTE.
func (u *replayUsecase) loadTimelineBlocks(ctx context.Context, criteria apptypes.ReplayCriteria) ([]apptypes.TimelineBlock, error) {
	if criteria.TimelineLimit() <= 0 {
		return nil, nil
	}
	gap := criteria.TimelineGapSeconds()
	if gap <= 0 {
		gap = defaultReplayTimelineGapSeconds
	}
	blocks, err := u.eventQuery.ListTimelineBlocks(
		ctx,
		domtypes.Workspace(""),
		time.Time{},
		time.Time{},
		gap,
		criteria.TimelineLimit(),
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to list timeline blocks for replay: %w", err)
	}
	return blocks, nil
}

// loadFailureHotspots clusters non-zero-exit-code command_executed
// events by normalized command prefix (first whitespace-delimited
// token) within a workspace and returns the top N clusters by count.
// Empty command bodies fall through as "(unknown)" so the ranking
// does not silently drop events with malformed payloads.
func (u *replayUsecase) loadFailureHotspots(ctx context.Context, criteria apptypes.ReplayCriteria) ([]apptypes.ReplayFailureHotspot, error) {
	limit := criteria.HotspotLimit()
	if limit <= 0 {
		return nil, nil
	}
	lookback := criteria.HotspotLookback()
	if lookback <= 0 {
		lookback = defaultReplayHotspotLookback
	}
	now := u.now()
	from := now.Add(-lookback)

	// Widen the underlying scan so the top-N cluster count is
	// statistically meaningful; the limit applied in ListRecent is the
	// number of raw failure events we look at, not the number of
	// clusters we emit.
	const hotspotScanMultiplier = 20
	scanLimit := limit * hotspotScanMultiplier
	events, err := u.eventQuery.ListRecent(
		ctx,
		scanLimit,
		0,
		domtypes.EventKindCommandExecuted,
		domtypes.Client(""),
		domtypes.Agent(""),
		domtypes.SessionID(""),
		domtypes.Workspace(""),
		true,
		from,
		now,
		"",
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to list failure events for replay hotspot: %w", err)
	}

	type clusterKey struct {
		command   string
		workspace string
	}
	type clusterAggregate struct {
		count          int
		lastOccurredAt time.Time
	}
	clusters := make(map[clusterKey]clusterAggregate, len(events))
	for _, event := range events {
		commandPrefix := normalizeFailureCommandPrefix(event.Body())
		key := clusterKey{
			command:   commandPrefix,
			workspace: event.Workspace().String(),
		}
		agg := clusters[key]
		agg.count++
		if event.CreatedAt().After(agg.lastOccurredAt) {
			agg.lastOccurredAt = event.CreatedAt()
		}
		clusters[key] = agg
	}

	hotspots := make([]apptypes.ReplayFailureHotspot, 0, len(clusters))
	for key, agg := range clusters {
		hotspots = append(hotspots, apptypes.ReplayFailureHotspotOf(key.command, key.workspace, agg.count, agg.lastOccurredAt.UTC()))
	}
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].Count() != hotspots[j].Count() {
			return hotspots[i].Count() > hotspots[j].Count()
		}
		if !hotspots[i].LastOccurredAt().Equal(hotspots[j].LastOccurredAt()) {
			return hotspots[i].LastOccurredAt().After(hotspots[j].LastOccurredAt())
		}
		if hotspots[i].Command() != hotspots[j].Command() {
			return hotspots[i].Command() < hotspots[j].Command()
		}
		return hotspots[i].Workspace() < hotspots[j].Workspace()
	})
	if len(hotspots) > limit {
		hotspots = hotspots[:limit]
	}
	return hotspots, nil
}

// normalizeFailureCommandPrefix extracts the first whitespace-delimited
// token from a command_executed body so clusters group by tool name
// (for example `go test ./...` and `go vet ./...` both cluster under
// `go`). Empty bodies fall back to "(unknown)".
func normalizeFailureCommandPrefix(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "(unknown)"
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "(unknown)"
	}
	return fields[0]
}
