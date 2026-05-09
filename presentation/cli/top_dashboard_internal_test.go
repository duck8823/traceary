package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

// stubTopLoader is a deterministic topSnapshotLoader that returns the
// canned snapshot every time. The model talks to the loader via the
// returned tea.Cmd, so wrapping it lets tests assert on Update transitions
// without spinning up the application use cases.
type stubTopLoader struct {
	snapshot topDataSnapshot
	err      error
	calls    int
}

func (s *stubTopLoader) loadSnapshot(_ context.Context, _ topDataCriteria) (topDataSnapshot, error) {
	s.calls++
	return s.snapshot, s.err
}

// fixedDashboardNow pins "now" so idle markers and last-event timestamps
// stay deterministic across test runs.
var fixedDashboardNow = time.Date(2026, 5, 7, 14, 0, 0, 0, time.UTC)

func newDashboardTestModel(t *testing.T, loader topSnapshotLoader) topModel {
	t.Helper()
	return newTopModel(topModelConfig{
		Keys:            tui.DefaultKeyMap(),
		Actions:         defaultTopPaneActionKeys(),
		Styles:          tui.DefaultStyles(),
		Loader:          loader,
		Criteria:        topDataCriteria{},
		Idle:            10 * time.Minute,
		Now:             func() time.Time { return fixedDashboardNow },
		Location:        time.UTC,
		RefreshInterval: 0, // disable ticker so tests stay deterministic
		LoaderCtx:       context.Background(),
	})
}

func dashboardSessionNode(id string, latest time.Time) *sessionNode {
	summary := apptypes.SessionSummaryOf(
		domtypes.SessionID(id),
		domtypes.Workspace("duck8823/traceary"),
		latest.Add(-10*time.Minute),
		domtypes.None[time.Time](),
		"active",
		3,
		1,
		[]string{"claude"},
		"",
		"",
		domtypes.SessionID(""),
		domtypes.Client("claude"),
		latest,
		apptypes.SessionSummaryLatestEventOf(domtypes.EventKindTranscript, "row "+id),
	)
	return &sessionNode{summary: summary}
}

func dashboardEvent(id string, kind domtypes.EventKind, body string) *model.Event {
	return model.EventOf(
		domtypes.EventID(id),
		kind,
		domtypes.Client("claude"),
		domtypes.Agent("claude"),
		domtypes.SessionID("session-1"),
		domtypes.Workspace("duck8823/traceary"),
		body,
		fixedDashboardNow,
	)
}

func dashboardCandidate(t *testing.T, id string, fact string) apptypes.MemorySummary {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypePreference,
		domtypes.WorkspaceScopeOf(workspace),
		fact,
		domtypes.MemoryStatusCandidate,
		domtypes.ConfidenceMedium,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedDashboardNow,
		domtypes.None[time.Time](),
		fixedDashboardNow,
		fixedDashboardNow,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf: %v", err)
	}
	return summary
}

func dashboardStaleMemory(t *testing.T, id string, reason apptypes.StaleMemoryReason, fact string) apptypes.StaleMemoryRow {
	t.Helper()
	workspace, err := domtypes.WorkspaceFrom("duck8823/traceary")
	if err != nil {
		t.Fatalf("WorkspaceFrom: %v", err)
	}
	status := domtypes.MemoryStatusAccepted
	switch reason {
	case apptypes.StaleMemoryReasonExpired:
		status = domtypes.MemoryStatusExpired
	case apptypes.StaleMemoryReasonSuperseded:
		status = domtypes.MemoryStatusSuperseded
	}
	summary, err := apptypes.MemorySummaryOf(
		domtypes.MemoryID(id),
		domtypes.MemoryTypeDecision,
		domtypes.WorkspaceScopeOf(workspace),
		fact,
		status,
		domtypes.ConfidenceHigh,
		domtypes.MemorySourceManual,
		domtypes.None[domtypes.MemoryID](),
		domtypes.None[time.Time](),
		fixedDashboardNow,
		domtypes.None[time.Time](),
		fixedDashboardNow,
		fixedDashboardNow,
	)
	if err != nil {
		t.Fatalf("MemorySummaryOf(stale): %v", err)
	}
	row, err := apptypes.StaleMemoryRowOf(summary, reason)
	if err != nil {
		t.Fatalf("StaleMemoryRowOf: %v", err)
	}
	return row
}

func dashboardStaleResult(t *testing.T, count int, rows ...apptypes.StaleMemoryRow) apptypes.StaleMemoryListResult {
	t.Helper()
	result, err := apptypes.StaleMemoryListResultOf(count, rows)
	if err != nil {
		t.Fatalf("StaleMemoryListResultOf: %v", err)
	}
	return result
}

// applySnapshot is a test-only helper that drives the model through the
// same path the production tea.Cmd takes: it calls the loader (synchronously
// here) and feeds the resulting topSnapshotMsg back into Update.
func applySnapshot(t *testing.T, m topModel) topModel {
	t.Helper()
	cmd := m.fetchSnapshotCmd()
	msg := cmd()
	updated, _ := m.Update(msg)
	return updated.(topModel)
}

func resize(m topModel, width, height int) topModel {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(topModel)
}

func sendKey(m topModel, key tea.KeyMsg) topModel {
	updated, _ := m.Update(key)
	return updated.(topModel)
}

func TestTopModel_InitialFetchPopulatesSnapshot(t *testing.T) {
	t.Parallel()
	loader := &stubTopLoader{snapshot: topDataSnapshot{
		Sessions: []*sessionNode{dashboardSessionNode("root", fixedDashboardNow.Add(-time.Minute))},
	}}
	m := newDashboardTestModel(t, loader)

	m = applySnapshot(t, m)

	if loader.calls != 1 {
		t.Fatalf("loader.calls = %d, want 1", loader.calls)
	}
	if !m.loaded {
		t.Fatalf("model.loaded = false, want true after applying snapshot")
	}
	if len(m.snapshot.Sessions) != 1 {
		t.Fatalf("snapshot.Sessions length = %d, want 1", len(m.snapshot.Sessions))
	}
}

func TestTopModel_RefreshTickIssuesNewFetch(t *testing.T) {
	t.Parallel()
	loader := &stubTopLoader{}
	m := newDashboardTestModel(t, loader)

	updated, cmd := m.Update(topRefreshTickMsg{})
	if cmd == nil {
		t.Fatalf("topRefreshTickMsg should produce a fetch cmd")
	}
	// Run the returned cmd to confirm it lands as a topSnapshotMsg.
	msg := cmd()
	if _, ok := msg.(topSnapshotMsg); !ok {
		t.Fatalf("tick cmd returned %T, want topSnapshotMsg", msg)
	}
	_ = updated
	if loader.calls != 1 {
		t.Fatalf("loader.calls = %d, want 1 after tick", loader.calls)
	}
}

func TestTopModel_RefreshKeyTriggersLoader(t *testing.T) {
	t.Parallel()
	loader := &stubTopLoader{}
	m := newDashboardTestModel(t, loader)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatalf("refresh key should produce a fetch cmd")
	}
	if _, ok := cmd().(topSnapshotMsg); !ok {
		t.Fatalf("refresh cmd should return topSnapshotMsg")
	}
	if loader.calls != 1 {
		t.Fatalf("loader.calls = %d, want 1 after refresh key", loader.calls)
	}
	_ = updated
}

func TestTopModel_QuitKeyReturnsTeaQuit(t *testing.T) {
	t.Parallel()
	m := newDashboardTestModel(t, &stubTopLoader{})

	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyEsc},
	} {
		_, cmd := m.Update(k)
		if cmd == nil {
			t.Fatalf("Quit key %v should return tea.Quit, got nil cmd", k)
		}
		if msg := cmd(); msg == nil {
			t.Fatalf("Quit cmd %v returned nil msg, want tea.QuitMsg", k)
		}
	}
}

func TestTopModel_TabCyclesPanesForward(t *testing.T) {
	t.Parallel()
	m := newDashboardTestModel(t, &stubTopLoader{})

	want := []topPane{
		topPaneFailures,
		topPaneRecentCommands,
		topPaneCandidates,
		topPaneStaleMemories,
		topPaneSessions,
	}
	for i, expect := range want {
		m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab})
		if m.pane != expect {
			t.Fatalf("step %d: pane = %v, want %v", i, m.pane, expect)
		}
	}
}

func TestTopModel_ShiftTabCyclesPanesBackward(t *testing.T) {
	t.Parallel()
	m := newDashboardTestModel(t, &stubTopLoader{})

	want := []topPane{
		topPaneStaleMemories,
		topPaneCandidates,
		topPaneRecentCommands,
		topPaneFailures,
		topPaneSessions,
	}
	for i, expect := range want {
		m = sendKey(m, tea.KeyMsg{Type: tea.KeyShiftTab})
		if m.pane != expect {
			t.Fatalf("step %d: pane = %v, want %v", i, m.pane, expect)
		}
	}
}

func TestTopModel_ScrollDownAndUpIsBoundedByPaneContent(t *testing.T) {
	t.Parallel()
	events := make([]*model.Event, 20)
	for i := range events {
		events[i] = dashboardEvent("e-"+string(rune('a'+i)), domtypes.EventKindCommandExecuted, "row "+string(rune('a'+i)))
	}
	loader := &stubTopLoader{snapshot: topDataSnapshot{Failures: events}}
	m := newDashboardTestModel(t, loader)
	m = applySnapshot(t, m)
	m = resize(m, 80, 30) // ~ 5 rows per pane after chrome

	// Focus the failures pane.
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.pane != topPaneFailures {
		t.Fatalf("pane = %v, want failures", m.pane)
	}

	viewport := m.paneViewportRows()
	// Scroll down by enough to push past the end; offset must clamp.
	for range len(events) + 5 {
		m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got, want := m.offsets[topPaneFailures], len(events)-viewport; got != want {
		t.Fatalf("offset after over-scroll = %d, want %d (len=%d viewport=%d)", got, want, len(events), viewport)
	}

	// Scroll up past the top; offset must clamp at zero.
	for range len(events) + 5 {
		m = sendKey(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.offsets[topPaneFailures] != 0 {
		t.Fatalf("offset after over-scroll up = %d, want 0", m.offsets[topPaneFailures])
	}
}

func TestTopModel_HomeAndEndJumpToBoundaries(t *testing.T) {
	t.Parallel()
	events := make([]*model.Event, 30)
	for i := range events {
		events[i] = dashboardEvent("e-"+string(rune('a'+i)), domtypes.EventKindCommandExecuted, "row")
	}
	loader := &stubTopLoader{snapshot: topDataSnapshot{Failures: events}}
	m := newDashboardTestModel(t, loader)
	m = applySnapshot(t, m)
	m = resize(m, 80, 30)
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab}) // focus failures

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnd})
	viewport := m.paneViewportRows()
	wantEnd := len(events) - viewport
	if wantEnd < 0 {
		wantEnd = 0
	}
	if m.offsets[topPaneFailures] != wantEnd {
		t.Fatalf("End offset = %d, want %d", m.offsets[topPaneFailures], wantEnd)
	}
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyHome})
	if m.offsets[topPaneFailures] != 0 {
		t.Fatalf("Home offset = %d, want 0", m.offsets[topPaneFailures])
	}
}

func TestTopModel_PageDownAdvancesByViewport(t *testing.T) {
	t.Parallel()
	events := make([]*model.Event, 30)
	for i := range events {
		events[i] = dashboardEvent("e-"+string(rune('a'+i)), domtypes.EventKindCommandExecuted, "row")
	}
	m := newDashboardTestModel(t, &stubTopLoader{snapshot: topDataSnapshot{Failures: events}})
	m = applySnapshot(t, m)
	m = resize(m, 80, 30)
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab})

	viewport := m.paneViewportRows()
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyPgDown})
	if got := m.offsets[topPaneFailures]; got != viewport {
		t.Fatalf("PgDn offset = %d, want %d", got, viewport)
	}
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.offsets[topPaneFailures] != 0 {
		t.Fatalf("PgUp offset = %d, want 0", m.offsets[topPaneFailures])
	}
}

func TestTopModel_HelpModeTogglesAndSwallowsNavigation(t *testing.T) {
	t.Parallel()
	loader := &stubTopLoader{snapshot: topDataSnapshot{
		Sessions: []*sessionNode{dashboardSessionNode("root", fixedDashboardNow)},
	}}
	m := newDashboardTestModel(t, loader)
	m = applySnapshot(t, m)
	m = resize(m, 100, 40)

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.mode != topModeHelp {
		t.Fatalf("mode = %v, want topModeHelp", m.mode)
	}
	view := m.View()
	if !strings.Contains(view, "help") {
		t.Fatalf("help view did not contain `help` marker:\n%s", view)
	}

	// Tab should not change the focused pane while help is up.
	before := m.pane
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.pane != before {
		t.Fatalf("tab in help mode changed pane to %v, want %v", m.pane, before)
	}

	m = sendKey(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.mode != topModeBrowse {
		t.Fatalf("mode = %v after second `?`, want topModeBrowse", m.mode)
	}
}

func TestTopModel_EmptySnapshotRendersPerPaneEmptyState(t *testing.T) {
	t.Parallel()
	m := newDashboardTestModel(t, &stubTopLoader{})
	m = applySnapshot(t, m)
	m = resize(m, 100, 40)

	view := m.View()
	for _, expect := range []string{
		"No active sessions found.",
		"No matching records.",
		"No candidate durable memories",
		"No stale memories.",
	} {
		if !strings.Contains(view, expect) {
			t.Fatalf("View() missing empty-state %q. Full view:\n%s", expect, view)
		}
	}
}

func TestTopModel_LoadErrorIsRenderedInPanes(t *testing.T) {
	t.Parallel()
	loadErr := errors.New("boom")
	m := newDashboardTestModel(t, &stubTopLoader{err: loadErr})
	m = applySnapshot(t, m)
	m = resize(m, 100, 40)

	view := m.View()
	if !strings.Contains(view, "boom") {
		t.Fatalf("View() did not surface load error %q. Full view:\n%s", loadErr, view)
	}
}

func TestTopModel_NarrowTerminalKeepsViewportAtLeastOne(t *testing.T) {
	t.Parallel()
	m := newDashboardTestModel(t, &stubTopLoader{snapshot: topDataSnapshot{
		Sessions: []*sessionNode{dashboardSessionNode("root", fixedDashboardNow)},
	}})
	m = applySnapshot(t, m)
	m = resize(m, 30, 8) // intentionally small

	if got := m.paneViewportRows(); got < 1 {
		t.Fatalf("paneViewportRows = %d, want >= 1 even on a tiny terminal", got)
	}
	view := m.View()
	if view == "" {
		t.Fatalf("View() returned empty string on narrow terminal")
	}
}

func TestTopModel_ResizeClampsExistingOffsets(t *testing.T) {
	t.Parallel()
	events := make([]*model.Event, 30)
	for i := range events {
		events[i] = dashboardEvent("e-"+string(rune('a'+i)), domtypes.EventKindCommandExecuted, "row")
	}
	m := newDashboardTestModel(t, &stubTopLoader{snapshot: topDataSnapshot{Failures: events}})
	m = applySnapshot(t, m)
	m = resize(m, 80, 60)
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab}) // focus failures
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnd})

	// Now expand the terminal vertically; the End offset must shrink so
	// the viewport never points past the end.
	m = resize(m, 80, 200)
	viewport := m.paneViewportRows()
	if got, want := m.offsets[topPaneFailures], max(len(events)-viewport, 0); got != want {
		t.Fatalf("offset after resize = %d, want %d (len=%d viewport=%d)", got, want, len(events), viewport)
	}
}

func TestTopModel_SessionLinesRendersEachNode(t *testing.T) {
	t.Parallel()
	root := dashboardSessionNode("root", fixedDashboardNow)
	child := dashboardSessionNode("child", fixedDashboardNow)
	root.children = []*sessionNode{child}
	m := newDashboardTestModel(t, &stubTopLoader{snapshot: topDataSnapshot{Sessions: []*sessionNode{root}}})
	m = applySnapshot(t, m)

	lines := m.sessionLines(80)
	if len(lines) != 2 {
		t.Fatalf("sessionLines = %d, want 2 (root + child)", len(lines))
	}
	if !strings.Contains(lines[0], "root") {
		t.Fatalf("sessionLines[0] = %q, want to contain `root`", lines[0])
	}
	if !strings.Contains(lines[1], "child") {
		t.Fatalf("sessionLines[1] = %q, want to contain `child`", lines[1])
	}
}

func TestTopModel_CandidateLinesRendersFactAndID(t *testing.T) {
	t.Parallel()
	candidate := dashboardCandidate(t, "mem-1", "prefer table-driven subtests")
	m := newDashboardTestModel(t, &stubTopLoader{snapshot: topDataSnapshot{Candidates: []apptypes.MemorySummary{candidate}}})
	m = applySnapshot(t, m)

	lines := m.candidateLines(120)
	if len(lines) != 1 {
		t.Fatalf("candidateLines = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "mem-1") || !strings.Contains(lines[0], "prefer table-driven subtests") {
		t.Fatalf("candidateLines[0] = %q, want to contain id+fact", lines[0])
	}
}

func TestTopModel_StaleMemoryLinesRendersReasonScopeAndFact(t *testing.T) {
	t.Parallel()
	stale := dashboardStaleMemory(t, "mem-stale-1", apptypes.StaleMemoryReasonSuperseded, "old rollout decision")
	m := newDashboardTestModel(t, &stubTopLoader{snapshot: topDataSnapshot{
		StaleMemories: dashboardStaleResult(t, 3, stale),
	}})
	m = applySnapshot(t, m)

	lines := m.staleMemoryLines(160)
	if len(lines) != 1 {
		t.Fatalf("staleMemoryLines = %d, want 1", len(lines))
	}
	for _, expect := range []string{"mem-stale-1", "decision", "workspace:duck8823/traceary", "superseded", "old rollout decision"} {
		if !strings.Contains(lines[0], expect) {
			t.Fatalf("staleMemoryLines[0] = %q, want to contain %q", lines[0], expect)
		}
	}
}

func TestTopModel_StaleMemoryPaneRendersHeaderCountAndRows(t *testing.T) {
	t.Parallel()
	stale := dashboardStaleMemory(t, "mem-stale-1", apptypes.StaleMemoryReasonExpired, "expired cleanup target")
	m := newDashboardTestModel(t, &stubTopLoader{snapshot: topDataSnapshot{
		StaleMemories: dashboardStaleResult(t, 2, stale),
	}})
	m = applySnapshot(t, m)
	m = resize(m, 120, 40)
	for range 4 {
		m = sendKey(m, tea.KeyMsg{Type: tea.KeyTab})
	}
	if m.pane != topPaneStaleMemories {
		t.Fatalf("pane = %v, want stale memories", m.pane)
	}

	view := m.View()
	for _, expect := range []string{"STALE MEMORIES (count=2)", "mem-stale-1", "expired cleanup target"} {
		if !strings.Contains(view, expect) {
			t.Fatalf("View() missing %q. Full view:\n%s", expect, view)
		}
	}
}

func TestTopModel_TruncateToWidthClampsLongRows(t *testing.T) {
	t.Parallel()
	got := truncateToWidth("abcdefghij", 5)
	if width := runeWidthAt(got); width > 5 {
		t.Fatalf("truncateToWidth returned %q (width=%d), want <= 5", got, width)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncateToWidth(%q,5) = %q, want trailing ellipsis", "abcdefghij", got)
	}
}

// runeWidthAt is a thin alias around runeWidth so the truncate test reads
// without dragging in the production helper's name.
func runeWidthAt(s string) int { return runeWidth(s) }
