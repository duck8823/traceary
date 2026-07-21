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
	previewQuery queryservice.EventPreviewQueryService
	memoryQuery  queryservice.MemoryQueryService
}

const contextPackCommandPreviewRuneLimit = 4096

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
	previewQuery, ok := eventQuery.(queryservice.EventPreviewQueryService)
	if !ok {
		return nil, xerrors.Errorf("event preview query service is not configured")
	}
	return &contextPackBuilder{
		sessionQuery: sessionQuery,
		eventQuery:   eventQuery,
		previewQuery: previewQuery,
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

	resolution, err := queryservice.ResolveSessionWorkspace(
		ctx,
		b.sessionQuery,
		b.eventQuery,
		queryservice.WorkspaceResolutionCriteriaOf(criteria.SessionID(), criteria.Workspace(), false),
	)
	if err != nil {
		return domtypes.None[apptypes.ContextPack](), xerrors.Errorf("failed to resolve session workspace for context pack: %w", err)
	}
	if !resolution.MatchedFound() {
		return domtypes.None[apptypes.ContextPack](), nil
	}

	session := resolution.MatchedSession()
	if !criteria.AllowStale() && criteria.StaleAfter() > 0 && isStaleActiveSession(session, criteria.StaleAfter(), time.Now()) {
		return domtypes.None[apptypes.ContextPack](), nil
	}
	recentCommands, recentCommandItems, err := b.loadRecentCommands(ctx, session, criteria.RecentCommandsLimit())
	if err != nil {
		return domtypes.None[apptypes.ContextPack](), err
	}
	compactSummary, err := b.loadCompactSummary(ctx, session)
	if err != nil {
		compactSummary = ""
	}
	memories, err := b.loadMemories(
		ctx,
		session,
		criteria.Workspace(),
		criteria.MemoryLimit(),
		criteria.MemoryPreset(),
		criteria.IncludeMemoryCandidates(),
		criteria.MemoryAsOf(),
	)
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
		memories.trusted,
	).
		WithRecentCommandItems(recentCommandItems).
		WithRequestedWorkspace(criteria.Workspace()).
		WithMemoryNeedsReview(memories.needsReview, memories.candidateCount).
		WithMemoryCounts(memories.acceptedCount, memories.candidateCount)
	return domtypes.Some(pack), nil
}

func (b *contextPackBuilder) loadRecentCommands(ctx context.Context, session apptypes.SessionSummary, limit int) ([]string, []apptypes.RecentCommandSummary, error) {
	if limit == 0 {
		return nil, nil, nil
	}
	previews, err := b.previewQuery.ListRecentCommandPreviews(ctx, session.SessionID(), limit, contextPackCommandPreviewRuneLimit)
	if err != nil {
		return nil, nil, xerrors.Errorf("failed to list recent command previews for context pack: %w", err)
	}
	commands := make([]string, 0, len(previews))
	items := make([]apptypes.RecentCommandSummary, 0, len(previews))
	for _, preview := range previews {
		summary := summarizeCommand(preview.Body())
		// A handoff summary is intentionally lossy: paragraph selection and
		// whitespace normalization also mean the response does not contain the
		// persisted body verbatim. Report that conservatively as response
		// truncation, in addition to the bounded SQL prefix case.
		responseTruncated := preview.StoredBytes() > len(preview.Body()) ||
			(strings.TrimSpace(preview.Body()) != "" && summary != preview.Body())
		extent, err := apptypes.EventBodyExtentOf(
			preview.OriginalBytes(), preview.StoredBytes(), preview.IngestTruncated(),
			preview.StorageTruncated(), domtypes.None[int](),
		)
		if err != nil {
			return nil, nil, xerrors.Errorf("failed to build recent command extent: %w", err)
		}
		item, err := apptypes.RecentCommandSummaryOf(
			preview.EventID(), summary, responseTruncated, extent, preview.CreatedAt(),
		)
		if err != nil {
			return nil, nil, xerrors.Errorf("failed to build recent command summary: %w", err)
		}
		commands = append(commands, summary)
		items = append(items, item)
	}
	return commands, items, nil
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
		"",
	)
	if err != nil {
		return "", xerrors.Errorf("failed to list compact summary events for context pack: %w", err)
	}
	for _, event := range events {
		body := event.Body()
		// Skip PreCompact snapshots: new rows carry source_hook =
		// "pre_compact"; legacy (pre-#672) rows still carry the
		// "[phase:pre-compact]" body prefix and must keep working
		// through the migration window.
		if event.SourceHook() == "pre_compact" ||
			strings.HasPrefix(strings.TrimSpace(body), domtypes.EventBodyMarkerCompactPreSnapshot) {
			continue
		}
		return extractCompactSummarySignal(body), nil
	}
	return "", nil
}

func (b *contextPackBuilder) loadMemories(
	ctx context.Context,
	session apptypes.SessionSummary,
	requestedWorkspace domtypes.Workspace,
	limit int,
	preset apptypes.MemoryRetrievalPreset,
	includeCandidates bool,
	asOf domtypes.Optional[time.Time],
) (contextPackMemoryLoad, error) {
	if b.memoryQuery == nil || limit == 0 {
		return contextPackMemoryLoad{}, nil
	}

	scopes := relevantMemoryScopes(session, requestedWorkspace)
	if len(scopes) == 0 {
		return contextPackMemoryLoad{}, nil
	}

	acceptedBuilder := apptypes.NewMemoryListCriteriaBuilder(limit).Scopes(scopes)
	if preset != "" {
		// Preset wins for context packs: callers asked for a specific
		// retrieval shape (resume / review / incident), so we honor
		// its Statuses + MemoryTypes defaults.
		acceptedBuilder = preset.ApplyToMemoryListCriteriaBuilder(acceptedBuilder)
	} else {
		// Default handoff / MCP context is accepted-only. Candidate
		// memories are untrusted backlog and must not be mixed into the
		// durable-memory context unless the caller opts into the separate
		// review section.
		acceptedBuilder = acceptedBuilder.Status(domtypes.MemoryStatusAccepted)
	}
	if asOfValue, ok := asOf.Value(); ok {
		acceptedBuilder = acceptedBuilder.AsOf(asOfValue)
	}
	trusted, err := b.memoryQuery.List(ctx, acceptedBuilder.Build())
	if err != nil {
		return contextPackMemoryLoad{}, xerrors.Errorf("failed to list accepted durable memories for context pack: %w", err)
	}

	candidateBuilder := apptypes.NewMemoryListCriteriaBuilder(limit).
		Scopes(scopes).
		Status(domtypes.MemoryStatusCandidate).
		Sources(contextPackCandidateMemorySources())
	if preset != "" {
		candidateBuilder = preset.ApplyMemoryTypeFiltersToMemoryListCriteriaBuilder(candidateBuilder)
	}
	if asOfValue, ok := asOf.Value(); ok {
		candidateBuilder = candidateBuilder.AsOf(asOfValue)
	}
	candidates, err := b.memoryQuery.List(ctx, candidateBuilder.Build())
	if err != nil {
		return contextPackMemoryLoad{}, xerrors.Errorf("failed to list candidate durable memories for context pack: %w", err)
	}

	needsReview := []apptypes.MemorySummary(nil)
	if includeCandidates {
		needsReview = candidates
	}
	return contextPackMemoryLoad{
		trusted:        trusted,
		needsReview:    needsReview,
		acceptedCount:  len(trusted),
		candidateCount: len(candidates),
	}, nil
}

type contextPackMemoryLoad struct {
	trusted        []apptypes.MemorySummary
	needsReview    []apptypes.MemorySummary
	acceptedCount  int
	candidateCount int
}

func contextPackCandidateMemorySources() []domtypes.MemorySource {
	return []domtypes.MemorySource{
		domtypes.MemorySourceManual,
		domtypes.MemorySourceExtracted,
		domtypes.MemorySourceRememberIntent,
		domtypes.MemorySourceCompactSummary,
		domtypes.MemorySourceImported,
	}
}

func relevantMemoryScopes(session apptypes.SessionSummary, requestedWorkspace domtypes.Workspace) []domtypes.MemoryScope {
	type scopeKey struct {
		kind string
		key  string
	}

	seen := make(map[scopeKey]struct{})
	scopes := make([]domtypes.MemoryScope, 0, len(session.Agents())+3)
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
	// When parent fallback selected a session under an ancestor workspace,
	// also surface memories scoped to the originally requested child
	// workspace so the canonical resolution covers all three surfaces
	// (sessions, events, memories) consistently.
	if requestedWorkspace.String() != "" && requestedWorkspace != session.Workspace() {
		appendScope(domtypes.WorkspaceScopeOf(requestedWorkspace))
	}
	if session.SessionID().String() != "" {
		appendScope(domtypes.SessionFamilyScopeOf(session.SessionID()))
	}
	for _, agentValue := range session.Agents() {
		agent, err := domtypes.AgentFrom(agentValue)
		if err != nil {
			continue
		}
		appendScope(domtypes.AgentScopeOf(agent))
	}

	return scopes
}

// isStaleActiveSession reports whether the supplied session is an
// unended session whose start is older than staleAfter relative to now.
// The threshold mirrors the existing 24h semantics used by
// session_datasource, session active, and session gc so the handoff
// surface stays consistent with the other stale-aware code paths.
func isStaleActiveSession(session apptypes.SessionSummary, staleAfter time.Duration, now time.Time) bool {
	if staleAfter <= 0 {
		return false
	}
	if _, ended := session.EndedAt().Value(); ended {
		return false
	}
	return session.StartedAt().Before(now.Add(-staleAfter))
}

func summarizeCommand(command string) string {
	if beforeDetails, _, found := strings.Cut(strings.TrimSpace(command), "\n\n"); found {
		command = beforeDetails
	}
	trimmed := strings.Join(strings.Fields(command), " ")
	if trimmed == "" {
		return "-"
	}
	// The handoff RECENT_COMMANDS list renders one row per command, so
	// the shared single-line cap (DefaultHandoffRecentCommandLimit) is
	// applied here. The truncation policy lives in application/types so
	// list, snapshot, and handoff surfaces share the same ellipsis glyph
	// and rune-counting semantics.
	return apptypes.TruncateCommandPayload(trimmed, apptypes.DefaultHandoffRecentCommandLimit).Body
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
