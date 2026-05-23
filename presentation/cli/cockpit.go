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
const cockpitDoctorMessageWidth = 160
const cockpitNewEventLimit = 200

type cockpitExitError struct {
	message  string
	exitCode int
}

func (e cockpitExitError) Error() string { return e.message }
func (e cockpitExitError) ExitCode() int { return e.exitCode }

type cockpitCommandOptions struct {
	dbPath     string
	resetState bool
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
	cmd.Flags().BoolVar(&opts.resetState, "reset-state", false, Localize("reset local cockpit last-seen state before opening", "起動前に cockpit の local last-seen state をリセットする"))
	return cmd
}

func (c *RootCLI) runCockpit(ctx context.Context, output io.Writer, opts cockpitCommandOptions) error {
	stdin, stdout := cockpitIO(output)
	if !tui.Interactive(stdin, stdout) {
		return newCockpitNonInteractiveError(output)
	}
	if opts.resetState {
		if err := c.resetCockpitState(ctx); err != nil {
			return err
		}
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
	EventLastSeenAt         time.Time
	NewEventCount           int
	NewEventKnown           bool
	NewEventScanLimited     bool
	StaleMemoryCount        int
	RecentFailureCount      int
	RecentCommandCount      int
	LargePayloadCount       int
}

type cockpitDoctorSnapshot struct {
	LoadedAt time.Time
	DBPath   string
	Summary  doctorSummary
	Sections []cockpitDoctorSection
}

type cockpitDoctorSection struct {
	Name   string
	Checks []cockpitDoctorCheck
}

type cockpitDoctorCheck struct {
	Name             string
	Status           string
	Severity         string
	Section          string
	Message          string
	Hint             string
	FixCommand       string
	AutoFixAvailable bool
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
	if eventLastSeen, eventLastSeenKnown, err := c.loadCockpitEventLastSeen(ctx); err == nil && eventLastSeenKnown {
		home.EventLastSeenAt = eventLastSeen.at
		home.NewEventCount, home.NewEventScanLimited, home.NewEventKnown = c.countCockpitNewEvents(ctx, eventLastSeen)
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

type cockpitEventStateReader interface {
	EventLastSeenAt(ctx context.Context) (time.Time, bool, error)
}

type cockpitEventSeenIDsReader interface {
	EventLastSeenIDs(ctx context.Context) ([]string, bool, error)
}

type cockpitEventLastSeenCheckpoint struct {
	at     time.Time
	seenID map[string]struct{}
}

type cockpitStateWriter interface {
	MarkMemoryLastSeenAt(ctx context.Context, at time.Time) error
	MarkEventLastSeenAt(ctx context.Context, at time.Time, seenIDs []string) error
	ResetCockpitState(ctx context.Context) error
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

func (c *RootCLI) loadCockpitEventLastSeen(ctx context.Context) (cockpitEventLastSeenCheckpoint, bool, error) {
	reader, ok := c.cockpitState.(cockpitEventStateReader)
	if !ok {
		return cockpitEventLastSeenCheckpoint{}, false, nil
	}
	lastSeenAt, ok, err := reader.EventLastSeenAt(ctx)
	if err != nil {
		return cockpitEventLastSeenCheckpoint{}, false, xerrors.Errorf("%s: %w", Localize("failed to load cockpit event last-seen state", "cockpit event last-seen state の読み込みに失敗しました"), err)
	}
	if !ok {
		return cockpitEventLastSeenCheckpoint{}, false, nil
	}
	checkpoint := cockpitEventLastSeenCheckpoint{at: lastSeenAt, seenID: make(map[string]struct{})}
	if idsReader, ok := c.cockpitState.(cockpitEventSeenIDsReader); ok {
		seenIDs, _, err := idsReader.EventLastSeenIDs(ctx)
		if err != nil {
			return cockpitEventLastSeenCheckpoint{}, false, xerrors.Errorf("%s: %w", Localize("failed to load cockpit event last-seen IDs", "cockpit event last-seen ID の読み込みに失敗しました"), err)
		}
		for _, id := range seenIDs {
			checkpoint.seenID[id] = struct{}{}
		}
	}
	return checkpoint, true, nil
}

func (c *RootCLI) countCockpitNewEvents(ctx context.Context, checkpoint cockpitEventLastSeenCheckpoint) (int, bool, bool) {
	if c.event == nil {
		return 0, false, false
	}
	events, err := c.event.List(ctx, apptypes.NewEventListCriteriaBuilder(cockpitNewEventScanLimit(checkpoint)).From(checkpoint.at).Build())
	if err != nil {
		return 0, false, false
	}
	count := 0
	for _, event := range events {
		if isCockpitEventNewSinceCheckpoint(event, checkpoint) {
			count++
		}
	}
	limited := count > cockpitNewEventLimit
	if limited {
		count = cockpitNewEventLimit
	}
	return count, limited, true
}

func isCockpitEventNewSinceCheckpoint(event *model.Event, checkpoint cockpitEventLastSeenCheckpoint) bool {
	if event == nil {
		return false
	}
	if event.CreatedAt().After(checkpoint.at) {
		return true
	}
	if !event.CreatedAt().Equal(checkpoint.at) {
		return false
	}
	_, seen := checkpoint.seenID[event.EventID().String()]
	return !seen
}

func cockpitNewEventScanLimit(checkpoint cockpitEventLastSeenCheckpoint) int {
	return cockpitNewEventLimit + len(checkpoint.seenID) + 1
}

func (c *RootCLI) markCockpitMemorySeen(ctx context.Context, at time.Time) error {
	writer, ok := c.cockpitState.(cockpitStateWriter)
	if !ok {
		return nil
	}
	if err := writer.MarkMemoryLastSeenAt(ctx, at); err != nil {
		return xerrors.Errorf("failed to mark cockpit memory last-seen state: %w", err)
	}
	return nil
}

func (c *RootCLI) markCockpitEventsSeen(ctx context.Context, at time.Time, seenIDs []string) error {
	writer, ok := c.cockpitState.(cockpitStateWriter)
	if !ok {
		return nil
	}
	if err := writer.MarkEventLastSeenAt(ctx, at, seenIDs); err != nil {
		return xerrors.Errorf("failed to mark cockpit event last-seen state: %w", err)
	}
	return nil
}

func (c *RootCLI) resetCockpitState(ctx context.Context) error {
	writer, ok := c.cockpitState.(cockpitStateWriter)
	if !ok {
		return nil
	}
	if err := writer.ResetCockpitState(ctx); err != nil {
		return xerrors.Errorf("failed to reset cockpit state: %w", err)
	}
	return nil
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

func loadCockpitDoctorSnapshot(report *doctorReport, loadedAt time.Time) cockpitDoctorSnapshot {
	if report == nil {
		return cockpitDoctorSnapshot{LoadedAt: loadedAt}
	}
	snapshot := cockpitDoctorSnapshot{
		LoadedAt: loadedAt,
		DBPath:   report.DBPath,
		Summary:  report.Summary,
		Sections: make([]cockpitDoctorSection, 0, len(report.Sections)),
	}
	for _, section := range report.Sections {
		out := cockpitDoctorSection{Name: section.Name, Checks: make([]cockpitDoctorCheck, 0, len(section.Checks))}
		for _, check := range section.Checks {
			out.Checks = append(out.Checks, cockpitDoctorCheck{
				Name:             check.Name,
				Status:           check.Status,
				Severity:         check.Severity,
				Section:          check.Section,
				Message:          check.Message,
				Hint:             check.Hint,
				FixCommand:       check.FixCommand,
				AutoFixAvailable: check.AutoFixAvailable,
			})
		}
		snapshot.Sections = append(snapshot.Sections, out)
	}
	return snapshot
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
	loadCockpitDoctor(ctx context.Context) (cockpitDoctorSnapshot, error)
	loadCockpitLive(ctx context.Context, cursor tailCursor, initial bool) (cockpitLiveSnapshot, error)
	loadCockpitEventDetail(ctx context.Context, eventID domtypes.EventID) (topDetailContent, error)
	loadCockpitMemoryReviewItems(ctx context.Context) ([]apptypes.MemoryDetails, error)
	finishCockpitMemoryReview(ctx context.Context, final reviewModel, items []apptypes.MemoryDetails) (memoryInboxReviewResult, error)
	markCockpitMemorySeen(ctx context.Context, at time.Time) error
	markCockpitEventsSeen(ctx context.Context, at time.Time, seenIDs []string) error
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

func (l cockpitRuntimeLoader) loadCockpitDoctor(ctx context.Context) (cockpitDoctorSnapshot, error) {
	// The cockpit doctor pane is explicit (`d`/`r`) and intentionally uses the
	// read-only report builder. It never passes --fix, so remediation commands
	// are only displayed for the operator to run separately.
	report, err := l.root.loadCockpitDoctorReport(ctx, l.opts)
	if err != nil {
		return cockpitDoctorSnapshot{}, err
	}
	return loadCockpitDoctorSnapshot(report, topNowFunc()), nil
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

func (l cockpitRuntimeLoader) markCockpitMemorySeen(ctx context.Context, at time.Time) error {
	return l.root.markCockpitMemorySeen(ctx, at)
}

func (l cockpitRuntimeLoader) markCockpitEventsSeen(ctx context.Context, at time.Time, seenIDs []string) error {
	return l.root.markCockpitEventsSeen(ctx, at, seenIDs)
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
	if s.NewEventKnown && s.NewEventCount > 0 {
		hint := "press `2` to open live tail"
		if s.NewEventScanLimited {
			hint = "open live tail; count is capped at the cockpit scan limit"
		}
		warnings = append(warnings, cockpitWarning{severity: "WARN", label: fmt.Sprintf("new events=%d", s.NewEventCount), hint: hint})
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
	doctor                 cockpitDoctorState
	doctorOffset           int
	doctorRequestSeq       uint64
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
	cockpitModeDoctor
	cockpitModeLive
	cockpitModeDetail
	cockpitModeMemoryReview
	cockpitModeSessions
)

type cockpitSectionID int

const (
	cockpitSectionHome cockpitSectionID = iota
	cockpitSectionLive
	cockpitSectionDoctor
	cockpitSectionMemory
	cockpitSectionSessions
)

type cockpitNavigationSection struct {
	id    cockpitSectionID
	key   string
	label string
}

type cockpitAction struct {
	key         string
	description string
}

var cockpitNavigationSections = []cockpitNavigationSection{
	{id: cockpitSectionHome, key: "1", label: "Home"},
	{id: cockpitSectionLive, key: "2", label: "Live"},
	{id: cockpitSectionDoctor, key: "3", label: "Doctor"},
	{id: cockpitSectionMemory, key: "4", label: "Memory"},
	{id: cockpitSectionSessions, key: "5", label: "Sessions"},
}

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

type cockpitDoctorState struct {
	snapshot   cockpitDoctorSnapshot
	loading    bool
	requestSeq uint64
	err        error
}

type cockpitDoctorLoadedMsg struct {
	snapshot cockpitDoctorSnapshot
	seq      uint64
	err      error
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
	items  []apptypes.MemoryDetails
	seq    uint64
	seenAt time.Time
	err    error
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
	case cockpitDoctorLoadedMsg:
		if m.mode != cockpitModeDoctor || msg.seq != m.doctor.requestSeq {
			return m, nil
		}
		m.doctor.loading = false
		m.doctor.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		m.doctor.snapshot = msg.snapshot
		m.clampDoctorOffset()
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
		var markCmd tea.Cmd
		if msg.err == nil && m.mode == cockpitModeLive {
			markCmd = m.markCockpitEventsSeenCmd(msg.snapshot)
		}
		if m.mode == cockpitModeLive && m.live.follow {
			return m, tea.Batch(markCmd, m.cockpitLiveTickCmd())
		}
		return m, markCmd
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
		return m, m.markCockpitMemorySeenCmd(msg.seenAt)
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
	case isCockpitQuitKey(msg):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		return m, nil
	case isCockpitBackKey(msg):
		return m.backCockpitSection()
	}
	if section, ok := cockpitSectionFromKey(msg); ok {
		return m.openCockpitSection(section)
	}
	if section, ok := m.cockpitAdjacentSectionFromKey(msg); ok {
		return m.openCockpitSection(section)
	}
	switch m.mode {
	case cockpitModeDoctor:
		return m.updateDoctorKey(msg)
	case cockpitModeDetail:
		return m.updateDetailKey(msg)
	case cockpitModeLive:
		return m.updateLiveKey(msg)
	case cockpitModeSessions:
		return m.updateSessionsKey(msg)
	default:
		return m.updateHomeKey(msg)
	}
}

func isCockpitQuitKey(msg tea.KeyMsg) bool {
	if msg.Type == tea.KeyCtrlC {
		return true
	}
	return msg.Type == tea.KeyRunes && strings.ToLower(string(msg.Runes)) == "q"
}

func isCockpitBackKey(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyEsc
}

func cockpitSectionFromKey(msg tea.KeyMsg) (cockpitSectionID, bool) {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return 0, false
	}
	switch msg.Runes[0] {
	case '1':
		return cockpitSectionHome, true
	case '2':
		return cockpitSectionLive, true
	case '3':
		return cockpitSectionDoctor, true
	case '4':
		return cockpitSectionMemory, true
	case '5':
		return cockpitSectionSessions, true
	default:
		return 0, false
	}
}

func (m cockpitModel) cockpitAdjacentSectionFromKey(msg tea.KeyMsg) (cockpitSectionID, bool) {
	switch msg.String() {
	case "tab":
		return nextCockpitSection(m.activeCockpitSection(), 1), true
	case "shift+tab":
		return nextCockpitSection(m.activeCockpitSection(), -1), true
	default:
		return 0, false
	}
}

func nextCockpitSection(current cockpitSectionID, delta int) cockpitSectionID {
	index := 0
	for i, section := range cockpitNavigationSections {
		if section.id == current {
			index = i
			break
		}
	}
	next := (index + delta) % len(cockpitNavigationSections)
	if next < 0 {
		next += len(cockpitNavigationSections)
	}
	return cockpitNavigationSections[next].id
}

func (m cockpitModel) activeCockpitSection() cockpitSectionID {
	switch m.mode {
	case cockpitModeLive, cockpitModeDetail:
		return cockpitSectionLive
	case cockpitModeDoctor:
		return cockpitSectionDoctor
	case cockpitModeMemoryReview:
		return cockpitSectionMemory
	case cockpitModeSessions:
		return cockpitSectionSessions
	default:
		return cockpitSectionHome
	}
}

func (m cockpitModel) openCockpitSection(section cockpitSectionID) (tea.Model, tea.Cmd) {
	m.showHelp = false
	if section == m.activeCockpitSection() {
		if m.mode == cockpitModeDetail && section == cockpitSectionLive {
			return m.backCockpitSection()
		}
		return m, nil
	}
	switch section {
	case cockpitSectionHome:
		m.mode = cockpitModeHome
		return m, nil
	case cockpitSectionLive:
		if m.mode == cockpitModeDetail {
			m.detail = topDetailState{}
			m.detailOffset = 0
		}
		m.mode = cockpitModeLive
		return m, m.startCockpitLiveLoad(true)
	case cockpitSectionDoctor:
		m.mode = cockpitModeDoctor
		m.doctorOffset = 0
		return m, m.startCockpitDoctorLoad()
	case cockpitSectionMemory:
		m.mode = cockpitModeMemoryReview
		return m, m.startCockpitMemoryReviewLoad()
	case cockpitSectionSessions:
		m.mode = cockpitModeSessions
		return m, nil
	default:
		return m, nil
	}
}

func (m cockpitModel) backCockpitSection() (tea.Model, tea.Cmd) {
	m.showHelp = false
	switch m.mode {
	case cockpitModeDetail:
		m.mode = cockpitModeLive
		m.detail = topDetailState{}
		m.detailOffset = 0
		if m.live.follow {
			return m, m.cockpitLiveTickCmd()
		}
		return m, nil
	case cockpitModeHome:
		return m, nil
	default:
		m.mode = cockpitModeHome
		return m, nil
	}
}

func (m cockpitModel) updateHomeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyRunes {
		switch strings.ToLower(string(msg.Runes)) {
		// Legacy aliases stay supported, but the persistent shell advertises
		// numbered global navigation so operators do not have to memorize
		// per-screen one-off shortcuts.
		case "d":
			return m.openCockpitSection(cockpitSectionDoctor)
		case "t", "l":
			return m.openCockpitSection(cockpitSectionLive)
		case "m":
			return m.openCockpitSection(cockpitSectionMemory)
		}
	}
	return m, nil
}

func (m cockpitModel) updateDoctorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		m.doctorOffset--
		m.clampDoctorOffset()
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.doctorOffset++
		m.clampDoctorOffset()
		return m, nil
	case key.Matches(msg, m.keys.Refresh):
		return m, m.startCockpitDoctorLoad()
	}
	if msg.Type == tea.KeyRunes {
		switch strings.ToLower(string(msg.Runes)) {
		case "h":
			m.mode = cockpitModeHome
			m.doctor = cockpitDoctorState{}
			m.doctorOffset = 0
			return m, nil
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

func (m cockpitModel) updateSessionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Refresh) {
		return m, m.startCockpitHomeLoad()
	}
	return m, nil
}

func (m cockpitModel) updateMemoryReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	actions := defaultReviewActionKeys()
	if m.memoryReview.applying {
		return m, nil
	}
	if handled, next, cmd := m.updateMemoryReviewGlobalNavigationKey(msg); handled {
		return next, cmd
	}
	if msg.Type == tea.KeyRunes && strings.ToLower(string(msg.Runes)) == "h" && (m.memoryReview.loading || m.memoryReview.err != nil) {
		m.mode = cockpitModeHome
		if m.memoryReview.err != nil {
			m.memoryReview = cockpitMemoryReviewState{}
			return m, m.startCockpitHomeLoad()
		}
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

func (m cockpitModel) updateMemoryReviewGlobalNavigationKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return true, m, tea.Quit
	}
	if key.Matches(msg, m.keys.Help) && m.memoryReview.review.mode != reviewModeEdit {
		m.showHelp = !m.showHelp
		if !m.memoryReview.loading && m.memoryReview.err == nil {
			updated, _ := m.memoryReview.review.Update(msg)
			m.memoryReview.review = updated.(reviewModel)
		}
		return true, m, nil
	}
	reviewModeAllowsSectionJump := m.memoryReview.loading || m.memoryReview.err != nil || m.memoryReview.review.mode == reviewModeBrowse || m.memoryReview.review.mode == reviewModeHelp
	if !reviewModeAllowsSectionJump {
		return false, m, nil
	}
	if section, ok := cockpitSectionFromKey(msg); ok {
		next, cmd := m.leaveCockpitMemoryReviewForSection(section)
		return true, next, cmd
	}
	if section, ok := m.cockpitAdjacentSectionFromKey(msg); ok {
		next, cmd := m.leaveCockpitMemoryReviewForSection(section)
		return true, next, cmd
	}
	return false, m, nil
}

func (m cockpitModel) leaveCockpitMemoryReviewForSection(section cockpitSectionID) (tea.Model, tea.Cmd) {
	if section == cockpitSectionMemory {
		return m, nil
	}
	m.memoryReview = cockpitMemoryReviewState{}
	if section == cockpitSectionHome {
		m.mode = cockpitModeHome
		return m, m.startCockpitHomeLoad()
	}
	return m.openCockpitSection(section)
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

func (m *cockpitModel) startCockpitDoctorLoad() tea.Cmd {
	m.doctor.loading = true
	m.doctor.err = nil
	m.doctorOffset = 0
	m.doctorRequestSeq++
	m.doctor.requestSeq = m.doctorRequestSeq
	return m.fetchCockpitDoctorCmd(m.doctor.requestSeq)
}

func (m cockpitModel) fetchCockpitDoctorCmd(seq uint64) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	return func() tea.Msg {
		if loader == nil {
			return cockpitDoctorLoadedMsg{seq: seq, err: xerrors.Errorf("cockpit doctor loader is not configured")}
		}
		snapshot, err := loader.loadCockpitDoctor(ctx)
		return cockpitDoctorLoadedMsg{snapshot: snapshot, seq: seq, err: err}
	}
}

func (m cockpitModel) fetchCockpitMemoryReviewCmd() tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	seq := m.memoryReview.requestSeq
	seenAt := topNowFunc().UTC()
	return func() tea.Msg {
		if loader == nil {
			return cockpitMemoryReviewLoadedMsg{seq: seq, seenAt: seenAt, err: xerrors.Errorf("cockpit memory review loader is not configured")}
		}
		items, err := loader.loadCockpitMemoryReviewItems(ctx)
		return cockpitMemoryReviewLoadedMsg{items: items, seq: seq, seenAt: seenAt, err: err}
	}
}

func (m cockpitModel) markCockpitMemorySeenCmd(at time.Time) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	return func() tea.Msg {
		if loader != nil {
			_ = loader.markCockpitMemorySeen(ctx, at)
		}
		return nil
	}
}

func (m cockpitModel) markCockpitEventsSeenCmd(snapshot cockpitLiveSnapshot) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	at := cockpitLiveSeenAt(snapshot)
	seenIDs := cockpitLiveSeenIDs(snapshot)
	return func() tea.Msg {
		if loader != nil && !at.IsZero() {
			_ = loader.markCockpitEventsSeen(ctx, at, seenIDs)
		}
		return nil
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

func (m *cockpitModel) clampDoctorOffset() {
	maxOffset := len(m.doctorLines()) - 1
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.doctorOffset < 0 {
		m.doctorOffset = 0
	}
	if m.doctorOffset > maxOffset {
		m.doctorOffset = maxOffset
	}
}

func (m cockpitModel) View() string {
	switch m.mode {
	case cockpitModeDoctor:
		return m.doctorView()
	case cockpitModeLive:
		return m.liveView()
	case cockpitModeDetail:
		return m.detailView()
	case cockpitModeMemoryReview:
		return m.memoryReviewView()
	case cockpitModeSessions:
		return m.sessionsView()
	}
	return m.homeView()
}

func (m cockpitModel) homeView() string {
	memoryScanSuffix := ""
	if m.home.MemoryScanLimited {
		memoryScanSuffix = " scan_limited=true"
	}
	eventScanSuffix := ""
	if m.home.NewEventScanLimited {
		eventScanSuffix = " scan_limited=true"
	}
	lines := []string{
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
		fmt.Sprintf("• sessions: stale_active=%d recent_failures=%d recent_commands=%d new_events=%s%s", m.home.StaleActiveSessionCount, m.home.RecentFailureCount, m.home.RecentCommandCount, formatCockpitNewEventCount(m.home), eventScanSuffix),
		fmt.Sprintf("• memories: accepted(reviewed)=%d candidate(inbox)=%d new=%s remember-intent=%d low-quality=%d stale=%d%s", m.home.AcceptedMemoryCount, m.home.CandidateMemoryCount, formatCockpitNewCandidateCount(m.home), m.home.RememberIntentCount, m.home.LowQualityMemoryCount, m.home.StaleMemoryCount, memoryScanSuffix),
		fmt.Sprintf("• payloads: large=%d", m.home.LargePayloadCount),
		"",
		m.styles.Subtle.Render("Cockpit surfaces:"),
		"• 2 Live: live event stream and detail drill-down",
		"• 3 Doctor: warnings, skips, and remediation commands",
		"• 4 Memory: inbox notifications and review launcher",
		"• 5 Sessions: session and handoff entry points",
	)
	return m.renderCockpitShell("home", lines, "")
}

func formatCockpitNewCandidateCount(home cockpitHomeSnapshot) string {
	if !home.NewCandidateMemoryKnown {
		return "untracked"
	}
	return fmt.Sprintf("%d", home.NewCandidateMemoryCount)
}

func formatCockpitNewEventCount(home cockpitHomeSnapshot) string {
	if !home.NewEventKnown {
		return "untracked"
	}
	return fmt.Sprintf("%d", home.NewEventCount)
}

func formatCockpitMemoryReviewResult(result memoryInboxReviewResult) string {
	return fmt.Sprintf("memory review applied: accepted=%d rejected=%d distilled=%d failures=%d", len(result.Accepted), len(result.Rejected), len(result.Distilled), len(result.Failures))
}

func (m cockpitModel) doctorView() string {
	lines := []string{}
	content := m.doctorLines()
	offset := m.doctorOffset
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	lines = append(lines, content[offset:]...)
	return m.renderCockpitShell("doctor", lines, m.doctorLocalHelp())
}

func (m cockpitModel) doctorLines() []string {
	if m.doctor.loading {
		return []string{m.styles.Subtle.Render("Loading doctor checks...")}
	}
	if m.doctor.err != nil {
		return []string{m.styles.Error.Render(m.doctor.err.Error())}
	}
	snapshot := m.doctor.snapshot
	lines := []string{
		m.styles.Subtle.Render(fmt.Sprintf("loaded=%s db=%s", formatJSONTime(snapshot.LoadedAt), formatOptionalColumn(snapshot.DBPath))),
		fmt.Sprintf("summary: pass=%d warn=%d fail=%d", snapshot.Summary.Pass, snapshot.Summary.Warn, snapshot.Summary.Fail),
		"",
	}
	if len(snapshot.Sections) == 0 {
		return append(lines,
			m.styles.Success.Render("No doctor checks reported."),
			m.styles.Subtle.Render("Press r to refresh checks or 1 to return Home."),
		)
	}
	rendered := 0
	for _, section := range snapshot.Sections {
		if len(section.Checks) == 0 {
			continue
		}
		lines = append(lines, m.styles.Subtle.Render(section.Name))
		for _, check := range section.Checks {
			rendered++
			label := cockpitDoctorStatusLabel(check)
			message := truncateNormalized(check.Message, cockpitDoctorMessageWidth)
			line := fmt.Sprintf("• [%s] %s: %s", label, check.Name, message)
			lines = append(lines, renderCockpitDoctorCheck(m.styles, check, line))
			if check.Hint != "" {
				lines = append(lines, "  hint: "+truncateNormalized(check.Hint, cockpitDoctorMessageWidth))
			}
			if check.FixCommand != "" {
				lines = append(lines, "  fix: "+check.FixCommand)
			} else if check.AutoFixAvailable {
				lines = append(lines, "  fix: traceary doctor --fix --dry-run")
			}
		}
		lines = append(lines, "")
	}
	if rendered == 0 {
		lines = append(lines,
			m.styles.Success.Render("No doctor checks reported."),
			m.styles.Subtle.Render("Press r to refresh checks or 1 to return Home."),
		)
	}
	return lines
}

func cockpitDoctorStatusLabel(check cockpitDoctorCheck) string {
	if check.Status != "" {
		return strings.ToUpper(check.Status)
	}
	if check.Severity != "" {
		return strings.ToUpper(check.Severity)
	}
	return "PASS"
}

func renderCockpitDoctorCheck(styles tui.Styles, check cockpitDoctorCheck, line string) string {
	switch cockpitDoctorStatusLabel(check) {
	case strings.ToUpper(doctorStatusFail), doctorSeverityFail:
		return styles.Error.Render(line)
	case strings.ToUpper(doctorStatusWarn), doctorSeverityWarn:
		return styles.Warning.Render(line)
	case strings.ToUpper(doctorStatusPass), doctorSeverityPass:
		return styles.Success.Render(line)
	default:
		return styles.Subtle.Render(line)
	}
}

func (m cockpitModel) memoryReviewView() string {
	lines := []string{}
	switch {
	case m.memoryReview.loading:
		lines = append(lines, m.styles.Subtle.Render("Loading memory inbox review queue..."))
	case m.memoryReview.applying:
		lines = append(lines, m.styles.Subtle.Render("Applying memory review decisions..."))
	case m.memoryReview.err != nil:
		lines = append(lines, m.styles.Error.Render(m.memoryReview.err.Error()))
	default:
		lines = append(lines, m.memoryReview.review.View())
	}
	return m.renderCockpitShell("memory review", lines, m.memoryReviewLocalHelp())
}

func (m cockpitModel) liveView() string {
	lines := []string{
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
	return m.renderCockpitShell("live tail", lines, m.liveLocalHelp())
}

func (m cockpitModel) detailView() string {
	lines := []string{}
	if m.detail.err != nil {
		lines = append(lines, m.styles.Error.Render(m.detail.err.Error()))
	} else {
		detailLines := m.detailLines()
		if len(detailLines) == 0 {
			detailLines = []string{m.styles.Subtle.Render("No detail lines.")}
		}
		lines = append(lines, detailLines[m.detailOffset:]...)
	}
	return m.renderCockpitShell(m.detail.title, lines, m.detailLocalHelp())
}

func (m cockpitModel) sessionsView() string {
	eventScanSuffix := ""
	if m.home.NewEventScanLimited {
		eventScanSuffix = " scan_limited=true"
	}
	lines := []string{
		m.styles.Subtle.Render("Session and handoff workflows"),
		"",
		fmt.Sprintf("• stale active sessions=%d", m.home.StaleActiveSessionCount),
		fmt.Sprintf("• recent failures=%d", m.home.RecentFailureCount),
		fmt.Sprintf("• recent commands=%d", m.home.RecentCommandCount),
		fmt.Sprintf("• new events=%s%s", formatCockpitNewEventCount(m.home), eventScanSuffix),
		"",
		m.styles.Subtle.Render("Available today:"),
		"traceary top --snapshot [--json]",
		"traceary session handoff",
		"traceary tail [--follow]",
		"",
		m.styles.Subtle.Render("Dedicated session list and handoff drill-down remain compatible with the existing subcommands."),
	}
	return m.renderCockpitShell("sessions", lines, "r refresh session summary")
}

func (m cockpitModel) renderCockpitShell(title string, body []string, localHelp string) string {
	lines := []string{
		m.styles.Title.Render("Traceary cockpit · " + title),
		m.styles.Help.Render(m.cockpitNavigationBar()),
		"",
	}
	lines = append(lines, body...)
	lines = append(lines, "", m.styles.Help.Render(m.cockpitGlobalFooter(localHelp)))
	if m.showHelp {
		lines = append(lines, "")
		lines = append(lines, m.cockpitContextualHelp()...)
	}
	return strings.Join(lines, "\n")
}

func (m cockpitModel) cockpitNavigationBar() string {
	active := m.activeCockpitSection()
	parts := make([]string, 0, len(cockpitNavigationSections))
	for _, section := range cockpitNavigationSections {
		label := section.key + " " + section.label
		if section.id == active {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	return "sections: " + strings.Join(parts, "  ")
}

func (m cockpitModel) cockpitGlobalFooter(localHelp string) string {
	quitHelp := "q/ctrl+c quit"
	if m.mode == cockpitModeMemoryReview {
		quitHelp = "ctrl+c quit"
		if m.memoryReview.applying {
			quitHelp = "quit disabled while applying"
		}
	}
	parts := []string{}
	if m.cockpitSectionNavigationAvailable() {
		parts = append(parts, "1-5 sections", "tab/shift+tab next/prev")
	}
	if m.cockpitBackAvailable() {
		parts = append(parts, "esc back")
	}
	if quitHelp != "" {
		parts = append(parts, quitHelp)
	}
	if m.cockpitHelpAvailable() {
		parts = append(parts, "? help")
	}
	if localHelp != "" {
		parts = append(parts, localHelp)
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) cockpitSectionNavigationAvailable() bool {
	if m.mode != cockpitModeMemoryReview {
		return true
	}
	if m.memoryReview.applying {
		return false
	}
	return m.memoryReview.loading || m.memoryReview.err != nil || m.memoryReview.review.mode == reviewModeBrowse || m.memoryReview.review.mode == reviewModeHelp
}

func (m cockpitModel) cockpitBackAvailable() bool {
	if m.mode == cockpitModeHome {
		return false
	}
	if m.mode == cockpitModeMemoryReview {
		switch {
		case m.memoryReview.applying:
			return false
		case m.memoryReview.loading, m.memoryReview.err != nil:
			return true
		default:
			return m.memoryReview.review.mode == reviewModeBrowse
		}
	}
	return true
}

func (m cockpitModel) cockpitHelpAvailable() bool {
	if m.mode != cockpitModeMemoryReview {
		return true
	}
	return !m.memoryReview.applying && m.memoryReview.review.mode != reviewModeEdit
}

func (m cockpitModel) cockpitContextualHelp() []string {
	lines := []string{m.styles.Subtle.Render("Action menu")}
	actions := m.cockpitContextualActions()
	if len(actions) == 0 {
		lines = append(lines, "• No local actions are available while this operation is in progress.")
	} else {
		for _, action := range actions {
			if action.key == "" {
				lines = append(lines, "• "+action.description)
				continue
			}
			lines = append(lines, fmt.Sprintf("• %-12s %s", action.key, action.description))
		}
	}
	lines = append(lines,
		"",
		m.styles.Subtle.Render("Global navigation"),
		"1 Home      triage overview and notifications",
		"2 Live      event stream and event details",
		"3 Doctor    health checks and remediation hints",
		"4 Memory    inbox review queue",
		"5 Sessions  session and handoff entry points",
		"tab / shift+tab cycle sections",
		"esc backs out to the previous cockpit level; q exits the TUI",
		"",
		m.styles.Subtle.Render("Fallback commands available today:"),
		"traceary top --snapshot [--json]",
		"traceary tail [--follow]",
		"traceary doctor --json",
		"traceary session handoff",
		"traceary memory inbox review",
		"traceary tui --reset-state",
	)
	return lines
}

func (m cockpitModel) cockpitContextualActions() []cockpitAction {
	switch m.mode {
	case cockpitModeDoctor:
		actions := []cockpitAction{{key: "r", description: "Refresh doctor checks"}}
		if len(m.doctorLines()) > 3 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: "Scroll doctor output"})
		}
		if !m.doctor.loading && m.doctor.err == nil && cockpitDoctorHasRemediation(m.doctor.snapshot) {
			actions = append(actions, cockpitAction{description: "Remediation commands are shown inline; copy and run them outside the cockpit."})
		}
		return actions
	case cockpitModeLive:
		actions := []cockpitAction{
			{key: "r", description: "Refresh live events"},
			{key: "f", description: m.liveFollowActionDescription()},
		}
		if len(m.live.events) > 0 {
			actions = append(actions, cockpitAction{key: "enter", description: "Open selected event detail"})
			if len(m.live.events) > 1 {
				actions = append(actions, cockpitAction{key: "↑/↓", description: "Select an event"})
			}
		}
		return actions
	case cockpitModeDetail:
		actions := []cockpitAction{{key: "esc", description: "Return to Live"}}
		if len(m.detailLines()) > 1 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: "Scroll event detail"})
		}
		return actions
	case cockpitModeMemoryReview:
		return m.memoryReviewContextualActions()
	case cockpitModeSessions:
		return []cockpitAction{
			{key: "r", description: "Refresh session summary"},
			{description: "Use `traceary session handoff` for the full handoff outside the cockpit."},
		}
	default:
		return []cockpitAction{
			{key: "2", description: "Open Live tail"},
			{key: "3", description: "Run Doctor checks"},
			{key: "4", description: "Open Memory review"},
			{key: "5", description: "Open Sessions and handoff entry points"},
		}
	}
}

func cockpitDoctorHasRemediation(snapshot cockpitDoctorSnapshot) bool {
	for _, section := range snapshot.Sections {
		for _, check := range section.Checks {
			if check.FixCommand != "" || check.AutoFixAvailable {
				return true
			}
		}
	}
	return false
}

func (m cockpitModel) liveFollowActionDescription() string {
	if m.live.follow {
		return "Pause follow mode"
	}
	return "Start follow mode"
}

func (m cockpitModel) memoryReviewContextualActions() []cockpitAction {
	switch {
	case m.memoryReview.loading:
		return []cockpitAction{
			{key: "esc", description: "Return to Home while the inbox loads"},
			{key: "q", description: "Quit without applying decisions"},
		}
	case m.memoryReview.applying:
		return nil
	case m.memoryReview.err != nil:
		return []cockpitAction{
			{key: "esc", description: "Return to Home"},
			{key: "q", description: "Quit without applying decisions"},
		}
	}
	if len(m.memoryReview.items) == 0 {
		return []cockpitAction{
			{key: "q", description: "Finish review and refresh Home"},
			{key: "esc", description: "Return to Home without applying"},
		}
	}
	switch m.memoryReview.review.mode {
	case reviewModeEdit:
		return []cockpitAction{
			{key: "enter", description: "Commit operator-authored fact"},
			{key: "esc", description: "Cancel edit"},
			{key: "backspace", description: "Edit text"},
		}
	case reviewModeViewEvidence:
		return []cockpitAction{
			{key: "v / esc", description: "Close evidence view"},
			{key: "q", description: "Finish review and apply queued decisions"},
		}
	case reviewModeHelp:
		return []cockpitAction{
			{key: "? / esc", description: "Close memory review help"},
			{key: "q", description: "Finish review and apply queued decisions"},
		}
	default:
		acceptDescription := "Accept as-is only when the checklist passes"
		if len(m.memoryReview.items) > 0 && m.memoryReview.review.cursor >= 0 && m.memoryReview.review.cursor < len(m.memoryReview.items) && memoryReviewRequiresAcceptConfirmation(m.memoryReview.items[m.memoryReview.review.cursor]) {
			acceptDescription = "Accept as-is (requires pressing a twice; prefer edit/distill if unsure)"
		}
		actions := []cockpitAction{
			{key: "a", description: acceptDescription},
			{key: "x", description: "Reject current candidate"},
			{key: "s", description: "Skip when more context is needed"},
			{key: "e", description: "Edit/distill into an operator-authored fact when wording is unclear"},
			{key: "v", description: "View evidence and artifact refs"},
			{key: "q", description: "Finish review and apply queued decisions"},
			{description: "Accept checklist: factual, stable, useful later, scoped correctly, evidence-backed, not duplicate/stale."},
		}
		if len(m.memoryReview.items) > 1 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: "Navigate candidates"})
		}
		return actions
	}
}

func (m cockpitModel) liveLocalHelp() string {
	parts := []string{"r refresh", "f follow"}
	if m.live.follow {
		parts[1] = "f pause"
	}
	if len(m.live.events) > 0 {
		parts = append(parts, "enter detail")
		if len(m.live.events) > 1 {
			parts = append(parts, "↑/↓ select")
		}
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) doctorLocalHelp() string {
	parts := []string{"r refresh"}
	if len(m.doctorLines()) > 3 {
		parts = append(parts, "↑/↓ scroll")
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) detailLocalHelp() string {
	if len(m.detailLines()) > 1 {
		return "↑/↓ scroll"
	}
	return ""
}

func (m cockpitModel) memoryReviewLocalHelp() string {
	switch {
	case m.memoryReview.loading, m.memoryReview.err != nil:
		return "q quit"
	case m.memoryReview.applying:
		return "applying decisions"
	case len(m.memoryReview.items) == 0:
		return "q finish review"
	}
	switch m.memoryReview.review.mode {
	case reviewModeEdit:
		return "enter commit · esc cancel · backspace edit"
	case reviewModeViewEvidence:
		return "v/esc close evidence · q finish/apply"
	case reviewModeHelp:
		return "?/esc close help · q finish/apply"
	default:
		return "a accept as-is · x reject · s skip · e edit/distill · v evidence · q finish/apply"
	}
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

func cockpitLiveSeenAt(snapshot cockpitLiveSnapshot) time.Time {
	if !snapshot.Cursor.timestamp.IsZero() {
		return snapshot.Cursor.timestamp
	}
	return snapshot.LoadedAt
}

func cockpitLiveSeenIDs(snapshot cockpitLiveSnapshot) []string {
	if snapshot.Cursor.timestamp.IsZero() {
		return nil
	}
	seenIDs := make([]string, 0, len(snapshot.Cursor.seenIDs))
	for id := range snapshot.Cursor.seenIDs {
		seenIDs = append(seenIDs, id)
	}
	slices.Sort(seenIDs)
	return seenIDs
}

// cockpitIO resolves the stdin/stdout pair the cockpit TUI should drive. Tests
// pass a non-file writer (e.g. *bytes.Buffer), making tui.Interactive refuse
// the run before any Bubble Tea program is spawned.
func cockpitIO(output io.Writer) (*os.File, *os.File) {
	stdout, _ := output.(*os.File)
	return os.Stdin, stdout
}
