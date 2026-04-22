package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type contextPackBuilder struct {
	sessionQuery queryservice.SessionQueryService
	eventQuery   queryservice.EventQueryService
	memoryQuery  queryservice.MemoryQueryService
}

func newContextPackBuilder(
	sessionQuery queryservice.SessionQueryService,
	eventQuery queryservice.EventQueryService,
	memoryQuery queryservice.MemoryQueryService,
) (*contextPackBuilder, error) {
	if sessionQuery == nil {
		return nil, xerrors.Errorf("session query service is not configured")
	}
	if eventQuery == nil {
		return nil, xerrors.Errorf("event query service is not configured")
	}

	return &contextPackBuilder{
		sessionQuery: sessionQuery,
		eventQuery:   eventQuery,
		memoryQuery:  memoryQuery,
	}, nil
}

func (b *contextPackBuilder) Build(ctx context.Context, criteria apptypes.ContextPackCriteria) (domtypes.Optional[apptypes.ContextPack], error) {
	if criteria.RecentCommandsLimit() < 0 {
		return domtypes.None[apptypes.ContextPack](), xerrors.Errorf("recent commands limit must be greater than or equal to 0")
	}
	if criteria.MemoryLimit() < 0 {
		return domtypes.None[apptypes.ContextPack](), xerrors.Errorf("memory limit must be greater than or equal to 0")
	}

	sessions, err := b.sessionQuery.ListSummaries(
		ctx,
		1,
		0,
		criteria.SessionID(),
		criteria.Workspace(),
		domtypes.Client(""),
		domtypes.Agent(""),
		"",
		domtypes.None[time.Time](),
		domtypes.None[time.Time](),
	)
	if err != nil {
		return domtypes.None[apptypes.ContextPack](), xerrors.Errorf("failed to list sessions for context pack: %w", err)
	}
	if len(sessions) == 0 {
		return domtypes.None[apptypes.ContextPack](), nil
	}

	session := sessions[0]
	recentCommands, err := b.loadRecentCommands(ctx, session, criteria.RecentCommandsLimit())
	if err != nil {
		return domtypes.None[apptypes.ContextPack](), err
	}
	compactSummary, err := b.loadCompactSummary(ctx, session)
	if err != nil {
		compactSummary = ""
	}
	memories, err := b.loadMemories(ctx, session, criteria.MemoryLimit(), criteria.MemoryPreset(), criteria.MemoryAsOf())
	if err != nil {
		return domtypes.None[apptypes.ContextPack](), err
	}

	pack := apptypes.ContextPackOf(
		session.SessionID(),
		session.Workspace(),
		session.Label(),
		session.Status(),
		session.TotalEvents(),
		session.CommandCount(),
		session.Agents(),
		apptypes.WorkingStateOf(session.Summary(), compactSummary),
		recentCommands,
		memories,
	)
	return domtypes.Some(pack), nil
}

func (b *contextPackBuilder) loadRecentCommands(ctx context.Context, session apptypes.SessionSummary, limit int) ([]string, error) {
	if limit == 0 {
		return nil, nil
	}

	events, err := b.eventQuery.ListRecent(
		ctx,
		limit,
		0,
		domtypes.EventKindCommandExecuted,
		domtypes.Client(""),
		domtypes.Agent(""),
		session.SessionID(),
		domtypes.Workspace(""),
		false,
		time.Time{},
		time.Time{},
	)
	if err != nil {
		return nil, xerrors.Errorf("failed to list recent command events for context pack: %w", err)
	}

	recentCommands := make([]string, 0, len(events))
	for _, event := range events {
		recentCommands = append(recentCommands, summarizeCommand(event.Body()))
	}
	return recentCommands, nil
}

func (b *contextPackBuilder) loadCompactSummary(ctx context.Context, session apptypes.SessionSummary) (string, error) {
	// Pull a small window of compact_summary events and skip
	// pre-compact snapshots so the handoff path always returns the
	// most recent POST-compact digest even when a cancelled compact
	// cycle left a pre-compact entry as the newest row.
	const compactSummaryScanLimit = 10
	events, err := b.eventQuery.ListRecent(
		ctx,
		compactSummaryScanLimit,
		0,
		domtypes.EventKindCompactSummary,
		domtypes.Client(""),
		domtypes.Agent(""),
		session.SessionID(),
		session.Workspace(),
		false,
		time.Time{},
		time.Time{},
	)
	if err != nil {
		return "", xerrors.Errorf("failed to list compact summary events for context pack: %w", err)
	}
	for _, event := range events {
		body := event.Body()
		if strings.HasPrefix(strings.TrimSpace(body), compactPreSnapshotMarker) {
			continue
		}
		return extractCompactSummarySignal(body), nil
	}
	return "", nil
}

// compactPreSnapshotMarker mirrors the CLI-side marker used by the
// PreCompact hook. Keep the two copies in sync — the marker itself
// is body content and a later rename means rewriting history.
const compactPreSnapshotMarker = "[phase:pre-compact]"

func (b *contextPackBuilder) loadMemories(
	ctx context.Context,
	session apptypes.SessionSummary,
	limit int,
	preset apptypes.MemoryRetrievalPreset,
	asOf domtypes.Optional[time.Time],
) ([]apptypes.MemorySummary, error) {
	if b.memoryQuery == nil || limit == 0 {
		return nil, nil
	}

	scopes := relevantMemoryScopes(session)
	if len(scopes) == 0 {
		return nil, nil
	}

	builder := apptypes.NewMemoryListCriteriaBuilder(limit).Scopes(scopes)
	if preset != "" {
		// Preset wins for context packs: callers asked for a specific
		// retrieval shape (resume / review / incident), so we honor
		// its Statuses + MemoryTypes defaults. No preset falls back to
		// the legacy accepted-only behavior so existing clients see
		// the same pack shape as before.
		builder = preset.ApplyToMemoryListCriteriaBuilder(builder)
	} else {
		builder = builder.Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted})
	}
	if asOfValue, ok := asOf.Value(); ok {
		builder = builder.AsOf(asOfValue)
	}
	memories, err := b.memoryQuery.List(ctx, builder.Build())
	if err != nil {
		return nil, xerrors.Errorf("failed to list durable memories for context pack: %w", err)
	}
	return memories, nil
}

func relevantMemoryScopes(session apptypes.SessionSummary) []domtypes.MemoryScope {
	type scopeKey struct {
		kind string
		key  string
	}

	seen := make(map[scopeKey]struct{})
	scopes := make([]domtypes.MemoryScope, 0, len(session.Agents())+2)
	appendScope := func(scope domtypes.MemoryScope) {
		if scope == nil {
			return
		}
		key := scopeKey{kind: scope.Kind().String(), key: scope.Key()}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		scopes = append(scopes, scope)
	}

	if session.Workspace().String() != "" {
		appendScope(domtypes.WorkspaceScopeOf(session.Workspace()))
	}
	if session.SessionID().String() != "" {
		appendScope(domtypes.SessionFamilyScopeOf(session.SessionID()))
	}
	for _, agentValue := range session.Agents() {
		agent, err := domtypes.AgentOf(agentValue)
		if err != nil {
			continue
		}
		appendScope(domtypes.AgentScopeOf(agent))
	}

	return scopes
}

func summarizeCommand(command string) string {
	trimmed := strings.Join(strings.Fields(command), " ")
	if trimmed == "" {
		return "-"
	}
	if runes := []rune(trimmed); len(runes) > 60 {
		return string(runes[:60]) + "\u2026"
	}
	return trimmed
}

func extractCompactSummarySignal(body string) string {
	sections := []string{"Current Work", "Pending Tasks", "Optional Next Step"}
	parts := make([]string, 0, len(sections))

	for _, section := range sections {
		content := extractCompactSummarySection(body, section)
		if content != "" {
			parts = append(parts, content)
		}
	}

	result := strings.Join(parts, " | ")
	if runes := []rune(result); len(runes) > 500 {
		return string(runes[:500]) + "\u2026"
	}
	return result
}

func extractCompactSummarySection(body string, section string) string {
	candidates := []string{
		fmt.Sprintf("%s:", section),
	}
	for i := 1; i <= 9; i++ {
		candidates = append(candidates, fmt.Sprintf("%d. %s:", i, section))
	}

	start := -1
	for _, candidate := range candidates {
		if idx := strings.Index(body, candidate); idx >= 0 {
			start = idx + len(candidate)
			break
		}
	}
	if start < 0 {
		return ""
	}

	rest := body[start:]
	end := len(rest)
	for i := 1; i <= 9; i++ {
		if idx := strings.Index(rest, fmt.Sprintf("\n%d. ", i)); idx >= 0 && idx < end {
			end = idx
		}
	}
	for _, otherSection := range []string{"Current Work", "Pending Tasks", "Optional Next Step"} {
		if otherSection == section {
			continue
		}
		if idx := strings.Index(rest, "\n"+otherSection+":"); idx >= 0 && idx < end {
			end = idx
		}
	}
	if idx := strings.Index(rest, "\n</"); idx >= 0 && idx < end {
		end = idx
	}

	content := strings.TrimSpace(rest[:end])
	if content == "" {
		return ""
	}
	return strings.Join(strings.Fields(content), " ")
}
