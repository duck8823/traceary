package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

// topDashboardRefreshInterval pins how often the dashboard re-fetches its
// data slices. The legacy tcell renderer used 1 second; we keep parity so
// idle dimming and "refresh=1s" advertised in the header line match.
const topDashboardRefreshInterval = time.Second

// topPaneFailureLimit / topPaneRecentCommandLimit / topPaneCandidateLimit /
// topPaneStaleMemoryLimit cap the per-pane rows the loader fetches. The
// session pane re-uses the operator-controlled --limit flag (default 500);
// the secondary panes are summary surfaces that only need a short window.
const (
	topPaneFailureLimit       = 50
	topPaneRecentCommandLimit = 50
	topPaneCandidateLimit     = 25
	topPaneStaleMemoryLimit   = topPaneCandidateLimit
)

// topPane enumerates the focusable panes on the dashboard.
//
// The numeric order matches the visual order so iteration helpers can stay
// arithmetic; tests reference the named constants so a future re-shuffle is
// caught by the compiler.
type topPane int

const (
	topPaneSessions topPane = iota
	topPaneFailures
	topPaneRecentCommands
	topPaneCandidates
	topPaneStaleMemories
)

const topPaneCount = 5

// topMode encodes the sub-screen the model is showing. Browse is the
// dashboard itself; Help is the overlay rendered when the operator presses
// the shared `?` binding.
type topMode int

const (
	topModeBrowse topMode = iota
	topModeHelp
	topModeDetail
)

const topDetailRecentEventLimit = 10

type topDetailKind int

const (
	topDetailNone topDetailKind = iota
	topDetailSession
	topDetailEvent
	topDetailMemory
)

type topDetailTarget struct {
	kind      topDetailKind
	title     string
	sessionID domtypes.SessionID
	eventID   domtypes.EventID
	memoryID  domtypes.MemoryID
}

type topDetailRequest struct {
	pane   topPane
	target topDetailTarget
}

type topDetailContent struct {
	title string
	lines []string
}

type topDetailMsg struct {
	request topDetailRequest
	content topDetailContent
	err     error
}

type topDetailState struct {
	request topDetailRequest
	title   string
	lines   []string
	err     error
	loading bool
}

type topPaneRow struct {
	line   string
	target topDetailTarget
}

// topPaneActionKeys extends the shared tui.KeyMap with bindings that are
// dashboard-specific (pane switching). Quit / Help / Refresh / movement
// stay on the shared map so muscle memory transfers between Traceary's
// interactive surfaces.
type topPaneActionKeys struct {
	NextPane key.Binding
	PrevPane key.Binding
}

func defaultTopPaneActionKeys() topPaneActionKeys {
	return topPaneActionKeys{
		NextPane: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next pane"),
		),
		PrevPane: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev pane"),
		),
	}
}

// topSnapshotLoader is the seam that lets tests drive the dashboard with
// canned data instead of going through the application use cases. The
// production wiring satisfies this with topDataLoader.loadSnapshot.
type topSnapshotLoader interface {
	loadSnapshot(ctx context.Context, c topDataCriteria) (topDataSnapshot, error)
}

type topDetailLoader interface {
	loadDetail(ctx context.Context, req topDetailRequest) (topDetailContent, error)
}

// topRefreshTickMsg is delivered by the periodic ticker; the model reacts
// by issuing a fetch command and scheduling the next tick.
type topRefreshTickMsg struct{}

// topSnapshotMsg carries a fresh snapshot from the loader. The renderer
// reads it on the model's next View call.
type topSnapshotMsg struct {
	snapshot topDataSnapshot
	at       time.Time
	err      error
}

// topModel is the testable Bubble Tea model behind the multi-pane top
// dashboard. The model never touches use cases directly: it owns the
// rendered state (snapshot, focused pane, scroll offsets) and a loader
// command factory so tests can drive Update with synthetic messages.
type topModel struct {
	keys    tui.KeyMap
	actions topPaneActionKeys
	styles  tui.Styles

	loader   topSnapshotLoader
	detail   topDetailLoader
	criteria topDataCriteria
	idle     time.Duration

	width, height int

	pane    topPane
	offsets [topPaneCount]int

	searchOpen  bool
	searchQuery string

	snapshot     topDataSnapshot
	loadedAt     time.Time
	loadErr      error
	loaded       bool
	detailUI     topDetailState
	detailOffset int

	mode topMode

	// now is injected so tests can pin "now" without mutating time.Local.
	now func() time.Time
	// location renders timestamps in a deterministic timezone for tests.
	location *time.Location
	// refreshInterval is the period between automatic snapshot reloads.
	// Tests can set it to zero to disable the ticker entirely; production
	// wiring uses topDashboardRefreshInterval.
	refreshInterval time.Duration

	// loaderCtx is the context the production loader runs under. Tests
	// pass context.Background() through newTopModel; the cobra command
	// passes the request context so cancellation propagates.
	loaderCtx context.Context
}

// topModelConfig groups the inputs required to build a topModel. The
// struct keeps the constructor signature stable as new optional fields
// are added (e.g. injecting a clock for tests) without forcing callers to
// pass nil for parameters they do not care about.
type topModelConfig struct {
	Keys     tui.KeyMap
	Actions  topPaneActionKeys
	Styles   tui.Styles
	Loader   topSnapshotLoader
	Detail   topDetailLoader
	Criteria topDataCriteria
	Idle     time.Duration
	// Now defaults to time.Now when nil.
	Now func() time.Time
	// Location defaults to time.Local when nil.
	Location *time.Location
	// RefreshInterval defaults to topDashboardRefreshInterval; tests can
	// pass a tiny value to keep ticker churn out of the way or zero to
	// disable the periodic reload entirely.
	RefreshInterval time.Duration
	// LoaderCtx defaults to context.Background.
	LoaderCtx context.Context
}

// newTopModel constructs a topModel with sensible defaults applied to any
// zero-value config field.
func newTopModel(cfg topModelConfig) topModel {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	loc := cfg.Location
	if loc == nil {
		loc = time.Local
	}
	// RefreshInterval is taken verbatim: production callers pass
	// topDashboardRefreshInterval explicitly so tests can disable the
	// periodic ticker by passing zero (or a negative value) without
	// fighting a default.
	refresh := cfg.RefreshInterval
	loaderCtx := cfg.LoaderCtx
	if loaderCtx == nil {
		loaderCtx = context.Background()
	}
	return topModel{
		keys:            cfg.Keys,
		actions:         cfg.Actions,
		styles:          cfg.Styles,
		loader:          cfg.Loader,
		detail:          cfg.Detail,
		criteria:        cfg.Criteria,
		idle:            cfg.Idle,
		now:             now,
		location:        loc,
		refreshInterval: refresh,
		loaderCtx:       loaderCtx,
	}
}

// Init runs the first snapshot fetch and schedules the periodic ticker.
// The ticker is suppressed when refreshInterval is zero so tests can stay
// deterministic.
func (m topModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.fetchSnapshotCmd()}
	if m.refreshInterval > 0 {
		cmds = append(cmds, m.tickCmd())
	}
	return tea.Batch(cmds...)
}

// Update is the testable seam: tests drive concrete tea.Msg values and
// inspect the returned model state without going through a Program.
func (m topModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampOffsets()
		m.clampDetailOffset()
		return m, nil
	case topRefreshTickMsg:
		if m.refreshInterval <= 0 {
			return m, m.fetchSnapshotCmd()
		}
		return m, tea.Batch(m.fetchSnapshotCmd(), m.tickCmd())
	case topSnapshotMsg:
		m.snapshot = msg.snapshot
		m.loadedAt = msg.at
		m.loadErr = msg.err
		m.loaded = true
		m.clampOffsets()
		return m, nil
	case topDetailMsg:
		if m.mode != topModeDetail || msg.request != m.detailUI.request {
			return m, nil
		}
		m.detailUI.request = msg.request
		m.detailUI.loading = false
		m.detailUI.err = msg.err
		m.detailUI.title = msg.content.title
		m.detailUI.lines = msg.content.lines
		m.clampDetailOffset()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m topModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == topModeDetail {
		return m.updateDetailKey(msg)
	}
	if m.searchOpen {
		return m.updateSearchKey(msg)
	}
	switch {
	case m.searchQuery != "" && msg.Type == tea.KeyEsc:
		m.clearSearch()
		m.clampOffsets()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		if m.mode == topModeHelp {
			m.mode = topModeBrowse
		} else {
			m.mode = topModeHelp
		}
		return m, nil
	}
	if m.mode == topModeHelp {
		// Help mode swallows everything except Quit / Help so a stray rune
		// does not scroll the underlying pane the operator cannot see.
		return m, nil
	}
	switch {
	case key.Matches(msg, m.actions.NextPane):
		m.switchPane(topPane((int(m.pane) + 1) % topPaneCount))
		return m, nil
	case key.Matches(msg, m.actions.PrevPane):
		m.switchPane(topPane((int(m.pane) + topPaneCount - 1) % topPaneCount))
		return m, nil
	case key.Matches(msg, m.keys.Search):
		m.searchOpen = true
		m.offsets[m.pane] = 0
		return m, nil
	case key.Matches(msg, m.keys.Select):
		return m.openDetail()
	case key.Matches(msg, m.keys.Up):
		m.scrollBy(-1)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.scrollBy(1)
		return m, nil
	case key.Matches(msg, m.keys.PageUp):
		m.scrollBy(-m.paneContentViewportRows(m.pane))
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		m.scrollBy(m.paneContentViewportRows(m.pane))
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.offsets[m.pane] = 0
		return m, nil
	case key.Matches(msg, m.keys.End):
		lines := m.paneLineCount(m.pane)
		viewport := m.paneContentViewportRows(m.pane)
		if lines > viewport {
			m.offsets[m.pane] = lines - viewport
		} else {
			m.offsets[m.pane] = 0
		}
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		return m, m.fetchSnapshotCmd()
	}
	return m, nil
}

func (m topModel) updateSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyEsc:
		m.clearSearch()
		m.clampOffsets()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Select):
		m.searchOpen = false
		m.clampOffsets()
		return m, nil
	case key.Matches(msg, m.actions.NextPane):
		m.switchPane(topPane((int(m.pane) + 1) % topPaneCount))
		return m, nil
	case key.Matches(msg, m.actions.PrevPane):
		m.switchPane(topPane((int(m.pane) + topPaneCount - 1) % topPaneCount))
		return m, nil
	case msg.Type == tea.KeyBackspace || msg.Type == tea.KeyCtrlH:
		runes := []rune(m.searchQuery)
		if len(runes) > 0 {
			m.searchQuery = string(runes[:len(runes)-1])
			m.offsets[m.pane] = 0
		}
		m.clampOffsets()
		return m, nil
	case msg.Type == tea.KeySpace:
		m.searchQuery += " "
		m.offsets[m.pane] = 0
		m.clampOffsets()
		return m, nil
	case msg.Type == tea.KeyRunes:
		m.searchQuery += string(msg.Runes)
		m.offsets[m.pane] = 0
		m.clampOffsets()
		return m, nil
	}
	return m, nil
}

func (m topModel) updateDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC:
		return m, tea.Quit
	case msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && string(msg.Runes) == "q"):
		m.mode = topModeBrowse
		m.detailUI = topDetailState{}
		m.detailOffset = 0
		return m, nil
	case key.Matches(msg, m.keys.Up):
		m.scrollDetailBy(-1)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.scrollDetailBy(1)
		return m, nil
	case key.Matches(msg, m.keys.PageUp):
		m.scrollDetailBy(-m.detailViewportRows())
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		m.scrollDetailBy(m.detailViewportRows())
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.detailOffset = 0
		return m, nil
	case key.Matches(msg, m.keys.End):
		m.detailOffset = max(len(m.detailLines())-m.detailViewportRows(), 0)
		return m, nil
	}
	return m, nil
}

func (m topModel) openDetail() (tea.Model, tea.Cmd) {
	rows := m.paneRows(m.pane, m.paneInteriorWidth())
	if len(rows) == 0 {
		return m, nil
	}
	index := m.offsets[m.pane]
	if index < 0 {
		index = 0
	}
	if index >= len(rows) {
		index = len(rows) - 1
	}
	target := rows[index].target
	if target.kind == topDetailNone {
		return m, nil
	}
	req := topDetailRequest{pane: m.pane, target: target}
	m.mode = topModeDetail
	m.detailOffset = 0
	m.detailUI = topDetailState{
		request: req,
		title:   target.title,
		lines:   []string{Localize("Loading...", "読み込み中...")},
		loading: true,
	}
	return m, m.fetchDetailCmd(req)
}

func (m topModel) fetchDetailCmd(req topDetailRequest) tea.Cmd {
	loader := m.detail
	ctx := m.loaderCtx
	return func() tea.Msg {
		if loader == nil {
			return topDetailMsg{request: req, err: fmt.Errorf("detail loader is not configured")}
		}
		content, err := loader.loadDetail(ctx, req)
		return topDetailMsg{request: req, content: content, err: err}
	}
}

func (m *topModel) switchPane(next topPane) {
	if next == m.pane {
		return
	}
	m.clearSearch()
	m.pane = next
	m.clampOffsets()
}

func (m *topModel) clearSearch() {
	m.searchOpen = false
	m.searchQuery = ""
}

// scrollBy adjusts the focused pane's offset, clamping to the legal range
// so an over-scroll never wraps and so reaching the bottom of a short pane
// stops cleanly instead of leaving a blank window.
func (m *topModel) scrollBy(delta int) {
	offset := m.offsets[m.pane] + delta
	if offset < 0 {
		offset = 0
	}
	lines := m.paneLineCount(m.pane)
	viewport := m.paneContentViewportRows(m.pane)
	maxOffset := 0
	if lines > viewport {
		maxOffset = lines - viewport
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	m.offsets[m.pane] = offset
}

// clampOffsets recomputes scroll positions after data or geometry changes
// so the focused window never points past the end of the available rows.
func (m *topModel) clampOffsets() {
	for i := range m.offsets {
		pane := topPane(i)
		viewport := m.paneContentViewportRows(pane)
		lines := m.paneLineCount(pane)
		maxOffset := 0
		if lines > viewport {
			maxOffset = lines - viewport
		}
		if m.offsets[i] > maxOffset {
			m.offsets[i] = maxOffset
		}
		if m.offsets[i] < 0 {
			m.offsets[i] = 0
		}
	}
}

func (m *topModel) scrollDetailBy(delta int) {
	m.detailOffset += delta
	m.clampDetailOffset()
}

func (m *topModel) clampDetailOffset() {
	maxOffset := max(len(m.detailLines())-m.detailViewportRows(), 0)
	if m.detailOffset > maxOffset {
		m.detailOffset = maxOffset
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
}

// fetchSnapshotCmd runs the loader on the production scheduler and
// surfaces the result as a topSnapshotMsg. The command is also used as
// the manual-refresh handler bound to the shared `r` key.
func (m topModel) fetchSnapshotCmd() tea.Cmd {
	loader := m.loader
	criteria := m.criteria
	ctx := m.loaderCtx
	now := m.now
	return func() tea.Msg {
		if loader == nil {
			return topSnapshotMsg{at: now()}
		}
		snap, err := loader.loadSnapshot(ctx, criteria)
		return topSnapshotMsg{snapshot: snap, at: now(), err: err}
	}
}

func (m topModel) tickCmd() tea.Cmd {
	return tea.Tick(m.refreshInterval, func(time.Time) tea.Msg {
		return topRefreshTickMsg{}
	})
}

// paneLineCount returns the rendered line count of the given pane,
// reusing the same line buffer the View renderer would build. We compute
// this on demand so navigation stays correct even when the snapshot is
// refreshed under the cursor.
func (m topModel) paneLineCount(pane topPane) int {
	return len(m.paneLines(pane, m.paneInteriorWidth()))
}

// paneViewportRows returns how many rows of pane content fit between the
// pane border and the footer. The function deliberately returns 1 even on
// degenerate sizes so navigation does not divide-by-zero or scroll past
// the only visible row.
func (m topModel) paneViewportRows() int {
	rows := m.paneInteriorHeight()
	if rows < 1 {
		return 1
	}
	return rows
}

func (m topModel) paneContentViewportRows(pane topPane) int {
	rows := m.paneViewportRows()
	if m.searchOpen && pane == m.pane {
		rows--
	}
	if rows < 1 {
		return 1
	}
	return rows
}

// paneLines builds the slice of rendered rows for the given pane. Width
// is the interior width (excluding pane border). The slice is recomputed
// on every call rather than cached so the data stays in sync with the
// last snapshot — caching would require invalidation on resize and on
// snapshot apply, which the periodic ticker would race with.
func (m topModel) paneLines(pane topPane, width int) []string {
	rows := m.paneRows(pane, width)
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.line)
	}
	return lines
}

func (m topModel) paneRows(pane topPane, width int) []topPaneRow {
	var rows []topPaneRow
	switch pane {
	case topPaneSessions:
		rows = m.sessionRows(width)
	case topPaneFailures:
		rows = m.eventRows(m.snapshot.Failures, width)
	case topPaneRecentCommands:
		rows = m.eventRows(m.snapshot.RecentCommands, width)
	case topPaneCandidates:
		rows = m.candidateRows(width)
	case topPaneStaleMemories:
		rows = m.staleMemoryRows(width)
	default:
		return nil
	}
	return m.applyPaneSearchFilter(pane, rows)
}

func (m topModel) applyPaneSearchFilter(pane topPane, rows []topPaneRow) []topPaneRow {
	if pane != m.pane || m.searchQuery == "" {
		return rows
	}
	query := strings.ToLower(m.searchQuery)
	filtered := make([]topPaneRow, 0, len(rows))
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.line), query) {
			filtered = append(filtered, row)
		}
	}
	if len(filtered) == 0 {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("No rows match search.", "search に一致する行はありません。"))}}
	}
	return filtered
}

func topPaneRowLines(rows []topPaneRow) []string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.line)
	}
	return lines
}

// sessionLines renders the active session tree to one line per node so
// the dashboard can window into the result without re-walking the tree.
// The renderer mirrors the snapshot text formatter so the dashboard rows
// look identical to `traceary top --snapshot` output, modulo the pane
// width truncation that the Bubble Tea renderer applies on display.
func (m topModel) sessionLines(width int) []string {
	return topPaneRowLines(m.sessionRows(width))
}

func (m topModel) sessionRows(width int) []topPaneRow {
	if m.loadErr != nil {
		return []topPaneRow{{line: m.styles.Error.Render(m.loadErr.Error())}}
	}
	if !m.loaded {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("loading…", "読み込み中…"))}}
	}
	if len(m.snapshot.Sessions) == 0 {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("No active sessions found.", "active session が見つかりません"))}}
	}
	now := m.now()
	out := make([]topPaneRow, 0)
	for _, root := range m.snapshot.Sessions {
		out = appendSessionRows(out, root, "", true, false, m.idle, now, m.location, width)
	}
	return out
}

func appendSessionRows(out []topPaneRow, node *sessionNode, prefix string, isLast bool, hasParent bool, idle time.Duration, now time.Time, loc *time.Location, width int) []topPaneRow {
	connector := "├── "
	if isLast {
		connector = "└── "
	}
	if !hasParent {
		connector = ""
	}
	linePrefix := prefix + connector
	line := formatTopNodeLineIn(node, linePrefix, idle, now, loc)
	summary := node.summary
	out = append(out, topPaneRow{
		line: truncateToWidth(line, width),
		target: topDetailTarget{
			kind:      topDetailSession,
			title:     fmt.Sprintf("SESSION %s", summary.SessionID()),
			sessionID: summary.SessionID(),
		},
	})
	childPrefix := prefix
	if hasParent {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}
	for i, child := range node.children {
		out = appendSessionRows(out, child, childPrefix, i == len(node.children)-1, true, idle, now, loc, width)
	}
	return out
}

// eventLines renders one row per event for the failures and recent-commands
// panes. The format keeps timestamps and kinds aligned and truncates the
// body to fit the pane width so a single noisy event cannot wrap and shove
// the rest of the rows off-screen.
func (m topModel) eventRows(events []*model.Event, width int) []topPaneRow {
	if m.loadErr != nil {
		return []topPaneRow{{line: m.styles.Error.Render(m.loadErr.Error())}}
	}
	if !m.loaded {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("loading…", "読み込み中…"))}}
	}
	if len(events) == 0 {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("No matching records.", "一致する記録はありません"))}}
	}
	out := make([]topPaneRow, 0, len(events))
	for _, ev := range events {
		ts := ev.CreatedAt().In(m.location).Format(eventCompactTimeLayout)
		kind := ev.Kind().String()
		body := truncateMessage(ev.Body())
		line := fmt.Sprintf("%s %s %s", ts, kind, body)
		out = append(out, topPaneRow{
			line: truncateToWidth(line, width),
			target: topDetailTarget{
				kind:    topDetailEvent,
				title:   fmt.Sprintf("EVENT %s", ev.EventID()),
				eventID: ev.EventID(),
			},
		})
	}
	return out
}

// candidateLines renders one row per candidate memory. The pane reuses the
// candidate-list ordering set by the loader (remember-intent priority) so
// inbox and dashboard agree on which row is "next up".
func (m topModel) candidateLines(width int) []string {
	return topPaneRowLines(m.candidateRows(width))
}

func (m topModel) candidateRows(width int) []topPaneRow {
	if m.loadErr != nil {
		return []topPaneRow{{line: m.styles.Error.Render(m.loadErr.Error())}}
	}
	if !m.loaded {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("loading…", "読み込み中…"))}}
	}
	if len(m.snapshot.Candidates) == 0 {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("No candidate durable memories in the inbox.", "inbox に candidate durable memory はありません"))}}
	}
	out := make([]topPaneRow, 0, len(m.snapshot.Candidates))
	for _, candidate := range m.snapshot.Candidates {
		line := fmt.Sprintf("%s %s %s", candidate.MemoryID(), candidate.MemoryType(), truncateMessage(candidate.Fact()))
		out = append(out, topPaneRow{
			line: truncateToWidth(line, width),
			target: topDetailTarget{
				kind:     topDetailMemory,
				title:    fmt.Sprintf("MEMORY %s", candidate.MemoryID()),
				memoryID: candidate.MemoryID(),
			},
		})
	}
	return out
}

// staleMemoryLines renders one row per stale durable memory. The row keeps
// the audit identifiers first (memory id, type, scope, reason) and then the
// human-readable fact so operators can quickly decide whether the stale row
// should be pruned or investigated from the dedicated memory commands.
func (m topModel) staleMemoryLines(width int) []string {
	return topPaneRowLines(m.staleMemoryRows(width))
}

func (m topModel) staleMemoryRows(width int) []topPaneRow {
	if m.loadErr != nil {
		return []topPaneRow{{line: m.styles.Error.Render(m.loadErr.Error())}}
	}
	if !m.loaded {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("loading…", "読み込み中…"))}}
	}
	items := m.snapshot.StaleMemories.Items()
	if len(items) == 0 {
		return []topPaneRow{{line: m.styles.Subtle.Render(Localize("No stale memories.", "stale な memory はありません。"))}}
	}
	out := make([]topPaneRow, 0, len(items))
	for _, row := range items {
		summary := row.Summary()
		line := fmt.Sprintf(
			"%s %s %s %s %s",
			summary.MemoryID(),
			summary.MemoryType(),
			formatMemoryScope(summary.Scope()),
			row.Reason(),
			truncateMessage(summary.Fact()),
		)
		out = append(out, topPaneRow{
			line: truncateToWidth(line, width),
			target: topDetailTarget{
				kind:     topDetailMemory,
				title:    fmt.Sprintf("MEMORY %s", summary.MemoryID()),
				memoryID: summary.MemoryID(),
			},
		})
	}
	return out
}

// truncateToWidth clamps text to width visual columns by walking runes
// and counting East Asian Wide characters as 2 columns. Width <= 0 falls
// back to the original string so a degenerate viewport still shows
// something the operator can read after a resize.
func truncateToWidth(text string, width int) string {
	if width <= 0 {
		return text
	}
	if runewidth.StringWidth(text) <= width {
		return text
	}
	const ellipsis = '…'
	ellipsisWidth := runewidth.RuneWidth(ellipsis)
	budget := width - ellipsisWidth
	if budget < 0 {
		budget = 0
	}
	used := 0
	var b strings.Builder
	for _, r := range text {
		w := runewidth.RuneWidth(r)
		if w == 0 {
			continue
		}
		if used+w > budget {
			break
		}
		b.WriteRune(r)
		used += w
	}
	b.WriteRune(ellipsis)
	return b.String()
}

// View renders the dashboard into a single string. The function is
// pure given (model, terminal size) so tests can assert on the rendered
// output without spinning up a Program.
func (m topModel) View() string {
	if m.mode == topModeHelp {
		return m.renderHelp()
	}
	if m.mode == topModeDetail {
		return m.renderDetail()
	}
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")
	for i := range topPaneCount {
		b.WriteString(m.renderPane(topPane(i)))
		b.WriteString("\n")
	}
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m topModel) renderDetail() string {
	width := m.paneInteriorWidth()
	lines := m.detailLines()
	viewport := m.detailViewportRows()
	start := m.detailOffset
	if start > len(lines) {
		start = len(lines)
	}
	end := start + viewport
	if end > len(lines) {
		end = len(lines)
	}
	visible := make([]string, 0, end-start)
	for _, line := range lines[start:end] {
		visible = append(visible, truncateToWidth(line, width))
	}
	title := m.detailUI.title
	if title == "" {
		title = Localize("detail", "detail")
	}
	scroll := ""
	if len(lines) > viewport {
		scroll = fmt.Sprintf(" %d-%d/%d", start+1, min(start+viewport, len(lines)), len(lines))
	} else if len(lines) > 0 {
		scroll = fmt.Sprintf(" %d", len(lines))
	}
	header := m.styles.Title.Render(fmt.Sprintf("traceary top · detail · %s%s", title, scroll))
	body := strings.Join(visible, "\n")
	if body == "" {
		body = m.styles.Subtle.Render(Localize("(empty)", "(空)"))
	}
	help := m.styles.Help.Render(Localize("↑/↓ scroll · pgup/pgdn page · g/G top/bottom · esc/q close · ctrl+c quit", "↑/↓ スクロール · pgup/pgdn ページ · g/G 先頭/末尾 · esc/q 閉じる · ctrl+c quit"))
	return header + "\n\n" + body + "\n\n" + help
}

func (m topModel) detailLines() []string {
	if m.detailUI.loading {
		return []string{Localize("Loading...", "読み込み中...")}
	}
	if m.detailUI.err != nil {
		return []string{m.styles.Error.Render(m.detailUI.err.Error())}
	}
	if len(m.detailUI.lines) == 0 {
		return []string{Localize("(empty)", "(空)")}
	}
	return m.detailUI.lines
}

func (m topModel) detailViewportRows() int {
	if m.height <= 0 {
		return 12
	}
	const detailChromeRows = 4
	rows := m.height - detailChromeRows
	if rows < 1 {
		return 1
	}
	return rows
}

func (m topModel) renderHeader() string {
	title := m.styles.Title.Render(Localize("traceary top", "traceary top"))
	filterLine := fmt.Sprintf("workspace=%s client=%s agent=%s idle=%s refresh=%s",
		formatFilterValue(m.criteria.Workspace),
		formatFilterValue(m.criteria.Client),
		formatFilterValue(m.criteria.Agent),
		m.idle,
		m.refreshInterval,
	)
	return title + "\n" + m.styles.Subtle.Render(filterLine)
}

func (m topModel) renderFooter() string {
	loaded := "-"
	if m.loaded {
		loaded = m.loadedAt.In(m.location).Format(eventCompactTimeLayout)
	}
	status := fmt.Sprintf("loaded=%s pane=%s", loaded, paneLabel(m.pane))
	if m.searchQuery != "" {
		status += fmt.Sprintf(" search=%q", m.searchQuery)
	}
	if m.searchOpen {
		status += " search-edit"
	}
	if m.loadErr != nil {
		status += " " + m.styles.Error.Render(Localize("load error", "load error"))
	}
	help := Localize(
		"tab/shift+tab pane · enter detail · / search · ↑/↓ scroll · pgup/pgdn page · g/G top/bottom · r refresh · ? help · q quit",
		"tab/shift+tab pane · enter detail · / search · ↑/↓ スクロール · pgup/pgdn ページ · g/G 先頭/末尾 · r 更新 · ? help · q quit",
	)
	return m.styles.Subtle.Render(status) + "\n" + m.styles.Help.Render(help)
}

func (m topModel) renderHelp() string {
	var b strings.Builder
	b.WriteString(m.styles.Title.Render(Localize("traceary top · help", "traceary top · ヘルプ")))
	b.WriteString("\n\n")
	b.WriteString(Localize("Panes:\n", "ペイン:\n"))
	b.WriteString("  1 sessions       " + Localize("active session tree (root → child)", "active session tree (root → child)") + "\n")
	b.WriteString("  2 failures       " + Localize("recent failed command_executed events", "最新の失敗 command_executed イベント") + "\n")
	b.WriteString("  3 commands       " + Localize("recent command_executed events", "最新の command_executed イベント") + "\n")
	b.WriteString("  4 candidates     " + Localize("durable-memory inbox candidates", "durable memory inbox の候補") + "\n")
	b.WriteString("  5 stale memories " + Localize("stale durable memories needing cleanup", "整理対象の stale durable memory") + "\n")
	b.WriteString("\n")
	b.WriteString(Localize("Navigation:\n", "操作:\n"))
	b.WriteString("  tab / shift+tab  " + Localize("focus next / previous pane", "次 / 前のペインへフォーカス") + "\n")
	b.WriteString("  ↑ / ↓ (k / j)    " + Localize("scroll the focused pane by one row", "フォーカス中のペインを1行スクロール") + "\n")
	b.WriteString("  pgup / pgdn      " + Localize("page through the focused pane", "フォーカス中のペインをページ移動") + "\n")
	b.WriteString("  g / G            " + Localize("jump to top / bottom of the pane", "ペインの先頭 / 末尾へ") + "\n")
	b.WriteString("  enter            " + Localize("open detail for the highlighted row", "highlight 中の行の detail を開く") + "\n")
	b.WriteString("  /                " + Localize("open / edit pane search filter", "ペイン内 search filter を開く / 編集") + "\n")
	b.WriteString("  enter (search)   " + Localize("keep the current search filter and return to pane navigation", "現在の search filter を保持してペイン操作へ戻る") + "\n")
	b.WriteString("  esc              " + Localize("clear the active search filter while editing search", "search 編集中の filter をクリア") + "\n")
	b.WriteString("  r                " + Localize("force a snapshot refresh", "スナップショットを再取得") + "\n")
	b.WriteString("  ?                " + Localize("toggle this help", "ヘルプの表示を切替") + "\n")
	b.WriteString("  q / ctrl+c       " + Localize("quit", "終了") + "\n")
	b.WriteString("\n")
	b.WriteString(m.styles.Help.Render(Localize("? close help · q quit", "? ヘルプを閉じる · q quit")))
	return b.String()
}

// renderPane renders a single pane: header, visible rows, and a scroll
// indicator. The focused pane is wrapped with the active style so the
// operator can tell at a glance which pane keys are bound to.
func (m topModel) renderPane(pane topPane) string {
	width := m.paneInteriorWidth()
	rows := m.paneRows(pane, width)
	viewport := m.paneContentViewportRows(pane)
	if m.offsets[pane] > 0 && m.offsets[pane] >= len(rows) {
		// Snapshot shrunk under the cursor; rewind to the last visible row
		// so the pane never renders an empty window after a refresh.
		m.offsets[pane] = max(len(rows)-viewport, 0)
	}
	start := m.offsets[pane]
	end := start + viewport
	if end > len(rows) {
		end = len(rows)
	}
	visible := rows[start:end]
	header := m.renderPaneHeader(pane, len(rows))
	bodyLines := make([]string, 0, len(visible)+1)
	if m.searchOpen && pane == m.pane {
		bodyLines = append(bodyLines, m.renderSearchPrompt(width))
	}
	for i, row := range visible {
		bodyLines = append(bodyLines, m.renderPaneRow(pane, start+i, row, width))
	}
	body := strings.Join(bodyLines, "\n")
	if body == "" {
		body = m.styles.Subtle.Render(Localize("(empty)", "(空)"))
	}
	return header + "\n" + body
}

func (m topModel) renderPaneRow(pane topPane, index int, row topPaneRow, width int) string {
	if pane == m.pane && index == m.offsets[pane] && row.target.kind != topDetailNone {
		return m.styles.Active.Render(truncateToWidth("› "+row.line, width))
	}
	return row.line
}

func (m topModel) renderSearchPrompt(width int) string {
	prompt := fmt.Sprintf("search: /%s", m.searchQuery)
	return m.styles.Help.Render(truncateToWidth(prompt, width))
}

func (m topModel) renderPaneHeader(pane topPane, total int) string {
	label := paneLabel(pane)
	if pane == topPaneStaleMemories {
		label = fmt.Sprintf("STALE MEMORIES (count=%d)", m.snapshot.StaleMemories.Count())
	}
	scroll := ""
	viewport := m.paneContentViewportRows(pane)
	if total > viewport {
		scroll = fmt.Sprintf(" %d-%d/%d", m.offsets[pane]+1, min(m.offsets[pane]+viewport, total), total)
	} else if total > 0 {
		scroll = fmt.Sprintf(" %d", total)
	}
	prefix := fmt.Sprintf("[%d] %s%s", int(pane)+1, label, scroll)
	if pane == m.pane {
		return m.styles.Active.Render("▶ " + prefix)
	}
	return m.styles.Subtle.Render("  " + prefix)
}

// paneInteriorWidth returns the column budget available to a single pane
// row. The dashboard uses a vertical stack so each pane gets the full
// width minus a small margin for the focus marker.
func (m topModel) paneInteriorWidth() int {
	width := m.width - 2
	if width < 20 {
		width = 20
	}
	return width
}

// paneInteriorHeight returns the rows allocated to one pane's body. The
// dashboard distributes the available terminal height between the configured
// panes after subtracting the title (2 rows), per-pane header (1 row each),
// and footer (2 rows). The minimum is clamped to 1 so navigation stays
// well-defined on a tiny terminal.
func (m topModel) paneInteriorHeight() int {
	if m.height <= 0 {
		return 5
	}
	const titleRows = 2
	const footerRows = 2
	const paneHeaderRows = 1
	chrome := titleRows + footerRows + topPaneCount*paneHeaderRows
	body := m.height - chrome
	if body < topPaneCount {
		return 1
	}
	return body / topPaneCount
}

func paneLabel(pane topPane) string {
	switch pane {
	case topPaneSessions:
		return Localize("sessions", "sessions")
	case topPaneFailures:
		return Localize("failures", "failures")
	case topPaneRecentCommands:
		return Localize("recent commands", "recent commands")
	case topPaneCandidates:
		return Localize("candidates", "candidates")
	case topPaneStaleMemories:
		return Localize("stale memories", "stale memories")
	}
	return ""
}
