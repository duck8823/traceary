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
	Sessions       []*sessionNode
	Failures       []*model.Event
	RecentCommands []*model.Event
	Candidates     []apptypes.MemorySummary
	StaleMemories  apptypes.StaleMemoryListResult
}

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
	if l.session == nil || c.SessionLimit <= 0 {
		return nil, nil
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
		return nil, xerrors.Errorf("%s: %w", Localize("failed to list sessions", "セッション一覧の取得に失敗しました"), err)
	}
	expanded, err := l.expandSessionLineages(ctx, summaries)
	if err != nil {
		return nil, err
	}
	return filterTopSessionTree(buildActiveSessionTree(expanded), topCommandOptions{
		workspace: workspace,
		client:    client,
		agent:     agent,
	}), nil
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
	sessions, err := l.loadSessions(ctx, c)
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
	return topDataSnapshot{
		Sessions:       sessions,
		Failures:       failures,
		RecentCommands: commands,
		Candidates:     candidates,
		StaleMemories:  staleMemories,
	}, nil
}
