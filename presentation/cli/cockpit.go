package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

const cockpitExitCodeNotInteractive = 2
const cockpitLiveMaxEvents = 200

type cockpitExitError struct {
	message  string
	exitCode int
}

func (e cockpitExitError) Error() string { return e.message }
func (e cockpitExitError) ExitCode() int { return e.exitCode }

type cockpitCommandOptions struct {
	dbPath string
}

func (c *RootCLI) newCockpitCommand() *cobra.Command {
	opts := cockpitCommandOptions{}
	cmd := &cobra.Command{
		Use:     "tui",
		Aliases: []string{"dashboard"},
		Short:   Localize("Open the Traceary operator cockpit TUI", "Traceary operator cockpit TUI を開く"),
		Long: Localize(
			"Open the Traceary operator cockpit TUI. The cockpit is the explicit interactive entrypoint that will gather top, tail, doctor, handoff, and memory review workflows behind one TTY-only shell. Bare `traceary` behavior is unchanged.",
			"Traceary operator cockpit TUI を開きます。cockpit は top / tail / doctor / handoff / memory review を 1 つの TTY 専用 shell にまとめる明示的な対話 entrypoint です。bare `traceary` の挙動は変更しません。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runCockpit(cmd.Context(), cmd.OutOrStdout(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.dbPath, "db-path", "", dbPathFlagUsage())
	return cmd
}

func (c *RootCLI) runCockpit(ctx context.Context, output io.Writer, opts cockpitCommandOptions) error {
	stdin, stdout := cockpitIO(output)
	if !tui.Interactive(stdin, stdout) {
		return newCockpitNonInteractiveError(output)
	}
	home, err := c.loadCockpitHome(ctx, opts)
	if err != nil {
		return err
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), home)
	model.loader = cockpitRuntimeLoader{root: c, opts: opts}
	model.loaderCtx = ctx
	if err := tui.Run(model, tui.RunOptions{Input: stdin, Output: stdout, AltScreen: true}); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run cockpit TUI", "cockpit TUI の実行に失敗しました"), err)
	}
	return nil
}

type cockpitHomeSnapshot struct {
	LoadedAt time.Time
	DBPath   string

	DoctorPassCount int
	DoctorWarnCount int
	DoctorFailCount int
	DoctorError     string

	HookWarnCount int
	HookFailCount int

	StaleActiveSessionCount int
	AcceptedMemoryCount     int
	CandidateMemoryCount    int
	NewCandidateMemoryCount int
	NewCandidateMemoryKnown bool
	RememberIntentCount     int
	LowQualityMemoryCount   int
	MemoryScanLimited       bool
	MemoryLastSeenAt        time.Time
	StaleMemoryCount        int
	RecentFailureCount      int
	RecentCommandCount      int
	LargePayloadCount       int
}

func (c *RootCLI) loadCockpitHome(ctx context.Context, opts cockpitCommandOptions) (cockpitHomeSnapshot, error) {
	loadedAt := topNowFunc()
	home := cockpitHomeSnapshot{LoadedAt: loadedAt}

	report, reportErr := c.loadCockpitDoctorReport(ctx, opts)
	if reportErr != nil {
		home.DoctorError = reportErr.Error()
	} else if report != nil {
		home.DBPath = report.DBPath
		home.DoctorPassCount = report.Summary.Pass
		home.DoctorWarnCount = report.Summary.Warn
		home.DoctorFailCount = report.Summary.Fail
		home.HookWarnCount, home.HookFailCount = countCockpitHookIssues(report)
	}
	if home.DBPath == "" {
		resolvedDBPath, err := resolveDBPath(opts.dbPath)
		if err != nil {
			home.DoctorError = err.Error()
		} else {
			c.applyDatabasePath(resolvedDBPath)
			home.DBPath = resolvedDBPath
			if c.storeManagement != nil {
				if err := c.storeManagement.Initialize(ctx); err != nil {
					home.DoctorError = err.Error()
				}
			}
		}
	}

	memoryLastSeenAt, memoryLastSeenKnown, err := c.loadCockpitMemoryLastSeenAt(ctx)
	if err != nil {
		home.NewCandidateMemoryKnown = false
	} else if memoryLastSeenKnown {
		home.MemoryLastSeenAt = memoryLastSeenAt
		home.NewCandidateMemoryKnown = true
	}
	criteria := topDataCriteria{
		SessionLimit:       defaultTopLimit,
		FailureLimit:       topPaneFailureLimit,
		RecentCommandLimit: topPaneRecentCommandLimit,
		CandidateLimit:     topPaneCandidateLimit,
		StaleMemoryLimit:   topPaneStaleMemoryLimit,
		StaleAfter:         defaultActiveSessionStaleAfter,
		Now:                loadedAt,
	}
	if home.NewCandidateMemoryKnown {
		criteria.MemoryLastSeenAt = domtypes.Some(home.MemoryLastSeenAt)
	}
	snap, err := c.newTopDataLoader().loadSnapshot(ctx, criteria)
	if err != nil {
		return cockpitHomeSnapshot{}, err
	}
	home.StaleActiveSessionCount = snap.Reliability.StaleActiveSessionCount
	home.AcceptedMemoryCount = snap.Reliability.AcceptedMemoryCount
	home.CandidateMemoryCount = snap.Reliability.CandidateMemoryCount
	home.NewCandidateMemoryCount = snap.Reliability.NewCandidateCount
	home.NewCandidateMemoryKnown = snap.Reliability.NewCandidateKnown
	home.RememberIntentCount = snap.Reliability.RememberIntentCount
	home.LowQualityMemoryCount = snap.Reliability.LowQualityCount
	home.MemoryScanLimited = snap.Reliability.MemoryScanLimited
	home.StaleMemoryCount = snap.StaleMemories.Count()
	home.RecentFailureCount = len(snap.Failures)
	home.RecentCommandCount = len(snap.RecentCommands)
	home.LargePayloadCount = snap.Reliability.LargePayloads.Count

	return home, nil
}

// CockpitStateReader provides optional local cockpit state. Missing or failing
// state must not block read-only cockpit views; callers should treat it as a
// notification enhancement rather than critical data.
type CockpitStateReader interface {
	MemoryLastSeenAt(ctx context.Context) (time.Time, bool, error)
}

func (c *RootCLI) loadCockpitMemoryLastSeenAt(ctx context.Context) (time.Time, bool, error) {
	if c.cockpitState == nil {
		return time.Time{}, false, nil
	}
	lastSeenAt, ok, err := c.cockpitState.MemoryLastSeenAt(ctx)
	if err != nil {
		return time.Time{}, false, xerrors.Errorf("%s: %w", Localize("failed to load cockpit memory last-seen state", "cockpit memory last-seen state の読み込みに失敗しました"), err)
	}
	return lastSeenAt, ok, nil
}

func (c *RootCLI) loadCockpitDoctorReport(ctx context.Context, opts cockpitCommandOptions) (*doctorReport, error) {
	if c.storeManagement == nil {
		return nil, xerrors.Errorf(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if c.hooksOrchestrator == nil {
		return nil, xerrors.Errorf(Localize("hooks orchestrator is not configured", "hooks orchestrator が設定されていません"))
	}
	return c.buildDoctorReport(ctx, doctorCommandInput{
		dbPath:         opts.dbPath,
		currentVersion: "",
	})
}

func countCockpitHookIssues(report *doctorReport) (warn int, fail int) {
	if report == nil {
		return 0, 0
	}
	for _, check := range report.Checks {
		if check.Section != "Hooks" && check.Section != "MCP" {
			continue
		}
		switch check.Severity {
		case doctorSeverityWarn:
			warn++
		case doctorSeverityFail:
			fail++
		}
	}
	return warn, fail
}

type cockpitLoader interface {
	loadCockpitHome(ctx context.Context) (cockpitHomeSnapshot, error)
	loadCockpitLive(ctx context.Context, cursor tailCursor, initial bool) (cockpitLiveSnapshot, error)
	loadCockpitEventDetail(ctx context.Context, eventID domtypes.EventID) (topDetailContent, error)
	loadCockpitMemoryReviewItems(ctx context.Context) ([]apptypes.MemoryDetails, error)
	finishCockpitMemoryReview(ctx context.Context, final reviewModel, items []apptypes.MemoryDetails) (memoryInboxReviewResult, error)
}

type cockpitLiveSnapshot struct {
	Events   []*model.Event
	Cursor   tailCursor
	LoadedAt time.Time
}

type cockpitRuntimeLoader struct {
	root *RootCLI
	opts cockpitCommandOptions
}

func (l cockpitRuntimeLoader) loadCockpitHome(ctx context.Context) (cockpitHomeSnapshot, error) {
	return l.root.loadCockpitHome(ctx, l.opts)
}

func (l cockpitRuntimeLoader) loadCockpitLive(ctx context.Context, cursor tailCursor, initial bool) (cockpitLiveSnapshot, error) {
	return l.root.loadCockpitLive(ctx, cursor, initial)
}

func (l cockpitRuntimeLoader) loadCockpitEventDetail(ctx context.Context, eventID domtypes.EventID) (topDetailContent, error) {
	return l.root.loadCockpitEventDetail(ctx, eventID)
}

func (l cockpitRuntimeLoader) loadCockpitMemoryReviewItems(ctx context.Context) ([]apptypes.MemoryDetails, error) {
	if l.root.memory == nil {
		return nil, xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	if l.root.storeManagement != nil {
		if err := l.root.initializeStore(ctx, l.opts.dbPath); err != nil {
			return nil, err
		}
	}
	return l.root.loadInboxReviewItems(ctx, memoryInboxReviewCommandInput{
		dbPath: l.opts.dbPath,
		limit:  defaultMemoryInboxLimit,
	})
}

func (l cockpitRuntimeLoader) finishCockpitMemoryReview(ctx context.Context, final reviewModel, items []apptypes.MemoryDetails) (memoryInboxReviewResult, error) {
	if l.root.memory == nil {
		return memoryInboxReviewResult{}, xerrors.Errorf(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	result, err := applyInboxReviewDecisions(ctx, l.root.memory, final.Decisions(), items)
	if err != nil {
		return result, err
	}
	if len(result.Failures) > 0 {
		return result, memoryInboxReviewFailureError(result)
	}
	return result, nil
}

func (c *RootCLI) loadCockpitLive(ctx context.Context, cursor tailCursor, initial bool) (cockpitLiveSnapshot, error) {
	if c.event == nil {
		return cockpitLiveSnapshot{}, xerrors.Errorf(Localize("list events query service is not configured", "イベント一覧クエリサービスが設定されていません"))
	}
	now := topNowFunc().UTC()
	if initial || cursor.timestamp.IsZero() {
		events, err := c.event.List(ctx, apptypes.NewEventListCriteriaBuilder(defaultTailInitialLimit).Build())
		if err != nil {
			return cockpitLiveSnapshot{}, xerrors.Errorf("%s: %w", Localize("failed to list live events", "live event の一覧取得に失敗しました"), err)
		}
		slices.Reverse(events)
		if len(events) > 0 {
			cursor = newTailCursor(events[len(events)-1].CreatedAt())
			cursor.Advance(events)
		} else {
			cursor = newTailCursor(now)
		}
		return cockpitLiveSnapshot{Events: events, Cursor: cursor, LoadedAt: now}, nil
	}
	base := apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).Build()
	events, err := c.pollTailEvents(ctx, base, cursor, now)
	if err != nil {
		return cockpitLiveSnapshot{}, err
	}
	cursor.Advance(events)
	return cockpitLiveSnapshot{Events: events, Cursor: cursor, LoadedAt: now}, nil
}

func (c *RootCLI) loadCockpitEventDetail(ctx context.Context, eventID domtypes.EventID) (topDetailContent, error) {
	return c.newTopDataLoader().loadDetail(ctx, topDetailRequest{
		target: topDetailTarget{
			kind:    topDetailEvent,
			title:   fmt.Sprintf("EVENT %s", eventID.String()),
			eventID: eventID,
		},
	})
}

type cockpitWarning struct {
	severity string
	label    string
	hint     string
}

func (s cockpitHomeSnapshot) warnings() []cockpitWarning {
	warnings := make([]cockpitWarning, 0, 6)
	if s.DoctorError != "" {
		warnings = append(warnings, cockpitWarning{severity: "FAIL", label: "doctor unavailable", hint: s.DoctorError})
	}
	if s.DoctorFailCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "FAIL", label: fmt.Sprintf("doctor failures=%d", s.DoctorFailCount), hint: "run `traceary doctor` for remediation details"})
	}
	if s.HookFailCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "FAIL", label: fmt.Sprintf("hook/MCP failures=%d", s.HookFailCount), hint: "run `traceary doctor --json` and inspect Hooks/MCP sections"})
	}
	if s.StaleActiveSessionCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("stale active sessions=%d", s.StaleActiveSessionCount), hint: "run `traceary session gc --stale-after 24h --dry-run`"})
	}
	if s.DoctorWarnCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("doctor warnings=%d", s.DoctorWarnCount), hint: "run `traceary doctor`"})
	}
	if s.HookWarnCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("hook/MCP warnings=%d", s.HookWarnCount), hint: "run `traceary doctor --json` and inspect Hooks/MCP sections"})
	}
	if s.NewCandidateMemoryKnown && s.NewCandidateMemoryCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("new candidate memories=%d", s.NewCandidateMemoryCount), hint: "press memory review when available or run `traceary memory inbox review`"})
	}
	if s.RememberIntentCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("remember-intent candidates=%d", s.RememberIntentCount), hint: "prioritize with `traceary memory inbox review --remember-intent`"})
	}
	if s.LowQualityMemoryCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("low-quality candidates=%d", s.LowQualityMemoryCount), hint: "inspect with `traceary memory inbox cleanup --include-hidden`"})
	}
	if s.CandidateMemoryCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("candidate memories=%d", s.CandidateMemoryCount), hint: "review with `traceary memory inbox review`"})
	}
	if s.RecentFailureCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("recent failures=%d", s.RecentFailureCount), hint: "open `traceary top` or `traceary tail` for details"})
	}
	if s.LargePayloadCount > 0 {
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("large payloads=%d", s.LargePayloadCount), hint: "inspect full events with `traceary show <event_id>`"})
	}
	return warnings
}

func newCockpitNonInteractiveError(output io.Writer) error {
	guidance := Localize(
		"Traceary cockpit requires an interactive terminal (TTY).\nUse the existing non-interactive commands instead:\n  traceary top --snapshot [--json]\n  traceary tail [--follow]\n  traceary doctor --json\n  traceary session handoff\n  traceary memory inbox list\nRun `traceary tui` from a terminal to open the cockpit.",
		"Traceary cockpit には対話 terminal (TTY) が必要です。\n非対話 shell では既存 command を使ってください:\n  traceary top --snapshot [--json]\n  traceary tail [--follow]\n  traceary doctor --json\n  traceary session handoff\n  traceary memory inbox list\nterminal から `traceary tui` を実行すると cockpit を開けます。",
	)
	if output != nil {
		_, _ = fmt.Fprintln(output, guidance)
	}
	return cockpitExitError{message: guidance, exitCode: cockpitExitCodeNotInteractive}
}

type cockpitModel struct {
	keys   tui.KeyMap
	styles tui.Styles

	loader    cockpitLoader
	loaderCtx context.Context

	showHelp  bool
	mode      cockpitMode
	home      cockpitHomeSnapshot
	statusMsg string
	statusErr string

	live                   cockpitLiveState
	detail                 topDetailState
	detailOffset           int
	homeRequestSeq         uint64
	memoryReview           cockpitMemoryReviewState
	memoryReviewRequestSeq uint64
}

func newCockpitModel(keys tui.KeyMap, styles tui.Styles, home cockpitHomeSnapshot) cockpitModel {
	return cockpitModel{keys: keys, styles: styles, home: home}
}

func (m cockpitModel) Init() tea.Cmd { return nil }

type cockpitMode int

const (
	cockpitModeHome cockpitMode = iota
	cockpitModeLive
	cockpitModeDetail
	cockpitModeMemoryReview
)

type cockpitLiveState struct {
	events     []*model.Event
	cursor     tailCursor
	loadedAt   time.Time
	loading    bool
	follow     bool
	selected   int
	requestSeq uint64
	err        error
}

type cockpitLiveMsg struct {
	snapshot cockpitLiveSnapshot
	initial  bool
	seq      uint64
	err      error
}

type cockpitLiveTickMsg struct{}

type cockpitHomeMsg struct {
	home cockpitHomeSnapshot
	seq  uint64
	err  error
}

type cockpitDetailLoadedMsg struct {
	request topDetailRequest
	content topDetailContent
	err     error
}

type cockpitMemoryReviewState struct {
	items      []apptypes.MemoryDetails
	review     reviewModel
	loading    bool
	applying   bool
	requestSeq uint64
	err        error
	result     memoryInboxReviewResult
}

type cockpitMemoryReviewLoadedMsg struct {
	items []apptypes.MemoryDetails
	seq   uint64
	err   error
}

type cockpitMemoryReviewAppliedMsg struct {
	result memoryInboxReviewResult
	err    error
}

func (m cockpitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case cockpitHomeMsg:
		if msg.seq != m.homeRequestSeq {
			return m, nil
		}
		if msg.err != nil {
			m.statusMsg = ""
			m.statusErr = msg.err.Error()
			return m, nil
		}
		m.home = msg.home
		m.statusErr = ""
		return m, nil
	case cockpitLiveMsg:
		if msg.seq != m.live.requestSeq {
			return m, nil
		}
		m.live.loading = false
		m.live.err = msg.err
		if msg.err == nil {
			if msg.initial {
				m.live.events = msg.snapshot.Events
			} else {
				m.live.events = append(m.live.events, msg.snapshot.Events...)
				if len(m.live.events) > cockpitLiveMaxEvents {
					m.live.events = m.live.events[len(m.live.events)-cockpitLiveMaxEvents:]
				}
			}
			m.live.cursor = msg.snapshot.Cursor
			m.live.loadedAt = msg.snapshot.LoadedAt
			m.clampLiveSelection()
		}
		if m.mode == cockpitModeLive && m.live.follow {
			return m, m.cockpitLiveTickCmd()
		}
		return m, nil
	case cockpitLiveTickMsg:
		if m.mode == cockpitModeLive && m.live.follow && !m.live.loading {
			return m, m.startCockpitLiveLoad(false)
		}
		return m, nil
	case cockpitDetailLoadedMsg:
		if m.mode != cockpitModeDetail || msg.request != m.detail.request {
			return m, nil
		}
		m.detail.loading = false
		m.detail.err = msg.err
		m.detail.title = msg.content.title
		m.detail.lines = msg.content.lines
		m.clampDetailOffset()
		return m, nil
	case cockpitMemoryReviewLoadedMsg:
		if m.mode != cockpitModeMemoryReview || msg.seq != m.memoryReview.requestSeq {
			return m, nil
		}
		m.memoryReview.loading = false
		m.memoryReview.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.memoryReview.items = msg.items
		m.memoryReview.review = newReviewModel(msg.items, m.keys, m.styles)
		return m, nil
	case cockpitMemoryReviewAppliedMsg:
		m.memoryReview.applying = false
		m.memoryReview.result = msg.result
		m.memoryReview.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.statusMsg = formatCockpitMemoryReviewResult(msg.result)
		m.statusErr = ""
		m.mode = cockpitModeHome
		return m, m.startCockpitHomeLoad()
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m cockpitModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == cockpitModeMemoryReview {
		return m.updateMemoryReviewKey(msg)
	}
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	}
	switch m.mode {
	case cockpitModeDetail:
		return m.updateDetailKey(msg)
	case cockpitModeLive:
		return m.updateLiveKey(msg)
	default:
		return m.updateHomeKey(msg)
	}
}

func (m cockpitModel) updateHomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyRunes {
		switch strings.ToLower(string(msg.Runes)) {
		case "t", "l":
			m.mode = cockpitModeLive
			return m, m.startCockpitLiveLoad(true)
		case "m":
			m.mode = cockpitModeMemoryReview
			return m, m.startCockpitMemoryReviewLoad()
		}
	}
	return m, nil
}

func (m cockpitModel) updateLiveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.live.selected--
		m.clampLiveSelection()
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.live.selected++
		m.clampLiveSelection()
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		return m, m.startCockpitLiveLoad(true)
	case key.Matches(msg, m.keys.Select):
		return m.openCockpitLiveDetail()
	}
	if msg.Type == tea.KeyRunes {
		switch strings.ToLower(string(msg.Runes)) {
		case "h":
			m.mode = cockpitModeHome
			return m, nil
		case "f":
			m.live.follow = !m.live.follow
			if m.live.follow {
				return m, m.cockpitLiveTickCmd()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m cockpitModel) updateDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.detailOffset--
		m.clampDetailOffset()
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.detailOffset++
		m.clampDetailOffset()
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		switch strings.ToLower(string(msg.Runes)) {
		case "h":
			m.mode = cockpitModeHome
			m.detail = topDetailState{}
			m.detailOffset = 0
			return m, nil
		case "t", "l":
			m.mode = cockpitModeLive
			m.detail = topDetailState{}
			m.detailOffset = 0
			if m.live.follow {
				return m, m.cockpitLiveTickCmd()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m cockpitModel) updateMemoryReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	actions := defaultReviewActionKeys()
	if msg.Type == tea.KeyRunes && strings.ToLower(string(msg.Runes)) == "h" && (m.memoryReview.loading || m.memoryReview.err != nil) {
		m.mode = cockpitModeHome
		if m.memoryReview.err != nil {
			m.memoryReview = cockpitMemoryReviewState{}
			return m, m.startCockpitHomeLoad()
		}
		return m, nil
	}
	if m.memoryReview.applying {
		return m, nil
	}
	if m.memoryReview.err != nil {
		if key.Matches(msg, actions.Cancel) {
			m.mode = cockpitModeHome
			m.memoryReview = cockpitMemoryReviewState{}
			return m, m.startCockpitHomeLoad()
		}
		if isCockpitMemoryReviewFinishKey(msg) || msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		return m, nil
	}
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if m.memoryReview.loading {
		if key.Matches(msg, actions.Cancel) {
			m.mode = cockpitModeHome
			m.memoryReview = cockpitMemoryReviewState{}
			return m, nil
		}
		if isCockpitMemoryReviewFinishKey(msg) || msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		return m, nil
	}

	if key.Matches(msg, actions.Cancel) {
		switch m.memoryReview.review.mode {
		case reviewModeHelp, reviewModeViewEvidence, reviewModeEdit:
			updated, _ := m.memoryReview.review.Update(msg)
			m.memoryReview.review = updated.(reviewModel)
			return m, nil
		default:
			m.mode = cockpitModeHome
			m.memoryReview = cockpitMemoryReviewState{}
			return m, m.startCockpitHomeLoad()
		}
	}
	if m.memoryReview.review.mode != reviewModeEdit && isCockpitMemoryReviewFinishKey(msg) {
		m.memoryReview.applying = true
		return m, m.applyCockpitMemoryReviewCmd()
	}

	updated, cmd := m.memoryReview.review.Update(msg)
	m.memoryReview.review = updated.(reviewModel)
	// reviewModel returns tea.Quit for standalone `memory inbox review`; the
	// cockpit owns the outer program, so finishing review is handled above and
	// no delegated command should escape into the cockpit runtime.
	if cmd != nil {
		return m, nil
	}
	return m, nil
}

func isCockpitMemoryReviewFinishKey(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyRunes && strings.ToLower(string(msg.Runes)) == "q"
}

func (m *cockpitModel) startCockpitHomeLoad() tea.Cmd {
	m.homeRequestSeq++
	return m.fetchCockpitHomeCmd(m.homeRequestSeq)
}

func (m cockpitModel) fetchCockpitHomeCmd(seq uint64) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	return func() tea.Msg {
		if loader == nil {
			return cockpitHomeMsg{seq: seq, err: xerrors.Errorf("cockpit home loader is not configured")}
		}
		home, err := loader.loadCockpitHome(ctx)
		return cockpitHomeMsg{home: home, seq: seq, err: err}
	}
}

func (m cockpitModel) fetchCockpitMemoryReviewCmd() tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	seq := m.memoryReview.requestSeq
	return func() tea.Msg {
		if loader == nil {
			return cockpitMemoryReviewLoadedMsg{seq: seq, err: xerrors.Errorf("cockpit memory review loader is not configured")}
		}
		items, err := loader.loadCockpitMemoryReviewItems(ctx)
		return cockpitMemoryReviewLoadedMsg{items: items, seq: seq, err: err}
	}
}

func (m *cockpitModel) startCockpitMemoryReviewLoad() tea.Cmd {
	m.memoryReviewRequestSeq++
	m.memoryReview = cockpitMemoryReviewState{
		loading:    true,
		requestSeq: m.memoryReviewRequestSeq,
	}
	return m.fetchCockpitMemoryReviewCmd()
}

func (m cockpitModel) applyCockpitMemoryReviewCmd() tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	final := m.memoryReview.review
	items := append([]apptypes.MemoryDetails(nil), m.memoryReview.items...)
	return func() tea.Msg {
		if loader == nil {
			return cockpitMemoryReviewAppliedMsg{err: xerrors.Errorf("cockpit memory review loader is not configured")}
		}
		result, err := loader.finishCockpitMemoryReview(ctx, final, items)
		return cockpitMemoryReviewAppliedMsg{result: result, err: err}
	}
}

func (m *cockpitModel) startCockpitLiveLoad(initial bool) tea.Cmd {
	m.live.loading = true
	m.live.requestSeq++
	return m.fetchCockpitLiveCmd(initial, m.live.requestSeq)
}

func (m cockpitModel) fetchCockpitLiveCmd(initial bool, seq uint64) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	cursor := m.live.cursor
	return func() tea.Msg {
		if loader == nil {
			return cockpitLiveMsg{initial: initial, seq: seq, err: xerrors.Errorf("cockpit live loader is not configured")}
		}
		snapshot, err := loader.loadCockpitLive(ctx, cursor, initial)
		return cockpitLiveMsg{snapshot: snapshot, initial: initial, seq: seq, err: err}
	}
}

func (m cockpitModel) cockpitLiveTickCmd() tea.Cmd {
	return tea.Tick(defaultTailPollInterval, func(time.Time) tea.Msg {
		return cockpitLiveTickMsg{}
	})
}

func (m cockpitModel) openCockpitLiveDetail() (tea.Model, tea.Cmd) {
	if len(m.live.events) == 0 || m.live.selected < 0 || m.live.selected >= len(m.live.events) {
		return m, nil
	}
	event := m.live.events[m.live.selected]
	if event == nil {
		return m, nil
	}
	req := topDetailRequest{target: topDetailTarget{kind: topDetailEvent, title: fmt.Sprintf("EVENT %s", event.EventID()), eventID: event.EventID()}}
	m.mode = cockpitModeDetail
	m.detailOffset = 0
	m.detail = topDetailState{request: req, title: req.target.title, lines: []string{Localize("Loading...", "読み込み中...")}, loading: true}
	return m, m.fetchCockpitDetailCmd(req)
}

func (m cockpitModel) fetchCockpitDetailCmd(req topDetailRequest) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	return func() tea.Msg {
		if loader == nil {
			return cockpitDetailLoadedMsg{request: req, err: xerrors.Errorf("cockpit detail loader is not configured")}
		}
		content, err := loader.loadCockpitEventDetail(ctx, req.target.eventID)
		return cockpitDetailLoadedMsg{request: req, content: content, err: err}
	}
}

func (m *cockpitModel) clampLiveSelection() {
	if len(m.live.events) == 0 {
		m.live.selected = 0
		return
	}
	if m.live.selected < 0 {
		m.live.selected = 0
	}
	if m.live.selected >= len(m.live.events) {
		m.live.selected = len(m.live.events) - 1
	}
}

func (m *cockpitModel) clampDetailOffset() {
	maxOffset := len(m.detailLines()) - 1
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.detailOffset < 0 {
		m.detailOffset = 0
	}
	if m.detailOffset > maxOffset {
		m.detailOffset = maxOffset
	}
}

func (m cockpitModel) View() string {
	switch m.mode {
	case cockpitModeLive:
		return m.liveView()
	case cockpitModeDetail:
		return m.detailView()
	case cockpitModeMemoryReview:
		return m.memoryReviewView()
	}
	return m.homeView()
}

func (m cockpitModel) homeView() string {
	memoryScanSuffix := ""
	if m.home.MemoryScanLimited {
		memoryScanSuffix = " scan_limited=true"
	}
	lines := []string{
		m.styles.Title.Render("Traceary cockpit"),
		"",
		m.styles.Subtle.Render(fmt.Sprintf("loaded=%s db=%s", formatJSONTime(m.home.LoadedAt), formatOptionalColumn(m.home.DBPath))),
		"",
	}
	if m.statusMsg != "" {
		lines = append(lines, m.styles.Success.Render("• "+m.statusMsg), "")
	}
	if m.statusErr != "" {
		lines = append(lines, m.styles.Error.Render("• "+m.statusErr), "")
	}
	lines = append(lines, m.styles.Subtle.Render("ATTENTION"))
	warnings := m.home.warnings()
	if len(warnings) == 0 {
		lines = append(lines, m.styles.Success.Render("• No immediate cockpit warnings."))
	} else {
		for _, warning := range warnings {
			style := m.styles.Warning
			if warning.severity == "FAIL" {
				style = m.styles.Error
			}
			lines = append(lines, style.Render(fmt.Sprintf("• [%s] %s — %s", warning.severity, warning.label, warning.hint)))
		}
	}
	lines = append(lines,
		"",
		m.styles.Subtle.Render("OVERVIEW"),
		fmt.Sprintf("• doctor: pass=%d warn=%d fail=%d", m.home.DoctorPassCount, m.home.DoctorWarnCount, m.home.DoctorFailCount),
		fmt.Sprintf("• hooks/mcp: warn=%d fail=%d", m.home.HookWarnCount, m.home.HookFailCount),
		fmt.Sprintf("• sessions: stale_active=%d recent_failures=%d recent_commands=%d", m.home.StaleActiveSessionCount, m.home.RecentFailureCount, m.home.RecentCommandCount),
		fmt.Sprintf("• memories: accepted(reviewed)=%d candidate(inbox)=%d new=%s remember-intent=%d low-quality=%d stale=%d%s", m.home.AcceptedMemoryCount, m.home.CandidateMemoryCount, formatCockpitNewCandidateCount(m.home), m.home.RememberIntentCount, m.home.LowQualityMemoryCount, m.home.StaleMemoryCount, memoryScanSuffix),
		fmt.Sprintf("• payloads: large=%d", m.home.LargePayloadCount),
		"",
		m.styles.Subtle.Render("Planned cockpit surfaces:"),
		"• sessions: top dashboard and detail drill-down",
		"• tail: live event stream",
		"• memory: inbox notifications and review launcher",
		"• doctor: warnings and remediation commands",
		"",
		m.styles.Help.Render("t/l live tail · m memory review · q/esc/ctrl+c quit · ? help"),
	)
	if m.showHelp {
		lines = append(lines,
			"",
			m.styles.Subtle.Render("Fallback commands available today:"),
			"traceary top --snapshot [--json]",
			"traceary tail [--follow]",
			"traceary doctor --json",
			"traceary session handoff",
			"traceary memory inbox review",
		)
	}
	return strings.Join(lines, "\n")
}

func formatCockpitNewCandidateCount(home cockpitHomeSnapshot) string {
	if !home.NewCandidateMemoryKnown {
		return "untracked"
	}
	return fmt.Sprintf("%d", home.NewCandidateMemoryCount)
}

func formatCockpitMemoryReviewResult(result memoryInboxReviewResult) string {
	return fmt.Sprintf("memory review applied: accepted=%d rejected=%d distilled=%d failures=%d", len(result.Accepted), len(result.Rejected), len(result.Distilled), len(result.Failures))
}

func (m cockpitModel) memoryReviewView() string {
	lines := []string{
		m.styles.Title.Render("Traceary cockpit · memory review"),
		"",
	}
	switch {
	case m.memoryReview.loading:
		lines = append(lines, m.styles.Subtle.Render("Loading memory inbox review queue..."))
	case m.memoryReview.applying:
		lines = append(lines, m.styles.Subtle.Render("Applying memory review decisions..."))
	case m.memoryReview.err != nil:
		lines = append(lines, m.styles.Error.Render(m.memoryReview.err.Error()), "", m.styles.Help.Render("h home · q quit"))
	default:
		lines = append(lines, m.memoryReview.review.View(), "", m.styles.Help.Render("q finish review and refresh cockpit"))
	}
	return strings.Join(lines, "\n")
}

func (m cockpitModel) liveView() string {
	lines := []string{
		m.styles.Title.Render("Traceary cockpit · live tail"),
		m.styles.Subtle.Render(fmt.Sprintf("loaded=%s follow=%t rows=%d", formatJSONTime(m.live.loadedAt), m.live.follow, len(m.live.events))),
		"",
	}
	if m.live.err != nil {
		lines = append(lines, m.styles.Error.Render(m.live.err.Error()))
	} else if len(m.live.events) == 0 {
		if m.live.loading {
			lines = append(lines, m.styles.Subtle.Render("Loading live events..."))
		} else {
			lines = append(lines, m.styles.Subtle.Render("No recent events. Press r to refresh or f to follow."))
		}
	} else {
		for i, event := range m.live.events {
			prefix := "  "
			if i == m.live.selected {
				prefix = "> "
			}
			line := prefix + formatCockpitLiveEventRow(event, time.Local)
			if i == m.live.selected {
				line = m.styles.Active.Render(line)
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "", m.styles.Help.Render("h home · r refresh · f follow · enter detail · q quit"))
	return strings.Join(lines, "\n")
}

func (m cockpitModel) detailView() string {
	lines := []string{m.styles.Title.Render("Traceary cockpit · " + m.detail.title), ""}
	if m.detail.err != nil {
		lines = append(lines, m.styles.Error.Render(m.detail.err.Error()))
	} else {
		detailLines := m.detailLines()
		if len(detailLines) == 0 {
			detailLines = []string{m.styles.Subtle.Render("No detail lines.")}
		}
		lines = append(lines, detailLines[m.detailOffset:]...)
	}
	lines = append(lines, "", m.styles.Help.Render("t/l live · h home · ↑/↓ scroll · q quit"))
	return strings.Join(lines, "\n")
}

func (m cockpitModel) detailLines() []string {
	if m.detail.loading {
		return []string{Localize("Loading...", "読み込み中...")}
	}
	return m.detail.lines
}

func formatCockpitLiveEventRow(event *model.Event, loc *time.Location) string {
	if event == nil {
		return "-"
	}
	displayEvent := event
	truncated := false
	out := newTruncatedEventOutput(event, apptypes.DefaultTopSnapshotBodyLimit)
	if out.Truncated {
		truncated = true
		displayEvent = model.EventOfWithSourceHook(
			event.EventID(),
			event.Kind(),
			event.Client(),
			event.Agent(),
			event.SessionID(),
			event.Workspace(),
			out.Message,
			event.CreatedAt(),
			event.SourceHook(),
		)
	}
	row := formatEventCompactRow(displayEvent, eventTextFormatOptions{location: loc}, compactRowExtras{})
	if truncated {
		row += " [truncated]"
	}
	return row
}

// cockpitIO resolves the stdin/stdout pair the cockpit TUI should drive. Tests
// pass a non-file writer (e.g. *bytes.Buffer), making tui.Interactive refuse
// the run before any Bubble Tea program is spawned.
func cockpitIO(output io.Writer) (*os.File, *os.File) {
	stdout, _ := output.(*os.File)
	return os.Stdin, stdout
}
