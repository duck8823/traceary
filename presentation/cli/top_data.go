package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// topDataCriteria carries the filter / paging parameters used by
// topDataLoader. Fields are flat strings to keep the boundary easy to
// satisfy from a cobra command and from unit tests; the loader trims
// and converts them into the typed criteria objects expected by the
// underlying usecases.
//
// Limit fields use the convention that a non-positive value disables the
// corresponding load. The live dashboard and snapshot paths opt in pane by
// pane by setting the relevant limits.
type topDataCriteria struct {
	Workspace string
	Client    string
	Agent     string

	SessionLimit       int
	FailureLimit       int
	RecentCommandLimit int
	CandidateLimit     int
	StaleMemoryLimit   int

	// StaleAfter is the threshold past which an unended session is
	// treated as stale. A zero or negative value disables the check.
	StaleAfter time.Duration
	// AllowStale opts the caller in to stale active sessions. When
	// false (the default), stale sessions are dropped from the loaded
	// tree so the operator does not act on abandoned context.
	AllowStale bool
	// Now is the reference time used to evaluate staleness. Zero falls
	// back to time.Now() inside the loader so tests can pin it.
	Now time.Time
}

func (l *topDataLoader) loadDetail(ctx context.Context, req topDetailRequest) (topDetailContent, error) {
	switch req.target.kind {
	case topDetailSession:
		return l.loadSessionDetail(ctx, req)
	case topDetailEvent:
		return l.loadEventDetail(ctx, req)
	case topDetailMemory:
		return l.loadMemoryDetail(ctx, req)
	default:
		return topDetailContent{}, xerrors.Errorf("unsupported detail target")
	}
}

func (l *topDataLoader) loadSessionDetail(ctx context.Context, req topDetailRequest) (topDetailContent, error) {
	if l.session == nil {
		return topDetailContent{}, xerrors.Errorf("session detail loader is not configured")
	}
	lineage, err := l.session.Lineage(ctx, req.target.sessionID)
	if err != nil {
		return topDetailContent{}, xerrors.Errorf("%s: %w", Localize("failed to load session detail", "session detail の取得に失敗しました"), err)
	}
	var events []*model.Event
	if l.event != nil {
		events, err = l.event.List(ctx, apptypes.NewEventListCriteriaBuilder(topDetailRecentEventLimit).
			SessionID(req.target.sessionID).
			Build())
		if err != nil {
			return topDetailContent{}, xerrors.Errorf("%s: %w", Localize("failed to load session events", "session event の取得に失敗しました"), err)
		}
	}
	return topDetailContent{
		title: req.target.title,
		lines: formatTopSessionDetailLines(req.target.sessionID, lineage, events),
	}, nil
}

func (l *topDataLoader) loadEventDetail(ctx context.Context, req topDetailRequest) (topDetailContent, error) {
	if l.event == nil {
		return topDetailContent{}, xerrors.Errorf("event detail loader is not configured")
	}
	details, err := l.event.Show(ctx, req.target.eventID)
	if err != nil {
		return topDetailContent{}, xerrors.Errorf("%s: %w", Localize("failed to load event detail", "event detail の取得に失敗しました"), err)
	}
	var buf bytes.Buffer
	if err := writeEventDetails(&buf, details); err != nil {
		return topDetailContent{}, err
	}
	return topDetailContent{title: req.target.title, lines: splitTopDetailText(buf.String())}, nil
}

func (l *topDataLoader) loadMemoryDetail(ctx context.Context, req topDetailRequest) (topDetailContent, error) {
	if l.memory == nil {
		return topDetailContent{}, xerrors.Errorf("memory detail loader is not configured")
	}
	details, err := l.memory.Show(ctx, req.target.memoryID)
	if err != nil {
		return topDetailContent{}, xerrors.Errorf("%s: %w", Localize("failed to load memory detail", "memory detail の取得に失敗しました"), err)
	}
	var buf bytes.Buffer
	if err := writeMemoryDetails(&buf, details); err != nil {
		return topDetailContent{}, err
	}
	return topDetailContent{title: req.target.title, lines: splitTopDetailText(buf.String())}, nil
}

func formatTopSessionDetailLines(sessionID domtypes.SessionID, lineage []apptypes.SessionSummary, events []*model.Event) []string {
	lines := []string{
		fmt.Sprintf("SESSION_ID: %s", sessionID),
	}
	var selected apptypes.SessionSummary
	for _, summary := range lineage {
		if summary.SessionID() == sessionID {
			selected = summary
			break
		}
	}
	if selected.SessionID() != "" {
		lines = append(lines,
			fmt.Sprintf("ROW: %s", formatTopNodeLineIn(&sessionNode{summary: selected}, "", 0, selected.LatestEventAt(), timeUTC())),
			fmt.Sprintf("LABEL: %s", formatOptionalColumn(selected.Label())),
			fmt.Sprintf("SUMMARY: %s", formatOptionalColumn(selected.Summary())),
		)
	}
	lines = append(lines, "", "LINEAGE:")
	if len(lineage) == 0 {
		lines = append(lines, "- -")
	} else {
		for i, summary := range lineage {
			lines = append(lines, fmt.Sprintf("- %d. %s status=%s workspace=%s agent=%s parent=%s", i+1, summary.SessionID(), summary.Status(), summary.Workspace(), extractSubagentType(summary.Agents()), formatOptionalColumn(summary.ParentSessionID().String())))
		}
	}
	lines = append(lines, "", fmt.Sprintf("RECENT_EVENTS (limit=%d):", topDetailRecentEventLimit))
	if len(events) == 0 {
		lines = append(lines, "- -")
	} else {
		for _, ev := range events {
			lines = appendSessionEventDetailLines(lines, ev)
		}
	}
	return lines
}

func appendSessionEventDetailLines(lines []string, ev *model.Event) []string {
	bodyLines := splitTopDetailText(apptypes.ExtractPlainBody(ev.Body()))
	prefix := fmt.Sprintf("- %s %s %s", ev.CreatedAt().UTC().Format(eventCompactTimeLayout), ev.EventID(), ev.Kind())
	if len(bodyLines) == 0 {
		return append(lines, prefix)
	}
	lines = append(lines, fmt.Sprintf("%s %s", prefix, bodyLines[0]))
	for _, line := range bodyLines[1:] {
		lines = append(lines, "  "+line)
	}
	return lines
}

func splitTopDetailText(text string) []string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func timeUTC() *time.Location {
	return time.UTC
}

// topDataSnapshot bundles every data slice the redesigned top dashboard
// needs into a single value so the cobra command and tests can assert
// against one shape.
type topDataSnapshot struct {
	Sessions                     []*sessionNode
	Failures                     []*model.Event
	RecentCommands               []*model.Event
	Candidates                   []apptypes.MemorySummary
	RememberIntentCandidateCount int
	StaleMemories                apptypes.StaleMemoryListResult
	Reliability                  topReliabilityMetrics

	// StaleAfter is the threshold the loader used to evaluate session
	// staleness; a zero or negative value means the check was disabled.
	StaleAfter time.Duration
	// AllowStale reports whether the snapshot retained stale active
	// sessions. Consumers use this together with per-node is_stale
	// metadata to distinguish active vs stale-active rows in the JSON
	// snapshot envelope.
	AllowStale bool
	// Now is the reference time staleness was evaluated against.
	Now time.Time
}

type topReliabilityMetrics struct {
	StaleActiveSessionCount int

	AcceptedMemoryCount  int
	CandidateMemoryCount int
	MemoryScanLimit      int
	MemoryScanLimited    bool

	CandidateAge  topCandidateAgeMetrics
	LargePayloads topLargePayloadMetrics
}

type topCandidateAgeMetrics struct {
	Count      int
	Oldest     time.Time
	Newest     time.Time
	OldestAge  time.Duration
	AverageAge time.Duration
}

type topLargePayloadMetrics struct {
	Count              int
	RecentCommandCount int
	RecentFailureCount int
	SampledEventCount  int
	BodyLimitRunes     int
}

const topReliabilityMemoryScanLimit = 2000

// topDataLoader fetches every data slice the redesigned `traceary top`
// dashboard needs (active session tree, recent failures, recent
// commands, candidate memories, and stale memories). It is the testable
// seam between the cobra command and the application layer; the cobra
// command keeps its current snapshot output in this issue and routes its
// session fetch through the loader so future panes can be added without
// re-wiring the command (#928 / #929).
type topDataLoader struct {
	session usecase.SessionUsecase
	event   usecase.EventUsecase
	memory  usecase.MemoryUsecase
}

// newTopDataLoader constructs a topDataLoader backed by the given
// usecases. Any of session / event / memory may be nil; the
// corresponding loader method returns (nil, nil) so callers that only
// wired in a subset still operate. The current `traceary top` only
// needs sessions, but the same loader will serve the future
// multi-pane dashboard once the rest is rendered.
func newTopDataLoader(session usecase.SessionUsecase, event usecase.EventUsecase, memory usecase.MemoryUsecase) *topDataLoader {
	return &topDataLoader{session: session, event: event, memory: memory}
}

// loadSessions returns the active session tree filtered by the
// supplied criteria. The output is the same `[]*sessionNode` that the
// snapshot text / JSON renderers already expect, so the cobra command
// can swap the previous inline implementation for this call without
// changing user-visible output.
func (l *topDataLoader) loadSessions(ctx context.Context, c topDataCriteria) ([]*sessionNode, error) {
	roots, _, err := l.loadSessionsWithReliability(ctx, c)
	return roots, err
}

func (l *topDataLoader) loadSessionsWithReliability(ctx context.Context, c topDataCriteria) ([]*sessionNode, int, error) {
	if l.session == nil || c.SessionLimit <= 0 {
		return nil, 0, nil
	}
	workspace := strings.TrimSpace(c.Workspace)
	client := strings.TrimSpace(c.Client)
	agent := strings.TrimSpace(c.Agent)
	criteria := apptypes.NewSessionListCriteriaBuilder(c.SessionLimit).
		Workspace(domtypes.Workspace(workspace)).
		Client(domtypes.Client(client)).
		Agent(domtypes.Agent(agent)).
		ActiveOnly(true).
		Build()
	summaries, err := l.session.List(ctx, criteria)
	if err != nil {
		return nil, 0, xerrors.Errorf("%s: %w", Localize("failed to list sessions", "セッション一覧の取得に失敗しました"), err)
	}
	now := topDataNow(c)
	staleActiveCount := countStaleActiveSummaries(summaries, c.StaleAfter, now)
	// Drop stale active leaves before lineage expansion when the caller
	// did not opt in. We filter before expansion so retained ended
	// ancestors do not stay around for a leaf the operator never sees.
	if !c.AllowStale && c.StaleAfter > 0 {
		filtered := summaries[:0]
		for _, summary := range summaries {
			if topDataSummaryIsStale(summary, c.StaleAfter, now) {
				continue
			}
			filtered = append(filtered, summary)
		}
		summaries = filtered
	}
	expanded, err := l.expandSessionLineages(ctx, summaries)
	if err != nil {
		return nil, 0, err
	}
	return filterTopSessionTree(buildActiveSessionTreeWithOptions(expanded, c.AllowStale, c.StaleAfter, now), topCommandOptions{
		workspace: workspace,
		client:    client,
		agent:     agent,
	}), staleActiveCount, nil
}

// topDataNow returns the reference time the loader should use for
// staleness checks. Tests can pin it via topDataCriteria.Now; production
// callers leave the field zero and the loader falls back to time.Now().
func topDataNow(c topDataCriteria) time.Time {
	if c.Now.IsZero() {
		return time.Now()
	}
	return c.Now
}

// topDataSummaryIsStale reports whether the supplied summary is an
// unended session whose start is older than staleAfter relative to now.
// It mirrors the semantics used by session_active and session gc so the
// stale signal stays consistent across surfaces.
func topDataSummaryIsStale(summary apptypes.SessionSummary, staleAfter time.Duration, now time.Time) bool {
	if staleAfter <= 0 {
		return false
	}
	if _, ended := summary.EndedAt().Value(); ended {
		return false
	}
	return summary.StartedAt().Before(now.Add(-staleAfter))
}

func countStaleActiveSummaries(summaries []apptypes.SessionSummary, staleAfter time.Duration, now time.Time) int {
	count := 0
	for _, summary := range summaries {
		if topDataSummaryIsStale(summary, staleAfter, now) {
			count++
		}
	}
	return count
}

// expandSessionLineages walks every active session's lineage so root
// ancestors retained for an active child show up alongside the active
// row even when the root itself is `ended`. Duplicates are collapsed
// against the first occurrence so the resulting summary list is
// suitable for tree building.
func (l *topDataLoader) expandSessionLineages(ctx context.Context, summaries []apptypes.SessionSummary) ([]apptypes.SessionSummary, error) {
	if len(summaries) == 0 {
		return nil, nil
	}
	merged := make([]apptypes.SessionSummary, 0, len(summaries))
	seen := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		lineage, err := l.session.Lineage(ctx, summary.SessionID())
		if err != nil {
			return nil, xerrors.Errorf("%s: %w", Localize("failed to load session lineage", "セッション lineage の取得に失敗しました"), err)
		}
		if len(lineage) == 0 {
			lineage = []apptypes.SessionSummary{summary}
		}
		for _, lineageSummary := range lineage {
			key := lineageSummary.SessionID().String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, lineageSummary)
		}
	}
	return merged, nil
}

// loadFailures returns recent failed command events ordered newest
// first, filtered by the same workspace / client / agent the operator
// supplied to the top command. A non-positive FailureLimit disables
// the load and returns (nil, nil) so callers can opt in pane by pane.
func (l *topDataLoader) loadFailures(ctx context.Context, c topDataCriteria) ([]*model.Event, error) {
	if l.event == nil || c.FailureLimit <= 0 {
		return nil, nil
	}
	criteria := apptypes.NewEventListCriteriaBuilder(c.FailureLimit).
		Workspace(domtypes.Workspace(strings.TrimSpace(c.Workspace))).
		Client(domtypes.Client(strings.TrimSpace(c.Client))).
		Agent(domtypes.Agent(strings.TrimSpace(c.Agent))).
		FailuresOnly(true).
		Build()
	events, err := l.event.List(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list failures", "failures の取得に失敗しました"), err)
	}
	return events, nil
}

// loadRecentCommands returns recent command_executed events ordered
// newest first. It is the data source for the future "recent commands"
// pane in the redesigned dashboard. A non-positive RecentCommandLimit
// disables the load.
func (l *topDataLoader) loadRecentCommands(ctx context.Context, c topDataCriteria) ([]*model.Event, error) {
	if l.event == nil || c.RecentCommandLimit <= 0 {
		return nil, nil
	}
	criteria := apptypes.NewEventListCriteriaBuilder(c.RecentCommandLimit).
		Kind(domtypes.EventKindCommandExecuted).
		Workspace(domtypes.Workspace(strings.TrimSpace(c.Workspace))).
		Client(domtypes.Client(strings.TrimSpace(c.Client))).
		Agent(domtypes.Agent(strings.TrimSpace(c.Agent))).
		Build()
	events, err := l.event.List(ctx, criteria)
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list recent commands", "recent commands の取得に失敗しました"), err)
	}
	return events, nil
}

// loadCandidates returns recent inbox candidate memories ordered with
// the same remember-intent priority `memory inbox list` uses, so the
// future top pane stays consistent with the inbox views. Workspace and
// Agent on the criteria narrow the result to scoped candidates so the
// pane mirrors the rest of the dashboard's filter; Client has no memory
// scope equivalent and is intentionally ignored. A non-positive
// CandidateLimit disables the load.
func (l *topDataLoader) loadCandidates(ctx context.Context, c topDataCriteria) ([]apptypes.MemorySummary, error) {
	if l.memory == nil || c.CandidateLimit <= 0 {
		return nil, nil
	}
	builder := apptypes.NewMemoryListCriteriaBuilder(c.CandidateLimit).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusCandidate}).
		RememberIntentPriority(true)
	if workspace := strings.TrimSpace(c.Workspace); workspace != "" {
		builder = builder.Scope(domtypes.WorkspaceScopeOf(domtypes.Workspace(workspace)))
	}
	if agent := strings.TrimSpace(c.Agent); agent != "" {
		builder = builder.Scope(domtypes.AgentScopeOf(domtypes.Agent(agent)))
	}
	summaries, err := l.memory.List(ctx, builder.Build())
	if err != nil {
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list candidate memories", "候補 memory の取得に失敗しました"), err)
	}
	return summaries, nil
}

// loadStaleMemories returns stale durable memories for the future stale pane.
// Workspace and Agent narrow the result to memory scopes, mirroring the
// candidate pane; Client has no memory scope equivalent and is intentionally
// ignored. A non-positive StaleMemoryLimit disables the load.
func (l *topDataLoader) loadStaleMemories(ctx context.Context, c topDataCriteria) (apptypes.StaleMemoryListResult, error) {
	if l.memory == nil || c.StaleMemoryLimit <= 0 {
		return apptypes.StaleMemoryListResult{}, nil
	}
	builder := apptypes.NewStaleMemoryListCriteriaBuilder(c.StaleMemoryLimit)
	if workspace := strings.TrimSpace(c.Workspace); workspace != "" {
		builder = builder.Scope(domtypes.WorkspaceScopeOf(domtypes.Workspace(workspace)))
	}
	if agent := strings.TrimSpace(c.Agent); agent != "" {
		builder = builder.Scope(domtypes.AgentScopeOf(domtypes.Agent(agent)))
	}
	result, err := l.memory.ListStale(ctx, builder.Build())
	if err != nil {
		return apptypes.StaleMemoryListResult{}, xerrors.Errorf("%s: %w", Localize("failed to list stale memories", "stale memory の取得に失敗しました"), err)
	}
	return result, nil
}

// loadSnapshot fetches the five data slices in a single call. Each
// pane is opt-in via its limit field on topDataCriteria, so the
// current `traceary top` (sessions only) and the upcoming multi-pane
// dashboard share one entry point.
func (l *topDataLoader) loadSnapshot(ctx context.Context, c topDataCriteria) (topDataSnapshot, error) {
	sessions, staleActiveCount, err := l.loadSessionsWithReliability(ctx, c)
	if err != nil {
		return topDataSnapshot{}, err
	}
	failures, err := l.loadFailures(ctx, c)
	if err != nil {
		return topDataSnapshot{}, err
	}
	commands, err := l.loadRecentCommands(ctx, c)
	if err != nil {
		return topDataSnapshot{}, err
	}
	candidates, err := l.loadCandidates(ctx, c)
	if err != nil {
		return topDataSnapshot{}, err
	}
	staleMemories, err := l.loadStaleMemories(ctx, c)
	if err != nil {
		return topDataSnapshot{}, err
	}
	reliability, err := l.loadReliabilityMetrics(ctx, c, topReliabilityInputs{
		StaleActiveSessionCount: staleActiveCount,
		Failures:                failures,
		RecentCommands:          commands,
	})
	if err != nil {
		return topDataSnapshot{}, err
	}
	return topDataSnapshot{
		Sessions:                     sessions,
		Failures:                     failures,
		RecentCommands:               commands,
		Candidates:                   candidates,
		RememberIntentCandidateCount: countCandidatesBySource(candidates, domtypes.MemorySourceRememberIntent),
		StaleMemories:                staleMemories,
		Reliability:                  reliability,
		StaleAfter:                   c.StaleAfter,
		AllowStale:                   c.AllowStale,
		Now:                          topDataNow(c),
	}, nil
}

type topReliabilityInputs struct {
	StaleActiveSessionCount int
	Failures                []*model.Event
	RecentCommands          []*model.Event
}

func (l *topDataLoader) loadReliabilityMetrics(ctx context.Context, c topDataCriteria, inputs topReliabilityInputs) (topReliabilityMetrics, error) {
	metrics := topReliabilityMetrics{
		StaleActiveSessionCount: inputs.StaleActiveSessionCount,
		LargePayloads:           topLargePayloadMetricsOf(inputs.Failures, inputs.RecentCommands, apptypes.DefaultTopSnapshotBodyLimit),
	}
	if l.memory == nil {
		return metrics, nil
	}
	memories, err := l.memory.List(ctx, topReliabilityMemoryCriteria(c))
	if err != nil {
		return topReliabilityMetrics{}, xerrors.Errorf("%s: %w", Localize("failed to list memories for reliability metrics", "reliability metrics 用 memory 一覧の取得に失敗しました"), err)
	}
	metrics.MemoryScanLimit = topReliabilityMemoryScanLimit
	metrics.MemoryScanLimited = len(memories) >= topReliabilityMemoryScanLimit
	for _, summary := range memories {
		switch summary.Status() {
		case domtypes.MemoryStatusAccepted:
			metrics.AcceptedMemoryCount++
		case domtypes.MemoryStatusCandidate:
			metrics.CandidateMemoryCount++
			metrics.CandidateAge = topCandidateAgeMetricsAdd(metrics.CandidateAge, summary.UpdatedAt(), topDataNow(c))
		}
	}
	return metrics, nil
}

func topReliabilityMemoryCriteria(c topDataCriteria) apptypes.MemoryListCriteria {
	builder := apptypes.NewMemoryListCriteriaBuilder(topReliabilityMemoryScanLimit).
		Statuses([]domtypes.MemoryStatus{domtypes.MemoryStatusAccepted, domtypes.MemoryStatusCandidate})
	builder = applyTopMemoryScopes(builder, c)
	return builder.Build()
}

func applyTopMemoryScopes(builder *apptypes.MemoryListCriteriaBuilder, c topDataCriteria) *apptypes.MemoryListCriteriaBuilder {
	if workspace := strings.TrimSpace(c.Workspace); workspace != "" {
		builder = builder.Scope(domtypes.WorkspaceScopeOf(domtypes.Workspace(workspace)))
	}
	if agent := strings.TrimSpace(c.Agent); agent != "" {
		builder = builder.Scope(domtypes.AgentScopeOf(domtypes.Agent(agent)))
	}
	return builder
}

func topCandidateAgeMetricsAdd(metrics topCandidateAgeMetrics, updatedAt time.Time, now time.Time) topCandidateAgeMetrics {
	age := now.Sub(updatedAt)
	if age < 0 {
		age = 0
	}
	if metrics.Count == 0 || updatedAt.Before(metrics.Oldest) {
		metrics.Oldest = updatedAt
		metrics.OldestAge = age
	}
	if metrics.Count == 0 || updatedAt.After(metrics.Newest) {
		metrics.Newest = updatedAt
	}
	totalAge := metrics.AverageAge*time.Duration(metrics.Count) + age
	metrics.Count++
	metrics.AverageAge = totalAge / time.Duration(metrics.Count)
	return metrics
}

func topLargePayloadMetricsOf(failures []*model.Event, commands []*model.Event, limit int) topLargePayloadMetrics {
	metrics := topLargePayloadMetrics{
		SampledEventCount: len(failures) + len(commands),
		BodyLimitRunes:    limit,
	}
	metrics.RecentFailureCount = countLargePayloadEvents(failures, limit)
	metrics.RecentCommandCount = countLargePayloadEvents(commands, limit)
	metrics.Count = metrics.RecentFailureCount + metrics.RecentCommandCount
	return metrics
}

func countLargePayloadEvents(events []*model.Event, limit int) int {
	count := 0
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if apptypes.TruncateCommandPayload(apptypes.ExtractPlainBody(ev.Body()), limit).Truncated {
			count++
		}
	}
	return count
}

func countCandidatesBySource(candidates []apptypes.MemorySummary, source domtypes.MemorySource) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.Source() == source {
			count++
		}
	}
	return count
}
