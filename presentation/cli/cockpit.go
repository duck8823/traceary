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
const cockpitLiveDefaultViewportRows = 12
const cockpitLiveMinViewportRows = 1
const cockpitLiveBasePreludeRows = 2
const cockpitShellChromeRows = 5
const cockpitDoctorMessageWidth = 160
const cockpitNewEventLimit = 200
const cockpitTopUnknownWidth = 120
const cockpitTopShellHorizontalPadding = 6
const cockpitTopRowPrefixWidth = 2
const cockpitTopDefaultViewportRows = 12
const cockpitTopSummaryChromeRows = 18
const cockpitTopMinViewportRows = 5
const cockpitTopDetailDefaultViewportRows = 16
const cockpitTopDetailChromeRows = 6

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
			"Open the Traceary operator cockpit TUI. It gathers Tail (`tail`), Sessions (`sessions`), Doctor (`doctor`), Handoff, and memory review workflows behind one TTY-only shell; `traceary top` remains a non-interactive compatibility command. In an interactive terminal, bare `traceary` opens the same Tail-first TUI by default; `traceary tui` remains the explicit compatibility entrypoint for operators who prefer a named command.",
			"Traceary operator cockpit TUI を開きます。Tail (`tail`) / Sessions (`sessions`) / Doctor (`doctor`) / Handoff / メモリ確認を 1 つの TTY 専用 shell にまとめます。`traceary top` は非対話の互換 command として残ります。対話 terminal では subcommand なしの `traceary` も同じ Tail-first TUI をデフォルトで開きます。`traceary tui` は明示的に呼びたい operator のための互換 entrypoint として残ります。",
		),
		Args: noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runCockpit(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), opts)
		},
	}
	bindCockpitFlags(cmd, &opts)
	return cmd
}

func bindCockpitFlags(cmd *cobra.Command, opts *cockpitCommandOptions) {
	cmd.Flags().StringVar(&opts.dbPath, "db-path", "", dbPathFlagUsage())
	cmd.Flags().BoolVar(&opts.resetState, "reset-state", false, Localize("reset local cockpit last-seen state before opening", "起動前に cockpit の local last-seen state をリセットする"))
}

func (c *RootCLI) runCockpit(ctx context.Context, input io.Reader, output io.Writer, opts cockpitCommandOptions) error {
	stdin, stdout, ok := cockpitIO(input, output)
	if !ok || !c.isCockpitInteractive(stdin, stdout) {
		return newCockpitNonInteractiveError(output)
	}
	if opts.resetState {
		if err := c.resetCockpitState(ctx); err != nil {
			return err
		}
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.DefaultStyles(), cockpitHomeSnapshot{LoadedAt: topNowFunc()})
	model.loader = cockpitRuntimeLoader{root: c, opts: opts}
	model.loaderCtx = ctx
	if err := tui.Run(model, tui.RunOptions{Input: stdin, Output: stdout, AltScreen: true}); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to run cockpit TUI", "cockpit TUI の実行に失敗しました"), err)
	}
	return nil
}

type cockpitInteractiveFunc func(*os.File, *os.File) bool

func (c *RootCLI) isCockpitInteractive(stdin, stdout *os.File) bool {
	if c.cockpitInteractive != nil {
		return c.cockpitInteractive(stdin, stdout)
	}
	return tui.Interactive(stdin, stdout)
}

func (c *RootCLI) cockpitRunnerFunc() cockpitRunnerFunc {
	if c.cockpitRunner != nil {
		return c.cockpitRunner
	}
	return c.runCockpit
}

func withCockpitRuntimeForTest(interactive cockpitInteractiveFunc, runner cockpitRunnerFunc) RootCLIOption {
	return func(c *RootCLI) {
		c.cockpitInteractive = interactive
		c.cockpitRunner = runner
	}
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
		return nil, xerrors.New(Localize("store management usecase is not configured", "ストア管理ユースケースが設定されていません"))
	}
	if c.hooksOrchestrator == nil {
		return nil, xerrors.New(Localize("hooks orchestrator is not configured", "hooks orchestrator が設定されていません"))
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
	loadCockpitTop(ctx context.Context, criteria topDataCriteria) (topDataSnapshot, error)
	loadCockpitTopDetail(ctx context.Context, req topDetailRequest) (topDetailContent, error)
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
	Extras   map[domtypes.EventID]compactRowExtras
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

func (l cockpitRuntimeLoader) loadCockpitTop(ctx context.Context, criteria topDataCriteria) (topDataSnapshot, error) {
	if l.root.storeManagement != nil {
		if err := l.root.initializeStore(ctx, l.opts.dbPath); err != nil {
			return topDataSnapshot{}, err
		}
	}
	return l.root.newTopDataLoader().loadSnapshot(ctx, criteria)
}

func (l cockpitRuntimeLoader) loadCockpitTopDetail(ctx context.Context, req topDetailRequest) (topDetailContent, error) {
	if l.root.storeManagement != nil {
		if err := l.root.initializeStore(ctx, l.opts.dbPath); err != nil {
			return topDetailContent{}, err
		}
	}
	return l.root.newTopDataLoader().loadDetail(ctx, req)
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
		return nil, xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
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
		return memoryInboxReviewResult{}, xerrors.New(Localize("memory usecase is not configured", "memory ユースケースが設定されていません"))
	}
	result, err := applyInboxReviewDecisions(ctx, l.root.memory, final.Decisions(), items)
	if err != nil {
		return result, err
	}
	if len(result.Failures) > 0 {
		return result, memoryReviewFailureError(result)
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
		return cockpitLiveSnapshot{}, xerrors.New(Localize("list events query service is not configured", "イベント一覧クエリサービスが設定されていません"))
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
		return cockpitLiveSnapshot{Events: events, Extras: c.cockpitLiveExtras(ctx, events), Cursor: cursor, LoadedAt: now}, nil
	}
	base := apptypes.NewEventListCriteriaBuilder(defaultTailBatchSize).Build()
	events, err := c.pollTailEvents(ctx, base, cursor, now)
	if err != nil {
		return cockpitLiveSnapshot{}, err
	}
	cursor.Advance(events)
	return cockpitLiveSnapshot{Events: events, Extras: c.cockpitLiveExtras(ctx, events), Cursor: cursor, LoadedAt: now}, nil
}

func (c *RootCLI) cockpitLiveExtras(ctx context.Context, events []*model.Event) map[domtypes.EventID]compactRowExtras {
	resolver := c.makeCompactExtrasResolver(ctx, defaultReadFields, true)
	if resolver == nil || len(events) == 0 {
		return nil
	}
	extras := make(map[domtypes.EventID]compactRowExtras)
	for _, event := range events {
		if event == nil {
			continue
		}
		extras[event.EventID()] = resolver(event)
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
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

func formatCockpitCheckpoint(at time.Time) string {
	if at.IsZero() {
		return Localize("not recorded", "未記録")
	}
	return formatJSONTime(at)
}

func newCockpitNonInteractiveError(output io.Writer) error {
	guidance := Localize(
		"Traceary cockpit requires an interactive terminal (TTY).\nUse the existing non-interactive commands instead:\n  traceary list\n  traceary sessions --snapshot [--json]\n  traceary top --snapshot [--json] # compatibility\n  traceary doctor --json\n  traceary session handoff\n  traceary memory inbox list\nRun `traceary` (or `traceary tui`) from a terminal to open the cockpit.",
		"Traceary cockpit には対話 terminal (TTY) が必要です。\n非対話 shell では既存 command を使ってください:\n  traceary list\n  traceary sessions --snapshot [--json]\n  traceary top --snapshot [--json] # compatibility\n  traceary doctor --json\n  traceary session handoff\n  traceary memory inbox list\nterminal から `traceary`（または `traceary tui`）を実行すると cockpit を開けます。",
	)
	if output != nil {
		_, _ = fmt.Fprintln(output, guidance)
	}
	return cockpitExitError{message: guidance, exitCode: cockpitExitCodeNotInteractive}
}

type cockpitModel struct {
	keys   tui.KeyMap
	styles tui.Styles
	width  int
	height int

	loader    cockpitLoader
	loaderCtx context.Context

	showHelp  bool
	mode      cockpitMode
	home      cockpitHomeSnapshot
	statusMsg string
	statusErr string

	live                   cockpitLiveState
	top                    cockpitTopState
	detail                 topDetailState
	detailOffset           int
	homeRequestSeq         uint64
	doctor                 cockpitDoctorState
	doctorOffset           int
	doctorRequestSeq       uint64
	memoryReview           cockpitMemoryReviewState
	memoryReviewRequestSeq uint64
	settings               cockpitSettingsState
}

func newCockpitModel(keys tui.KeyMap, styles tui.Styles, home cockpitHomeSnapshot) cockpitModel {
	return cockpitModel{
		keys:   keys,
		styles: styles,
		home:   home,
		mode:   cockpitModeLive,
		live: cockpitLiveState{
			loadedAt: home.LoadedAt,
			loading:  true,
			follow:   true,
		},
		top: cockpitTopState{
			loadedAt: home.LoadedAt,
		},
	}
}

func (m cockpitModel) Init() tea.Cmd {
	if m.mode == cockpitModeLive {
		return tea.Batch(
			m.fetchCockpitHomeCmd(m.homeRequestSeq),
			m.fetchCockpitLiveCmd(true, m.live.requestSeq),
		)
	}
	return nil
}

type cockpitMode int

const (
	cockpitModeLive cockpitMode = iota
	cockpitModeTop
	cockpitModeDoctor
	cockpitModeDetail
	cockpitModeMemoryReview
	cockpitModeSettings
)

type cockpitSectionID int

const (
	cockpitSectionLive cockpitSectionID = iota
	cockpitSectionTop
	cockpitSectionMemory
	cockpitSectionSettings
	cockpitSectionCount
)

type cockpitAction struct {
	key         string
	description string
}

type cockpitSignalSeverity string

const (
	cockpitSignalOK      cockpitSignalSeverity = "OK"
	cockpitSignalInfo    cockpitSignalSeverity = "INFO"
	cockpitSignalWarning cockpitSignalSeverity = "WARN"
	cockpitSignalFailure cockpitSignalSeverity = "FAIL"
)

type cockpitTopSignal struct {
	severity    cockpitSignalSeverity
	label       string
	description string
	actionKey   string
	actionLabel string
}

type cockpitLiveState struct {
	events         []*model.Event
	extras         map[domtypes.EventID]compactRowExtras
	cursor         tailCursor
	loadedAt       time.Time
	loading        bool
	follow         bool
	pausedNewCount int
	selected       int
	requestSeq     uint64
	err            error
}

type cockpitTopState struct {
	snapshot     topDataSnapshot
	criteria     topDataCriteria
	loadedAt     time.Time
	loading      bool
	loaded       bool
	requestSeq   uint64
	err          error
	rows         []cockpitTopRow
	selected     int
	offset       int
	detail       topDetailState
	detailOpen   bool
	detailSeq    uint64
	detailOffset int
}

type cockpitTopRow struct {
	line       string
	pane       topPane
	target     topDetailTarget
	selectable bool
	header     bool
}

type cockpitLiveMsg struct {
	snapshot cockpitLiveSnapshot
	initial  bool
	seq      uint64
	err      error
}

type cockpitLiveTickMsg struct{}

type cockpitTopLoadedMsg struct {
	snapshot topDataSnapshot
	criteria topDataCriteria
	loadedAt time.Time
	seq      uint64
	err      error
}

type cockpitTopDetailLoadedMsg struct {
	request topDetailRequest
	content topDetailContent
	seq     uint64
	err     error
}

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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampLiveSelection()
		m.rebuildCockpitTopRows()
		m.clampTopSelection()
		m.clampTopDetailOffset()
		m.clampDetailOffset()
		m.clampDoctorOffset()
		return m, nil
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
				m.live.extras = cloneCockpitLiveExtras(msg.snapshot.Extras)
				m.live.pausedNewCount = 0
			} else {
				if !m.live.follow && len(msg.snapshot.Events) > 0 {
					m.live.pausedNewCount += len(msg.snapshot.Events)
				}
				m.live.events = append(m.live.events, msg.snapshot.Events...)
				m.mergeCockpitLiveExtras(msg.snapshot.Extras)
				m.trimCockpitLiveEventsToLimit()
			}
			m.live.cursor = msg.snapshot.Cursor
			m.live.loadedAt = msg.snapshot.LoadedAt
			m.clampLiveSelection()
			if m.live.follow {
				m.moveCockpitLiveSelectionToNewest()
				m.live.pausedNewCount = 0
			}
		}
		var markCmd tea.Cmd
		if msg.err == nil && m.mode == cockpitModeLive && m.live.follow {
			markCmd = m.markCockpitEventsSeenCmd(msg.snapshot)
		}
		if m.mode == cockpitModeLive {
			return m, tea.Batch(markCmd, m.cockpitLiveTickCmd())
		}
		return m, markCmd
	case cockpitTopLoadedMsg:
		if msg.seq != m.top.requestSeq {
			return m, nil
		}
		m.top.loading = false
		m.top.err = msg.err
		if msg.err != nil {
			m.top.criteria = msg.criteria
			m.top.loadedAt = msg.loadedAt
			return m, nil
		}
		m.top.snapshot = msg.snapshot
		m.top.criteria = msg.criteria
		m.top.loadedAt = msg.loadedAt
		m.top.loaded = true
		m.rebuildCockpitTopRows()
		m.clampTopSelection()
		return m, nil
	case cockpitLiveTickMsg:
		if m.mode == cockpitModeLive && !m.live.loading {
			return m, m.startCockpitLiveLoad(false)
		}
		return m, nil
	case cockpitTopDetailLoadedMsg:
		if m.mode != cockpitModeTop || !m.top.detailOpen || msg.seq != m.top.detailSeq || msg.request != m.top.detail.request {
			return m, nil
		}
		m.top.detail.loading = false
		m.top.detail.err = msg.err
		if msg.err == nil {
			if msg.content.title != "" {
				m.top.detail.title = msg.content.title
			}
			m.top.detail.lines = msg.content.lines
		}
		m.clampTopDetailOffset()
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
		m.invalidateCockpitTopSnapshot()
		m.statusMsg = formatCockpitMemoryReviewResult(msg.result)
		m.statusErr = ""
		m.mode = cockpitModeLive
		return m, tea.Batch(m.startCockpitHomeAndLiveLoad(), m.startCockpitTopLoad())
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m cockpitModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == cockpitModeMemoryReview {
		return m.updateMemoryReviewKey(msg)
	}
	if m.mode == cockpitModeSettings {
		return m.updateSettingsKey(msg)
	}
	if m.mode == cockpitModeTop && m.cockpitTopDetailOpen() && isCockpitBackKey(msg) {
		m.closeCockpitTopDetail()
		return m, nil
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
	if model, cmd, ok := m.openCockpitLegacyShortcut(msg); ok {
		return model, cmd
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
	case cockpitModeTop:
		return m.updateTopKey(msg)
	default:
		return m.updateLiveKey(msg)
	}
}

func (m cockpitModel) openCockpitLegacyShortcut(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return m, nil, false
	}
	switch strings.ToLower(string(msg.Runes)) {
	case "d":
		if m.cockpitTopDetailOpen() {
			m.closeCockpitTopDetail()
		}
		m.mode = cockpitModeDoctor
		m.doctorOffset = 0
		return m, m.startCockpitDoctorLoad(), true
	case "t", "l":
		next, cmd := m.openCockpitSection(cockpitSectionLive)
		return next, cmd, true
	case "m":
		next, cmd := m.openCockpitSection(cockpitSectionMemory)
		return next, cmd, true
	case "s":
		next, cmd := m.openCockpitSection(cockpitSectionSettings)
		return next, cmd, true
	default:
		return m, nil, false
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
	pressed := string(msg.Runes)
	for _, section := range cockpitNavigationSectionsList() {
		if section.key == pressed {
			return section.id, true
		}
	}
	return 0, false
}

func (m cockpitModel) cockpitAdjacentSectionFromKey(msg tea.KeyMsg) (cockpitSectionID, bool) {
	switch msg.String() {
	case "right", "tab":
		return nextCockpitSection(m.activeCockpitSection(), 1), true
	case "left", "shift+tab":
		return nextCockpitSection(m.activeCockpitSection(), -1), true
	default:
		return 0, false
	}
}

func nextCockpitSection(current cockpitSectionID, delta int) cockpitSectionID {
	index := 0
	found := false
	sections := cockpitNavigationSectionsList()
	for i, section := range sections {
		if section.id == current {
			index = i
			found = true
			break
		}
	}
	if !found {
		// Invalid section ids should still make forward progress when the
		// operator presses tab/arrow keys. Falling back to the first navigation
		// item keeps the cockpit recoverable instead of leaving focus stuck.
		return sections[0].id
	}
	next := (index + delta) % len(sections)
	if next < 0 {
		next += len(sections)
	}
	return sections[next].id
}

func (m cockpitModel) activeCockpitSection() cockpitSectionID {
	switch m.mode {
	case cockpitModeLive, cockpitModeDetail:
		return cockpitSectionLive
	case cockpitModeDoctor:
		return cockpitSectionTop
	case cockpitModeTop:
		return cockpitSectionTop
	case cockpitModeMemoryReview:
		return cockpitSectionMemory
	case cockpitModeSettings:
		return cockpitSectionSettings
	default:
		return cockpitSectionLive
	}
}

func (m cockpitModel) openCockpitSection(section cockpitSectionID) (tea.Model, tea.Cmd) {
	m.showHelp = false
	if section == m.activeCockpitSection() {
		if m.mode == cockpitModeDetail && section == cockpitSectionLive {
			return m.backCockpitSection()
		}
		if m.mode == cockpitModeDoctor && section == cockpitSectionTop {
			m.mode = cockpitModeTop
			if !m.top.loaded && !m.top.loading {
				return m, m.startCockpitTopLoad()
			}
			return m, nil
		}
		return m, nil
	}
	if section != cockpitSectionTop && m.cockpitTopDetailOpen() {
		m.closeCockpitTopDetail()
	}
	switch section {
	case cockpitSectionLive:
		if m.mode == cockpitModeDetail {
			m.detail = topDetailState{}
			m.detailOffset = 0
		}
		m.mode = cockpitModeLive
		m.resumeCockpitLiveFollow()
		return m, m.startCockpitLiveLoad(true)
	case cockpitSectionTop:
		m.mode = cockpitModeTop
		if !m.top.loaded && !m.top.loading {
			return m, m.startCockpitTopLoad()
		}
		return m, nil
	case cockpitSectionMemory:
		m.mode = cockpitModeMemoryReview
		return m, m.startCockpitMemoryReviewLoad()
	case cockpitSectionSettings:
		return m.openCockpitSettings()
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
		return m, m.cockpitLiveTickCmd()
	case cockpitModeLive:
		return m, nil
	default:
		m.mode = cockpitModeLive
		return m, nil
	}
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
			m.mode = cockpitModeLive
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
		m.scrollCockpitLiveStreamBy(-1)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.scrollCockpitLiveStreamBy(1) {
			return m, m.resumeCockpitLiveFollowCmd()
		}
		return m, nil
	case key.Matches(msg, m.keys.PageUp):
		m.scrollCockpitLiveStreamBy(-m.cockpitLiveKeyViewportRows())
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		if m.scrollCockpitLiveStreamBy(m.cockpitLiveKeyViewportRows()) {
			return m, m.resumeCockpitLiveFollowCmd()
		}
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.live.selected = 0
		m.clampLiveSelection()
		if !m.live.isAtNewest() {
			m.live.follow = false
		}
		return m, nil
	case key.Matches(msg, m.keys.End):
		return m, m.resumeCockpitLiveFollowCmd()
	case key.Matches(msg, m.keys.Refresh):
		return m, m.startCockpitLiveLoad(true)
	}
	return m, nil
}

func (m cockpitModel) updateTopKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cockpitTopDetailOpen() {
		switch {
		case key.Matches(msg, m.keys.Up):
			m.top.detailOffset--
			m.clampTopDetailOffset()
			return m, nil
		case key.Matches(msg, m.keys.Down):
			m.top.detailOffset++
			m.clampTopDetailOffset()
			return m, nil
		case key.Matches(msg, m.keys.PageUp):
			m.top.detailOffset -= m.cockpitTopDetailViewportRows()
			m.clampTopDetailOffset()
			return m, nil
		case key.Matches(msg, m.keys.PageDown):
			m.top.detailOffset += m.cockpitTopDetailViewportRows()
			m.clampTopDetailOffset()
			return m, nil
		case key.Matches(msg, m.keys.Home):
			m.top.detailOffset = 0
			return m, nil
		case key.Matches(msg, m.keys.End):
			m.top.detailOffset = max(len(m.cockpitTopDetailLines())-m.cockpitTopDetailViewportRows(), 0)
			return m, nil
		}
		return m, nil
	}
	switch {
	case key.Matches(msg, m.keys.Refresh):
		return m, tea.Batch(m.startCockpitHomeLoad(), m.startCockpitTopLoad())
	case key.Matches(msg, m.keys.Up):
		m.moveCockpitTopSelection(-1)
		return m, nil
	case key.Matches(msg, m.keys.Down):
		m.moveCockpitTopSelection(1)
		return m, nil
	case key.Matches(msg, m.keys.PageUp):
		m.moveCockpitTopSelection(-m.cockpitTopViewportRows())
		return m, nil
	case key.Matches(msg, m.keys.PageDown):
		m.moveCockpitTopSelection(m.cockpitTopViewportRows())
		return m, nil
	case key.Matches(msg, m.keys.Home):
		m.moveCockpitTopSelectionToEdge(1)
		return m, nil
	case key.Matches(msg, m.keys.End):
		m.moveCockpitTopSelectionToEdge(-1)
		return m, nil
	case key.Matches(msg, m.keys.Select):
		return m.openCockpitTopDetail()
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
			m.mode = cockpitModeLive
			m.detail = topDetailState{}
			m.detailOffset = 0
			return m, m.cockpitLiveTickCmd()
		case "t", "l":
			m.mode = cockpitModeLive
			m.detail = topDetailState{}
			m.detailOffset = 0
			return m, m.cockpitLiveTickCmd()
		}
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
		m.mode = cockpitModeLive
		m.memoryReview = cockpitMemoryReviewState{}
		return m, m.startCockpitHomeAndLiveLoad()
	}
	if m.memoryReview.err != nil {
		if key.Matches(msg, actions.Cancel) {
			m.mode = cockpitModeLive
			m.memoryReview = cockpitMemoryReviewState{}
			return m, m.startCockpitHomeAndLiveLoad()
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
			m.mode = cockpitModeLive
			m.memoryReview = cockpitMemoryReviewState{}
			return m, m.startCockpitHomeAndLiveLoad()
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
			m.mode = cockpitModeLive
			m.memoryReview = cockpitMemoryReviewState{}
			return m, m.startCockpitHomeAndLiveLoad()
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
	if section == cockpitSectionLive {
		m.mode = cockpitModeLive
		return m, m.startCockpitHomeAndLiveLoad()
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

func (m *cockpitModel) startCockpitHomeAndLiveLoad() tea.Cmd {
	m.resumeCockpitLiveFollow()
	return tea.Batch(m.startCockpitHomeLoad(), m.startCockpitLiveLoad(true))
}

func (m *cockpitModel) startCockpitTopLoad() tea.Cmd {
	m.top.loading = true
	m.top.err = nil
	m.top.requestSeq++
	return m.fetchCockpitTopCmd(m.top.requestSeq)
}

func (m cockpitModel) fetchCockpitHomeCmd(seq uint64) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	return func() tea.Msg {
		if loader == nil {
			return cockpitHomeMsg{seq: seq, err: xerrors.New(Localize("cockpit home loader is not configured", "cockpit home 用 loader が設定されていません"))}
		}
		home, err := loader.loadCockpitHome(ctx)
		return cockpitHomeMsg{home: home, seq: seq, err: err}
	}
}

func (m cockpitModel) fetchCockpitTopCmd(seq uint64) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	return func() tea.Msg {
		loadedAt := topNowFunc().UTC()
		criteria := m.cockpitTopCriteriaAt(loadedAt)
		if loader == nil {
			return cockpitTopLoadedMsg{seq: seq, criteria: criteria, loadedAt: loadedAt, err: xerrors.New(Localize("cockpit top loader is not configured", "cockpit top 用 loader が設定されていません"))}
		}
		snapshot, err := loader.loadCockpitTop(ctx, criteria)
		return cockpitTopLoadedMsg{snapshot: snapshot, criteria: criteria, loadedAt: loadedAt, seq: seq, err: err}
	}
}

func (m cockpitModel) cockpitTopCriteriaAt(now time.Time) topDataCriteria {
	criteria := topDataCriteria{
		SessionLimit:       defaultTopLimit,
		FailureLimit:       topPaneFailureLimit,
		RecentCommandLimit: topPaneRecentCommandLimit,
		CandidateLimit:     topPaneCandidateLimit,
		StaleMemoryLimit:   topPaneStaleMemoryLimit,
		StaleAfter:         defaultActiveSessionStaleAfter,
		Now:                now,
	}
	if m.home.NewCandidateMemoryKnown {
		criteria.MemoryLastSeenAt = domtypes.Some(m.home.MemoryLastSeenAt)
	}
	return criteria
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
			return cockpitDoctorLoadedMsg{seq: seq, err: xerrors.New(Localize("cockpit doctor loader is not configured", "cockpit doctor 用 loader が設定されていません"))}
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
			return cockpitMemoryReviewLoadedMsg{seq: seq, seenAt: seenAt, err: xerrors.New(Localize("cockpit memory review loader is not configured", "メモリ確認用 loader が設定されていません"))}
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
			return cockpitMemoryReviewAppliedMsg{err: xerrors.New(Localize("cockpit memory review loader is not configured", "メモリ確認用 loader が設定されていません"))}
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
			return cockpitLiveMsg{initial: initial, seq: seq, err: xerrors.New(Localize("cockpit live loader is not configured", "cockpit live 用 loader が設定されていません"))}
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

func (m cockpitModel) openCockpitTopDetail() (tea.Model, tea.Cmd) {
	rows := m.cockpitTopRows()
	if len(rows) == 0 {
		return m, nil
	}
	m.clampTopSelection()
	if m.top.selected < 0 || m.top.selected >= len(rows) {
		return m, nil
	}
	row := rows[m.top.selected]
	if !row.selectable || row.target.kind == topDetailNone {
		return m, nil
	}
	req := topDetailRequest{pane: row.pane, target: row.target}
	m.top.detailSeq++
	m.top.detailOffset = 0
	m.top.detailOpen = true
	m.top.detail = topDetailState{
		request: req,
		title:   row.target.title,
		lines:   []string{Localize("Loading...", "読み込み中...")},
		loading: true,
	}
	return m, m.fetchCockpitTopDetailCmd(req, m.top.detailSeq)
}

func (m cockpitModel) fetchCockpitTopDetailCmd(req topDetailRequest, seq uint64) tea.Cmd {
	loader := m.loader
	ctx := m.loaderCtx
	return func() tea.Msg {
		if loader == nil {
			return cockpitTopDetailLoadedMsg{request: req, seq: seq, err: xerrors.New(Localize("cockpit sessions detail loader is not configured", "cockpit sessions detail 用 loader が設定されていません"))}
		}
		content, err := loader.loadCockpitTopDetail(ctx, req)
		return cockpitTopDetailLoadedMsg{request: req, content: content, seq: seq, err: err}
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

func (m *cockpitModel) clampTopSelection() {
	rows := m.cockpitTopRows()
	if len(rows) == 0 {
		m.top.selected = 0
		m.top.offset = 0
		return
	}
	if m.top.selected < 0 {
		m.top.selected = 0
	}
	if m.top.selected >= len(rows) {
		m.top.selected = len(rows) - 1
	}
	if !rows[m.top.selected].selectable {
		if next, ok := cockpitTopSelectableAtOrAfter(rows, m.top.selected); ok {
			m.top.selected = next
		} else if previous, ok := cockpitTopSelectableAtOrBefore(rows, m.top.selected); ok {
			m.top.selected = previous
		}
	}
	m.ensureCockpitTopSelectionVisible(rows)
}

func (m *cockpitModel) rebuildCockpitTopRows() {
	if !m.top.loaded {
		m.top.rows = nil
		return
	}
	width := m.cockpitTopRowWidth()
	criteria := m.top.criteria
	if criteria.Now.IsZero() {
		criteria = m.cockpitTopCriteriaAt(m.top.loadedAt)
	}
	m.top.rows = buildCockpitTopRows(m.top.snapshot, criteria, m.top.loadedAt, width, m.styles)
}

func (m *cockpitModel) invalidateCockpitTopSnapshot() {
	m.top.requestSeq++
	m.top.snapshot = topDataSnapshot{}
	m.top.criteria = topDataCriteria{}
	m.top.loadedAt = time.Time{}
	m.top.loaded = false
	m.top.loading = false
	m.top.err = nil
	m.top.rows = nil
	m.top.selected = 0
	m.top.offset = 0
	m.closeCockpitTopDetail()
}

func cockpitTopSelectableAtOrAfter(rows []cockpitTopRow, start int) (int, bool) {
	if start < 0 {
		start = 0
	}
	for i := start; i < len(rows); i++ {
		if rows[i].selectable {
			return i, true
		}
	}
	return 0, false
}

func cockpitTopSelectableAtOrBefore(rows []cockpitTopRow, start int) (int, bool) {
	if start >= len(rows) {
		start = len(rows) - 1
	}
	for i := start; i >= 0; i-- {
		if rows[i].selectable {
			return i, true
		}
	}
	return 0, false
}

func (m *cockpitModel) moveCockpitTopSelection(delta int) {
	rows := m.cockpitTopRows()
	if len(rows) == 0 || delta == 0 {
		return
	}
	if !cockpitTopHasSelectableRow(rows) {
		m.scrollCockpitTopRows(delta)
		return
	}
	m.clampTopSelection()
	next := m.top.selected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(rows) {
		next = len(rows) - 1
	}
	if !rows[next].selectable {
		if delta > 0 {
			if selectable, ok := cockpitTopSelectableAtOrAfter(rows, next); ok {
				next = selectable
			} else if selectable, ok := cockpitTopSelectableAtOrBefore(rows, len(rows)-1); ok {
				next = selectable
			}
		} else if selectable, ok := cockpitTopSelectableAtOrBefore(rows, next); ok {
			next = selectable
		} else if selectable, ok := cockpitTopSelectableAtOrAfter(rows, 0); ok {
			next = selectable
		}
	}
	m.top.selected = next
	m.ensureCockpitTopSelectionVisible(rows)
}

func (m *cockpitModel) moveCockpitTopSelectionToEdge(direction int) {
	rows := m.cockpitTopRows()
	if len(rows) == 0 {
		return
	}
	if !cockpitTopHasSelectableRow(rows) {
		if direction >= 0 {
			m.top.offset = 0
		} else {
			m.top.offset = max(len(rows)-m.cockpitTopViewportRows(), 0)
		}
		return
	}
	if direction >= 0 {
		if next, ok := cockpitTopSelectableAtOrAfter(rows, 0); ok {
			m.top.selected = next
		}
	} else if next, ok := cockpitTopSelectableAtOrBefore(rows, len(rows)-1); ok {
		m.top.selected = next
	}
	m.ensureCockpitTopSelectionVisible(rows)
}

func (m *cockpitModel) scrollCockpitTopRows(delta int) {
	rows := m.cockpitTopRows()
	if len(rows) == 0 {
		m.top.offset = 0
		return
	}
	viewport := m.cockpitTopViewportRows()
	maxOffset := max(len(rows)-viewport, 0)
	m.top.offset += delta
	if m.top.offset < 0 {
		m.top.offset = 0
	}
	if m.top.offset > maxOffset {
		m.top.offset = maxOffset
	}
}

func cockpitTopHasSelectableRow(rows []cockpitTopRow) bool {
	for _, row := range rows {
		if row.selectable {
			return true
		}
	}
	return false
}

func (m *cockpitModel) ensureCockpitTopSelectionVisible(rows []cockpitTopRow) {
	viewport := m.cockpitTopViewportRows()
	if viewport <= 0 {
		viewport = 1
	}
	maxOffset := max(len(rows)-viewport, 0)
	if m.top.offset > maxOffset {
		m.top.offset = maxOffset
	}
	if m.top.offset < 0 {
		m.top.offset = 0
	}
	if m.top.selected < m.top.offset {
		m.top.offset = m.top.selected
	}
	if m.top.selected >= m.top.offset+viewport {
		m.top.offset = m.top.selected - viewport + 1
	}
	if m.top.offset > maxOffset {
		m.top.offset = maxOffset
	}
	if m.top.offset < 0 {
		m.top.offset = 0
	}
}

func (m *cockpitModel) trimCockpitLiveEventsToLimit() {
	before := len(m.live.events)
	overflow := len(m.live.events) - cockpitLiveMaxEvents
	if overflow <= 0 {
		return
	}
	if m.live.follow {
		m.live.events = m.live.events[overflow:]
		m.pruneCockpitLiveExtras()
		return
	}
	m.clampLiveSelection()
	for overflow > 0 && len(m.live.events) > cockpitLiveMaxEvents {
		switch {
		case m.live.selected > 0:
			m.live.events = m.live.events[1:]
			m.live.selected--
		case len(m.live.events) > 1:
			m.live.events = append(m.live.events[:1], m.live.events[2:]...)
		default:
			return
		}
		overflow--
	}
	if len(m.live.events) != before {
		m.pruneCockpitLiveExtras()
	}
}

func cloneCockpitLiveExtras(in map[domtypes.EventID]compactRowExtras) map[domtypes.EventID]compactRowExtras {
	if len(in) == 0 {
		return nil
	}
	out := make(map[domtypes.EventID]compactRowExtras, len(in))
	for id, extras := range in {
		out[id] = extras
	}
	return out
}

func (m *cockpitModel) mergeCockpitLiveExtras(in map[domtypes.EventID]compactRowExtras) {
	if len(in) == 0 {
		return
	}
	if m.live.extras == nil {
		m.live.extras = make(map[domtypes.EventID]compactRowExtras, len(in))
	}
	for id, extras := range in {
		m.live.extras[id] = extras
	}
}

func (m *cockpitModel) pruneCockpitLiveExtras() {
	if len(m.live.extras) == 0 {
		return
	}
	visible := make(map[domtypes.EventID]struct{}, len(m.live.events))
	for _, event := range m.live.events {
		if event != nil {
			visible[event.EventID()] = struct{}{}
		}
	}
	for id := range m.live.extras {
		if _, ok := visible[id]; !ok {
			delete(m.live.extras, id)
		}
	}
	if len(m.live.extras) == 0 {
		m.live.extras = nil
	}
}

func (m *cockpitModel) scrollCockpitLiveStreamBy(delta int) (resumeFollow bool) {
	if len(m.live.events) == 0 || delta == 0 {
		return false
	}
	viewport := m.cockpitLiveKeyViewportRows()
	start, _ := m.cockpitLiveVisibleRange(viewport)
	maxStart := max(len(m.live.events)-viewport, 0)
	next := start + delta
	if next <= 0 {
		m.live.selected = 0
		m.live.follow = false
		return false
	}
	if next >= maxStart {
		m.live.selected = maxStart
		if delta > 0 {
			return !m.live.follow
		}
		m.live.follow = false
		return false
	}
	m.live.selected = next
	m.live.follow = false
	return false
}

func (m *cockpitModel) moveCockpitLiveSelectionToNewest() {
	if len(m.live.events) == 0 {
		m.live.selected = 0
		return
	}
	m.live.selected = len(m.live.events) - 1
}

func (s cockpitLiveState) isAtNewest() bool {
	return len(s.events) == 0 || s.selected >= len(s.events)-1
}

func (m *cockpitModel) resumeCockpitLiveFollow() {
	m.live.follow = true
	m.live.pausedNewCount = 0
	m.moveCockpitLiveSelectionToNewest()
}

func (m *cockpitModel) resumeCockpitLiveFollowCmd() tea.Cmd {
	m.resumeCockpitLiveFollow()
	return tea.Batch(m.markCurrentCockpitLiveSeenCmd(), m.cockpitLiveTickCmd())
}

func (m cockpitModel) markCurrentCockpitLiveSeenCmd() tea.Cmd {
	if len(m.live.events) == 0 {
		return nil
	}
	return m.markCockpitEventsSeenCmd(cockpitLiveSnapshot{
		Cursor:   m.live.cursor,
		LoadedAt: m.live.loadedAt,
	})
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

func (m *cockpitModel) clampTopDetailOffset() {
	maxOffset := len(m.cockpitTopDetailLines()) - m.cockpitTopDetailViewportRows()
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.top.detailOffset < 0 {
		m.top.detailOffset = 0
	}
	if m.top.detailOffset > maxOffset {
		m.top.detailOffset = maxOffset
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
	case cockpitModeTop:
		return m.topTabView()
	case cockpitModeDetail:
		return m.detailView()
	case cockpitModeMemoryReview:
		return m.memoryReviewView()
	case cockpitModeSettings:
		return m.settingsView()
	}
	return m.liveView()
}

func formatCockpitNewCandidateCount(home cockpitHomeSnapshot) string {
	if !home.NewCandidateMemoryKnown {
		return Localize("untracked", "未追跡")
	}
	return fmt.Sprintf("%d", home.NewCandidateMemoryCount)
}

func formatCockpitNewEventCount(home cockpitHomeSnapshot) string {
	if !home.NewEventKnown {
		return Localize("untracked", "未追跡")
	}
	return fmt.Sprintf("%d", home.NewEventCount)
}

func formatCockpitMemoryReviewResult(result memoryInboxReviewResult) string {
	return Localizef("memory review applied: accepted=%d rejected=%d distilled=%d failures=%d", "メモリ確認 適用済み: accepted=%d rejected=%d distilled=%d failures=%d", len(result.Accepted), len(result.Rejected), len(result.Distilled), len(result.Failures))
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
	return m.renderCockpitShell(Localize("doctor", "ドクター"), lines, m.doctorLocalHelp())
}

func (m cockpitModel) doctorLines() []string {
	if m.doctor.loading {
		return []string{m.styles.Subtle.Render(Localize("Loading doctor checks...", "Doctor check を読み込み中..."))}
	}
	if m.doctor.err != nil {
		return []string{m.styles.Error.Render(m.doctor.err.Error())}
	}
	snapshot := m.doctor.snapshot
	lines := []string{
		m.styles.Subtle.Render(fmt.Sprintf("loaded=%s db=%s", formatJSONTime(snapshot.LoadedAt), formatOptionalColumn(snapshot.DBPath))),
		Localizef("summary: pass=%d warn=%d fail=%d", "summary: pass=%d warn=%d fail=%d", snapshot.Summary.Pass, snapshot.Summary.Warn, snapshot.Summary.Fail),
		"",
	}
	if len(snapshot.Sections) == 0 {
		return append(lines,
			m.styles.Success.Render(Localize("No doctor checks reported.", "Doctor check は報告されていません。")),
			m.styles.Subtle.Render(Localize("Press r to refresh checks, 1 to return Tail, or 2 to return Sessions.", "r で check を再取得、1 で Tail、2 で Sessions へ戻ります。")),
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
				lines = append(lines, Localize("  hint: ", "  ヒント: ")+truncateNormalized(check.Hint, cockpitDoctorMessageWidth))
			}
			if check.FixCommand != "" {
				lines = append(lines, Localize("  fix: ", "  修復: ")+check.FixCommand)
			} else if check.AutoFixAvailable {
				lines = append(lines, Localize("  fix: ", "  修復: ")+"traceary doctor --fix --dry-run")
			}
		}
		lines = append(lines, "")
	}
	if rendered == 0 {
		lines = append(lines,
			m.styles.Success.Render(Localize("No doctor checks reported.", "Doctor check は報告されていません。")),
			m.styles.Subtle.Render(Localize("Press r to refresh checks, 1 to return Tail, or 2 to return Sessions.", "r で check を再取得、1 で Tail、2 で Sessions へ戻ります。")),
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
		lines = append(lines, m.styles.Subtle.Render(Localize("Loading memory review queue...", "メモリ候補の確認キューを読み込み中...")))
	case m.memoryReview.applying:
		lines = append(lines, m.styles.Subtle.Render(Localize("Applying memory review decisions...", "メモリ確認の判断を適用中...")))
	case m.memoryReview.err != nil:
		lines = append(lines, m.styles.Error.Render(m.memoryReview.err.Error()))
	default:
		lines = append(lines, m.memoryReview.review.View())
	}
	return m.renderCockpitShell(memoryReviewWorkflowLabel(), lines, m.memoryReviewLocalHelp())
}

func (m cockpitModel) liveView() string {
	lines := []string{
		m.styles.Subtle.Render(fmt.Sprintf("loaded=%s auto_follow=%t rows=%d", formatJSONTime(m.live.loadedAt), m.live.follow, len(m.live.events))),
		"",
	}
	if m.statusMsg != "" {
		lines = append(lines, m.styles.Success.Render("• "+m.statusMsg), "")
	}
	if m.statusErr != "" {
		lines = append(lines, m.styles.Error.Render("• "+m.statusErr), "")
	}
	if pauseMessage := m.livePauseMessage(); pauseMessage != "" {
		lines = append(lines, pauseMessage, "")
	}
	if m.live.err != nil {
		lines = append(lines, m.styles.Error.Render(m.live.err.Error()))
	} else if len(m.live.events) == 0 {
		if m.live.loading {
			lines = append(lines, m.styles.Subtle.Render(Localize("Loading live events...", "ライブイベントを読み込み中...")))
		} else {
			lines = append(lines, m.styles.Subtle.Render(Localize("No recent events. Press r to refresh.", "最近のイベントはありません。r で再取得します。")))
		}
	} else {
		viewport := m.cockpitLiveViewportRows(len(lines))
		if viewport > cockpitLiveMinViewportRows && m.cockpitLiveScrollLine(viewport) != "" {
			viewport = m.cockpitLiveViewportRows(len(lines) + 1)
			lines = append(lines, m.cockpitLiveScrollLine(viewport))
		}
		start, end := m.cockpitLiveVisibleRange(viewport)
		for _, event := range m.live.events[start:end] {
			lines = append(lines, m.formatCockpitLiveEventRow(event, time.Local))
		}
	}
	return m.renderCockpitShell(Localize("live tail", "live tail"), lines, m.liveLocalHelp())
}

func (m cockpitModel) cockpitLiveViewportRows(preludeRows int) int {
	if m.height <= 0 {
		return cockpitLiveDefaultViewportRows
	}
	rows := m.height - cockpitShellChromeRows - preludeRows
	if rows < cockpitLiveMinViewportRows {
		return cockpitLiveMinViewportRows
	}
	return rows
}

func (m cockpitModel) cockpitLiveKeyViewportRows() int {
	return m.cockpitLiveViewportRows(cockpitLiveBasePreludeRows)
}

func (m cockpitModel) cockpitLiveRowWidth() int {
	if m.width <= 0 {
		return 0
	}
	return max(m.width-1, 1)
}

func (m cockpitModel) cockpitLiveVisibleRange(viewport int) (int, int) {
	if viewport < 1 {
		viewport = 1
	}
	total := len(m.live.events)
	if total == 0 {
		return 0, 0
	}
	if viewport >= total {
		return 0, total
	}
	if m.live.follow {
		return total - viewport, total
	}
	start := m.live.selected
	if start < 0 {
		start = 0
	}
	maxStart := total - viewport
	if start > maxStart {
		start = maxStart
	}
	return start, start + viewport
}

func (m cockpitModel) cockpitLiveScrollLine(viewport int) string {
	total := len(m.live.events)
	if total == 0 || viewport >= total {
		return ""
	}
	start, end := m.cockpitLiveVisibleRange(viewport)
	return m.styles.Subtle.Render(fmt.Sprintf("rows=%d-%d/%d", start+1, end, total))
}

func (m cockpitModel) livePauseMessage() string {
	if m.live.follow {
		return ""
	}
	if m.live.pausedNewCount > 0 {
		return m.styles.Warning.Render(formatCockpitPausedNewEvents(m.live.pausedNewCount))
	}
	return m.styles.Subtle.Render(Localize("Auto-follow paused · press End/G for newest", "auto-follow 停止中 · End/G で最新へ"))
}

func formatCockpitPausedNewEvents(count int) string {
	if count == 1 {
		return Localize("Auto-follow paused · 1 newer event available · press End/G for newest", "auto-follow 停止中 · 新しいイベント 1 件 · End/G で最新へ")
	}
	return Localizef("Auto-follow paused · %d newer events available · press End/G for newest", "auto-follow 停止中 · 新しいイベント %d 件 · End/G で最新へ", count)
}

func (m cockpitModel) detailView() string {
	lines := []string{}
	if m.detail.err != nil {
		lines = append(lines, m.styles.Error.Render(m.detail.err.Error()))
	} else {
		detailLines := m.detailLines()
		if len(detailLines) == 0 {
			detailLines = []string{m.styles.Subtle.Render(Localize("No detail lines.", "詳細行はありません。"))}
		}
		lines = append(lines, detailLines[m.detailOffset:]...)
	}
	return m.renderCockpitShell(m.detail.title, lines, m.detailLocalHelp())
}

func (m cockpitModel) topSignals() []cockpitTopSignal {
	home := m.home
	signals := []cockpitTopSignal{}
	if home.DoctorError != "" {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalFailure,
			label:       Localize("Doctor unavailable", "Doctor を利用できません"),
			description: home.DoctorError,
			actionKey:   "d",
			actionLabel: Localize("open Doctor checks", "Doctor check を開く"),
		})
	} else if home.DoctorFailCount > 0 || home.HookFailCount > 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalFailure,
			label:       Localize("Health failures", "Health failure"),
			description: Localizef("doctor_fail=%d hook_fail=%d", "doctor_fail=%d hook_fail=%d", home.DoctorFailCount, home.HookFailCount),
			actionKey:   "d",
			actionLabel: Localize("open Doctor checks", "Doctor check を開く"),
		})
	} else if home.DoctorWarnCount > 0 || home.HookWarnCount > 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalWarning,
			label:       Localize("Health warnings", "Health warning"),
			description: Localizef("doctor_warn=%d hook_warn=%d", "doctor_warn=%d hook_warn=%d", home.DoctorWarnCount, home.HookWarnCount),
			actionKey:   "d",
			actionLabel: Localize("open Doctor checks", "Doctor check を開く"),
		})
	}
	if home.CandidateMemoryCount > 0 || home.NewCandidateMemoryCount > 0 || home.RememberIntentCount > 0 || home.LowQualityMemoryCount > 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalWarning,
			label:       Localize("Memory review queue needs attention", "メモリ候補の確認が必要"),
			description: Localizef("candidate=%d new=%s remember-intent=%d low-quality=%d", "candidate=%d new=%s remember-intent=%d low-quality=%d", home.CandidateMemoryCount, formatCockpitNewCandidateCount(home), home.RememberIntentCount, home.LowQualityMemoryCount),
			actionKey:   "3",
			actionLabel: Localize("open Memory review", "メモリ確認を開く"),
		})
	}
	if home.RecentFailureCount > 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalWarning,
			label:       Localize("Recent command failures", "直近のコマンド失敗"),
			description: Localizef("recent_failures=%d", "recent_failures=%d", home.RecentFailureCount),
			actionKey:   "1",
			actionLabel: Localize("inspect Tail events", "Tail のイベントを確認"),
		})
	}
	if home.StaleActiveSessionCount > 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalWarning,
			label:       Localize("Stale active sessions", "古いアクティブセッション"),
			description: Localizef("stale_active=%d", "stale_active=%d", home.StaleActiveSessionCount),
			actionKey:   "2",
			actionLabel: Localize("open Sessions", "セッションを開く"),
		})
	}
	if home.NewEventKnown && home.NewEventCount > 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalInfo,
			label:       Localize("New Tail events", "Tail の新着イベント"),
			description: Localizef("new_events=%d", "new_events=%d", home.NewEventCount),
			actionKey:   "1",
			actionLabel: Localize("open Tail", "Tail を開く"),
		})
	}
	if home.LargePayloadCount > 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalWarning,
			label:       Localize("Large payloads", "大きなペイロード"),
			description: Localizef("large_payloads=%d", "large_payloads=%d", home.LargePayloadCount),
			actionKey:   "2",
			actionLabel: Localize("refresh Sessions summary", "Sessions 概要を再取得"),
		})
	}
	if len(signals) == 0 {
		signals = append(signals, cockpitTopSignal{
			severity:    cockpitSignalOK,
			label:       Localize("No active signals", "対応が必要な通知なし"),
			description: Localize("Tail and Sessions summaries have no review cues.", "Tail と Sessions 概要に確認が必要な項目はありません。"),
		})
	}
	return signals
}

func (m cockpitModel) renderTopSignal(signal cockpitTopSignal) string {
	label := fmt.Sprintf("• [%s] %s — %s", signal.severity, signal.label, signal.description)
	if signal.actionKey != "" {
		label += fmt.Sprintf(" (%s %s)", signal.actionKey, signal.actionLabel)
	}
	switch signal.severity {
	case cockpitSignalFailure:
		return m.styles.Error.Render(label)
	case cockpitSignalWarning:
		return m.styles.Warning.Render(label)
	case cockpitSignalOK:
		return m.styles.Success.Render(label)
	default:
		return label
	}
}

func (m cockpitModel) cockpitTopDetailOpen() bool {
	return m.top.detailOpen
}

func (m *cockpitModel) closeCockpitTopDetail() {
	m.top.detail = topDetailState{}
	m.top.detailOpen = false
	m.top.detailSeq++
	m.top.detailOffset = 0
}

func (m cockpitModel) topTabView() string {
	if m.cockpitTopDetailOpen() {
		return m.cockpitTopDetailView()
	}
	memoryScanSuffix := ""
	if m.home.MemoryScanLimited {
		memoryScanSuffix = " scan_limited=true"
	}
	eventScanSuffix := ""
	if m.home.NewEventScanLimited {
		eventScanSuffix = " scan_limited=true"
	}
	// Keep metric identifiers in English for copy/paste parity with CLI output, docs, and issue search.
	lines := []string{
		m.styles.Subtle.Render(fmt.Sprintf("loaded=%s db=%s", formatJSONTime(m.home.LoadedAt), formatOptionalColumn(m.home.DBPath))),
		"",
		m.styles.Subtle.Render(Localize("Sessions summary", "Sessions 概要")),
		Localizef("• sessions: stale_active=%d recent_failures=%d recent_commands=%d new_events=%s%s", "• セッション: stale_active=%d recent_failures=%d recent_commands=%d new_events=%s%s", m.home.StaleActiveSessionCount, m.home.RecentFailureCount, m.home.RecentCommandCount, formatCockpitNewEventCount(m.home), eventScanSuffix),
		Localizef("• memories: accepted(reviewed)=%d candidate(inbox)=%d new=%s remember-intent=%d low-quality=%d stale=%d%s", "• メモリ: accepted(reviewed)=%d candidate(inbox)=%d new=%s remember-intent=%d low-quality=%d stale=%d%s", m.home.AcceptedMemoryCount, m.home.CandidateMemoryCount, formatCockpitNewCandidateCount(m.home), m.home.RememberIntentCount, m.home.LowQualityMemoryCount, m.home.StaleMemoryCount, memoryScanSuffix),
		Localizef("• doctor: pass=%d warn=%d fail=%d", "• doctor: pass=%d warn=%d fail=%d", m.home.DoctorPassCount, m.home.DoctorWarnCount, m.home.DoctorFailCount),
		Localizef("• hooks/mcp: warn=%d fail=%d", "• hooks/mcp: warn=%d fail=%d", m.home.HookWarnCount, m.home.HookFailCount),
		Localizef("• payloads: large=%d", "• payloads: large=%d", m.home.LargePayloadCount),
		"",
		m.styles.Subtle.Render(Localize("Actionable signals", "対応が必要な通知")),
	}
	for _, signal := range m.topSignals() {
		lines = append(lines, m.renderTopSignal(signal))
	}
	lines = append(lines,
		"",
		m.styles.Subtle.Render(Localize("Signal details", "通知の詳細")),
	)
	switch {
	case m.home.NewEventKnown && m.home.NewEventCount > 0:
		lines = append(lines, Localizef("• new events=%d since %s", "• 新着イベント=%d（%s 以降）", m.home.NewEventCount, formatCockpitCheckpoint(m.home.EventLastSeenAt)))
	case m.home.NewEventKnown:
		lines = append(lines, Localizef("• no unseen events since %s", "• %s 以降の未確認イベントはありません", formatCockpitCheckpoint(m.home.EventLastSeenAt)))
	default:
		lines = append(lines, Localize("• new events=untracked until cockpit state is initialized", "• cockpit state 初期化まで新着イベントは未追跡"))
	}
	switch {
	case m.home.NewCandidateMemoryKnown && m.home.NewCandidateMemoryCount > 0:
		lines = append(lines, Localizef("• new memory candidates=%d", "• 新着メモリ候補=%d", m.home.NewCandidateMemoryCount))
	case m.home.NewCandidateMemoryKnown:
		lines = append(lines, Localizef("• no unseen candidates since %s", "• %s 以降の未確認メモリ候補はありません", formatCockpitCheckpoint(m.home.MemoryLastSeenAt)))
	default:
		lines = append(lines, Localize("• memory candidate new count=untracked", "• メモリ候補の新着数=未追跡"))
	}
	lines = append(lines, Localizef("• memory candidates=%d", "• メモリ候補=%d", m.home.CandidateMemoryCount))
	if m.home.RememberIntentCount > 0 {
		lines = append(lines, Localizef("• remember-intent candidates=%d", "• remember-intent メモリ候補=%d", m.home.RememberIntentCount))
	}
	if m.home.LowQualityMemoryCount > 0 {
		lines = append(lines, Localizef("• low-quality candidates=%d", "• 低品質メモリ候補=%d", m.home.LowQualityMemoryCount))
	}
	if m.home.RecentFailureCount > 0 {
		lines = append(lines, Localizef("• recent failures=%d", "• 最近の失敗=%d", m.home.RecentFailureCount))
	}
	if m.home.StaleActiveSessionCount > 0 {
		lines = append(lines, Localizef("• stale active sessions=%d", "• 古いアクティブセッション=%d", m.home.StaleActiveSessionCount))
	}
	lines = append(lines,
		"",
	)
	lines = append(lines, m.cockpitTopDashboardLines()...)
	if m.statusMsg != "" {
		lines = append([]string{m.styles.Success.Render("• " + m.statusMsg), ""}, lines...)
	}
	if m.statusErr != "" {
		lines = append([]string{m.styles.Error.Render("• " + m.statusErr), ""}, lines...)
	}
	return m.renderCockpitShell(Localize("sessions", "セッション"), lines, m.topLocalHelp())
}

func (m cockpitModel) cockpitTopDashboardLines() []string {
	lines := []string{m.styles.Subtle.Render(Localize("Sessions dashboard", "Sessions ダッシュボード"))}
	switch {
	case m.top.loading && !m.top.loaded:
		return append(lines, m.styles.Subtle.Render(Localize("Loading Sessions dashboard...", "Sessions ダッシュボードを読み込み中...")))
	case m.top.err != nil && !m.top.loaded:
		return append(lines, m.styles.Error.Render(m.top.err.Error()))
	case !m.top.loaded:
		return append(lines, m.styles.Subtle.Render(Localize("Sessions dashboard has not loaded yet. Press r to refresh.", "Sessions ダッシュボードはまだ読み込まれていません。r で再取得します。")))
	}
	if m.top.err != nil {
		lines = append(lines, m.styles.Error.Render(m.top.err.Error()))
	}

	rows := m.cockpitTopRows()
	if len(rows) == 0 {
		return append(lines, m.styles.Subtle.Render(Localize("No session dashboard rows available.", "Sessions ダッシュボード行はありません。")))
	}
	viewport := m.cockpitTopViewportRows()
	start := m.top.offset
	if start < 0 {
		start = 0
	}
	if start > len(rows) {
		start = len(rows)
	}
	end := start + viewport
	if end > len(rows) {
		end = len(rows)
	}
	scroll := ""
	if len(rows) > viewport {
		scroll = fmt.Sprintf(" rows=%d-%d/%d", start+1, end, len(rows))
	} else {
		scroll = fmt.Sprintf(" rows=%d", len(rows))
	}
	lines = append(lines, m.styles.Subtle.Render(fmt.Sprintf("loaded=%s%s", formatJSONTime(m.top.loadedAt), scroll)))
	for i := start; i < end; i++ {
		row := rows[i]
		prefix := "  "
		if row.header {
			prefix = ""
		}
		line := prefix + row.line
		if row.selectable && i == m.top.selected {
			line = m.styles.Active.Render("> ") + row.line
		}
		lines = append(lines, line)
	}
	return lines
}

func (m cockpitModel) cockpitTopRows() []cockpitTopRow {
	if !m.top.loaded {
		return nil
	}
	return m.top.rows
}

func buildCockpitTopRows(snapshot topDataSnapshot, criteria topDataCriteria, loadedAt time.Time, width int, styles tui.Styles) []cockpitTopRow {
	renderer := newCockpitTopRowRenderer(snapshot, criteria, loadedAt, width, styles)
	rows := make([]cockpitTopRow, 0)
	for _, pane := range []topPane{topPaneSessions, topPaneFailures, topPaneRecentCommands, topPaneCandidates, topPaneStaleMemories} {
		rows = append(rows, cockpitTopRow{
			line:   styles.Subtle.Render(cockpitTopSectionLabel(pane, snapshot)),
			pane:   pane,
			header: true,
		})
		for _, paneRow := range renderer.paneRows(pane, width) {
			rows = append(rows, cockpitTopRow{
				line:       paneRow.line,
				pane:       pane,
				target:     paneRow.target,
				selectable: paneRow.target.kind != topDetailNone,
			})
		}
	}
	return rows
}

func newCockpitTopRowRenderer(snapshot topDataSnapshot, criteria topDataCriteria, loadedAt time.Time, width int, styles tui.Styles) topModel {
	renderNow := criteria.Now
	if renderNow.IsZero() {
		renderNow = snapshot.Now
	}
	if renderNow.IsZero() {
		renderNow = loadedAt
	}
	renderer := newTopModel(topModelConfig{
		Keys:            tui.DefaultKeyMap(),
		Actions:         defaultTopPaneActionKeys(),
		Styles:          styles,
		Criteria:        criteria,
		Idle:            defaultActiveSessionStaleAfter,
		Now:             func() time.Time { return renderNow },
		Location:        time.Local,
		RefreshInterval: 0,
	})
	renderer.snapshot = snapshot
	renderer.loadedAt = loadedAt
	renderer.loaded = true
	renderer.width = width
	return renderer
}

func cockpitTopSectionLabel(pane topPane, snapshot topDataSnapshot) string {
	switch pane {
	case topPaneSessions:
		return Localizef("SESSIONS (%d)", "SESSIONS (%d)", countCockpitTopSessionRows(snapshot.Sessions))
	case topPaneFailures:
		return Localizef("RECENT FAILURES (%d)", "RECENT FAILURES (%d)", len(snapshot.Failures))
	case topPaneRecentCommands:
		return Localizef("RECENT COMMANDS (%d)", "RECENT COMMANDS (%d)", len(snapshot.RecentCommands))
	case topPaneCandidates:
		// The live cockpit uses the operator-facing glossary. Keep
		// top --snapshot's historical CANDIDATE MEMORIES text header separate
		// for script compatibility; see writeTopSnapshotTextCandidates.
		return Localizef("MEMORY CANDIDATES (%d)", "MEMORY CANDIDATES (%d)", len(snapshot.Candidates))
	case topPaneStaleMemories:
		return Localizef("STALE MEMORIES (%d)", "STALE MEMORIES (%d)", snapshot.StaleMemories.Count())
	default:
		return ""
	}
}

func countCockpitTopSessionRows(nodes []*sessionNode) int {
	count := 0
	for _, node := range nodes {
		if node == nil {
			continue
		}
		count++
		count += countCockpitTopSessionRows(node.children)
	}
	return count
}

func (m cockpitModel) cockpitTopRowWidth() int {
	if m.width <= 0 {
		return cockpitTopUnknownWidth
	}
	width := m.width - cockpitTopShellHorizontalPadding - cockpitTopRowPrefixWidth
	if width < 1 {
		return 1
	}
	return width
}

func (m cockpitModel) cockpitTopViewportRows() int {
	if m.height <= 0 {
		return cockpitTopDefaultViewportRows
	}
	rows := m.height - cockpitTopSummaryChromeRows
	if rows < cockpitTopMinViewportRows {
		return cockpitTopMinViewportRows
	}
	return rows
}

func (m cockpitModel) cockpitTopDetailView() string {
	title := m.top.detail.title
	if title == "" {
		title = Localize("sessions detail", "Sessions detail")
	}
	lines := m.cockpitTopDetailLines()
	if len(lines) == 0 {
		lines = []string{m.styles.Subtle.Render(Localize("(empty)", "(空)"))}
	}
	viewport := m.cockpitTopDetailViewportRows()
	start := m.top.detailOffset
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		start = len(lines)
	}
	end := start + viewport
	if end > len(lines) {
		end = len(lines)
	}
	body := append([]string{}, lines[start:end]...)
	if len(lines) > viewport {
		body = append([]string{m.styles.Subtle.Render(fmt.Sprintf("rows=%d-%d/%d", start+1, end, len(lines)))}, body...)
	}
	return m.renderCockpitShell(Localize("sessions detail · ", "Sessions detail · ")+title, body, m.topLocalHelp())
}

func (m cockpitModel) cockpitTopDetailLines() []string {
	if m.top.detail.loading {
		return []string{Localize("Loading...", "読み込み中...")}
	}
	if m.top.detail.err != nil {
		return []string{m.styles.Error.Render(m.top.detail.err.Error())}
	}
	return m.top.detail.lines
}

func (m cockpitModel) cockpitTopDetailViewportRows() int {
	if m.height <= 0 {
		return cockpitTopDetailDefaultViewportRows
	}
	rows := m.height - cockpitTopDetailChromeRows
	if rows < 1 {
		return 1
	}
	return rows
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
	sections := cockpitNavigationSectionsList()
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		label := section.prefix()
		if section.id == active {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	return Localize("tabs: ", "タブ: ") + strings.Join(parts, "  ")
}

func (m cockpitModel) cockpitGlobalFooter(localHelp string) string {
	quitHelp := Localize("q/ctrl+c quit", "q/ctrl+c 終了")
	if m.mode == cockpitModeMemoryReview {
		quitHelp = Localize("ctrl+c quit", "ctrl+c 終了")
		if m.memoryReview.applying {
			quitHelp = Localize("quit disabled while applying", "適用中は終了できません")
		}
	} else if m.mode == cockpitModeSettings && m.settings.editingPattern {
		quitHelp = Localize("ctrl+c quit", "ctrl+c 終了")
	}
	parts := []string{}
	if m.cockpitSectionNavigationAvailable() {
		parts = append(parts, Localize("1-4 tabs", "1-4 タブ"))
		parts = append(parts, Localize("←/→ tabs", "←/→ タブ"))
		parts = append(parts, Localize("tab/shift+tab next/prev", "tab/shift+tab 次/前"))
	}
	if m.width > 0 && m.height > 0 {
		parts = append(parts, Localizef("terminal %dx%d", "端末 %dx%d", m.width, m.height))
	}
	if m.cockpitBackAvailable() {
		parts = append(parts, Localize("esc back", "esc 戻る"))
	}
	if quitHelp != "" {
		parts = append(parts, quitHelp)
	}
	if m.cockpitHelpAvailable() {
		parts = append(parts, Localize("? help", "? ヘルプ"))
	}
	if localHelp != "" {
		parts = append(parts, localHelp)
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) cockpitSectionNavigationAvailable() bool {
	if m.mode == cockpitModeSettings && (m.settings.editingPattern || m.settings.confirmSave) {
		return false
	}
	if m.mode != cockpitModeMemoryReview {
		return true
	}
	if m.memoryReview.applying {
		return false
	}
	return m.memoryReview.loading || m.memoryReview.err != nil || m.memoryReview.review.mode == reviewModeBrowse || m.memoryReview.review.mode == reviewModeHelp
}

func (m cockpitModel) cockpitBackAvailable() bool {
	if m.mode == cockpitModeSettings && (m.settings.editingPattern || m.settings.confirmSave) {
		return false
	}
	if m.mode == cockpitModeLive {
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
	if m.mode == cockpitModeSettings && (m.settings.editingPattern || m.settings.confirmSave) {
		return false
	}
	if m.mode != cockpitModeMemoryReview {
		return true
	}
	return !m.memoryReview.applying && m.memoryReview.review.mode != reviewModeEdit
}

func (m cockpitModel) cockpitContextualHelp() []string {
	lines := []string{m.styles.Subtle.Render(Localize("Action menu", "アクションメニュー"))}
	actions := m.cockpitContextualActions()
	if len(actions) == 0 {
		lines = append(lines, "• "+Localize("No local actions are available while this operation is in progress.", "この処理中に利用できる画面内アクションはありません。"))
	} else {
		for _, action := range actions {
			if action.key == "" {
				lines = append(lines, "• "+action.description)
				continue
			}
			lines = append(lines, fmt.Sprintf("• %-12s %s", action.key, action.description))
		}
	}
	lines = append(lines, "", m.styles.Subtle.Render(m.cockpitContextualNavigationTitle()))
	lines = append(lines, m.cockpitContextualNavigationLines()...)
	lines = append(lines,
		"",
		m.styles.Subtle.Render(Localize("Fallback commands available today:", "現時点で使える代替コマンド:")),
		"traceary sessions --snapshot [--json]",
		"traceary top --snapshot [--json] # compatibility",
		"traceary tail",
		"traceary doctor --json",
		"traceary session handoff",
		"traceary memory inbox review",
		"traceary tui --reset-state",
	)
	return lines
}

func (m cockpitModel) cockpitContextualNavigationTitle() string {
	if m.mode == cockpitModeSettings && (m.settings.editingPattern || m.settings.confirmSave) {
		return Localize("Navigation paused", "ナビゲーション一時停止")
	}
	return Localize("Global navigation", "全体ナビゲーション")
}

func (m cockpitModel) cockpitContextualNavigationLines() []string {
	if m.mode == cockpitModeSettings && m.settings.editingPattern {
		return []string{Localize("Navigation is paused while regex input is active; enter stages the regex and esc cancels.", "regex 入力中はナビゲーションを一時停止します。enter で staged、esc でキャンセルします。")}
	}
	if m.mode == cockpitModeSettings && m.settings.confirmSave {
		return []string{Localize("Navigation is paused while config write confirmation is active; y saves and n/esc cancels.", "config 書き込み確認中はナビゲーションを一時停止します。y で保存、n/esc でキャンセルします。")}
	}
	items := cockpitNavigationSectionsList()
	labelWidth := cockpitNavigationLabelWidth(items)
	lines := make([]string, 0, len(items)+2)
	for _, item := range items {
		lines = append(lines, cockpitNavigationLine(item, labelWidth))
	}
	lines = append(lines, Localize("← / → cycle tabs; tab / shift+tab remain supported", "← / → でタブ移動。tab / shift+tab も利用可能"))
	lines = append(lines, Localize("esc backs out to Tail; q exits the TUI", "esc で Tail に戻り、q で TUI を終了"))
	return lines
}

func cockpitNavigationLabelWidth(items []cockpitNavigationSection) int {
	maxWidth := 0
	for _, item := range items {
		width := runeWidth(item.prefix())
		if width > maxWidth {
			maxWidth = width
		}
	}
	return maxWidth + 1
}

func cockpitNavigationLine(item cockpitNavigationSection, labelWidth int) string {
	prefix := item.prefix()
	padding := labelWidth - runeWidth(prefix)
	if padding < 1 {
		padding = 1
	}
	return prefix + strings.Repeat(" ", padding) + item.description()
}

func (m cockpitModel) cockpitContextualActions() []cockpitAction {
	switch m.mode {
	case cockpitModeDoctor:
		actions := []cockpitAction{{key: "r", description: Localize("Refresh doctor checks", "Doctor チェックを再取得")}}
		if len(m.doctorLines()) > 3 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Scroll doctor output", "Doctor 出力をスクロール")})
		}
		if !m.doctor.loading && m.doctor.err == nil && cockpitDoctorHasRemediation(m.doctor.snapshot) {
			actions = append(actions, cockpitAction{description: Localize("Remediation commands are shown inline; copy and run them outside the cockpit.", "修復 command は inline に表示されます。copy して cockpit 外で実行してください。")})
		}
		return actions
	case cockpitModeLive:
		actions := []cockpitAction{
			{key: "r", description: Localize("Refresh Tail events", "Tail のイベントを再取得")},
			{key: "End/G", description: Localize("Jump to newest and resume auto-follow", "最新へ移動して auto-follow を再開")},
		}
		if len(m.live.events) > 1 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Scroll Tail stream", "Tail stream をスクロール")})
		}
		actions = append(actions, cockpitAction{description: Localize("Tail is read-only; inspect full event payloads from Sessions detail or `traceary show <event_id>`.", "Tail は読み取り専用です。完全なイベント内容は Sessions detail または `traceary show <event_id>` で確認します。")})
		return actions
	case cockpitModeTop:
		if m.cockpitTopDetailOpen() {
			actions := []cockpitAction{{key: "esc", description: Localize("Close Sessions detail", "Sessions 詳細を閉じる")}}
			if len(m.cockpitTopDetailLines()) > 1 {
				actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Scroll Sessions detail", "Sessions 詳細をスクロール")})
			}
			return actions
		}
		actions := []cockpitAction{
			{key: "r", description: Localize("Refresh Sessions dashboard", "Sessions ダッシュボードを再取得")},
			{key: "d", description: Localize("Open Doctor checks", "Doctor チェックを開く")},
		}
		if cockpitTopHasSelectableRow(m.cockpitTopRows()) {
			actions = append(actions,
				cockpitAction{key: "↑/↓", description: Localize("Select a Sessions row", "Sessions row を選択")},
				cockpitAction{key: "enter", description: Localize("Open selected Sessions detail", "選択中 Sessions row の詳細を開く")},
			)
		}
		return actions
	case cockpitModeDetail:
		actions := []cockpitAction{{key: "esc", description: Localize("Return to Tail", "Tail に戻る")}}
		if len(m.detailLines()) > 1 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Scroll event detail", "イベント詳細をスクロール")})
		}
		return actions
	case cockpitModeMemoryReview:
		return m.memoryReviewContextualActions()
	case cockpitModeSettings:
		return m.settingsContextualActions()
	default:
		return []cockpitAction{
			{key: "1", description: Localize("Open Tail", "Tail を開く")},
			{key: "2", description: Localize("Open Sessions dashboard", "Sessions ダッシュボードを開く")},
			{key: "3", description: Localize("Open Memory review", "メモリ確認を開く")},
			{key: "4", description: Localize("Open Settings", "Settings を開く")},
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

func (m cockpitModel) memoryReviewContextualActions() []cockpitAction {
	switch {
	case m.memoryReview.loading:
		return []cockpitAction{
			{key: "esc", description: Localize("Return to Tail while the memory review queue loads", "メモリ候補の確認キューの読み込み中に Tail へ戻る")},
			{key: "q", description: Localize("Quit without applying decisions", "判断を適用せず終了")},
		}
	case m.memoryReview.applying:
		return nil
	case m.memoryReview.err != nil:
		return []cockpitAction{
			{key: "esc", description: Localize("Return to Tail", "Tail へ戻る")},
			{key: "q", description: Localize("Quit without applying decisions", "判断を適用せず終了")},
		}
	}
	if len(m.memoryReview.items) == 0 {
		return []cockpitAction{
			{key: "q", description: Localize("Finish review and return to Tail", "確認を終了して Tail へ戻る")},
			{key: "esc", description: Localize("Return to Tail without applying", "適用せず Tail へ戻る")},
		}
	}
	switch m.memoryReview.review.mode {
	case reviewModeEdit:
		return []cockpitAction{
			{key: "enter", description: Localize("Commit operator-authored fact", "operator が書いた事実を確定")},
			{key: "esc", description: Localize("Cancel edit", "編集をキャンセル")},
			{key: "backspace", description: Localize("Edit text", "テキストを編集")},
		}
	case reviewModeViewEvidence:
		return []cockpitAction{
			{key: "v / esc", description: Localize("Close evidence view", "evidence 表示を閉じる")},
			{key: "q", description: Localize("Finish review and apply queued decisions", "確認を終了し予約済み判断を適用")},
		}
	case reviewModeHelp:
		return []cockpitAction{
			{key: "? / esc", description: Localize("Close memory review help", "メモリ確認ヘルプを閉じる")},
			{key: "q", description: Localize("Finish review and apply queued decisions", "確認を終了し予約済み判断を適用")},
		}
	default:
		acceptDescription := Localize("Accept as-is only when the checklist passes", "チェックリストを満たす場合のみ accept as-is")
		editDescription := Localize("Edit/distill into an operator-authored fact when wording is unclear", "文言が曖昧なら operator が書いた事実に edit/distill")
		if len(m.memoryReview.items) > 0 && m.memoryReview.review.cursor >= 0 && m.memoryReview.review.cursor < len(m.memoryReview.items) {
			current := m.memoryReview.items[m.memoryReview.review.cursor]
			switch {
			case memoryReviewBlocksAccept(current):
				acceptDescription = Localize("Accept as-is unavailable until evidence exists", "evidence が追加されるまで accept as-is はできません")
				editDescription = Localize("Edit/distill unavailable without source evidence", "source evidence がないため edit/distill はできません")
			case memoryReviewRequiresAcceptConfirmation(current):
				acceptDescription = Localize("Accept as-is (requires pressing a twice; prefer edit/distill if unsure)", "accept as-is するには a を 2 回押します。不明なら edit/distill を優先")
			}
		}
		actions := []cockpitAction{
			{key: "a", description: acceptDescription},
			{key: "x", description: Localize("Reject current candidate", "現在のメモリ候補を reject")},
			{key: "s", description: Localize("Skip when more context is needed", "追加 context が必要なら skip")},
			{key: "e", description: editDescription},
			{key: "v", description: Localize("View evidence and artifact refs", "evidence と artifact refs を表示")},
			{key: "q", description: Localize("Finish review and apply queued decisions", "確認を終了し予約済み判断を適用")},
			{description: Localize("Accept checklist: factual, stable, useful later, scoped correctly, evidence-backed, not duplicate/stale.", "accept checklist: 事実で安定、将来有用、scope が正しい、evidence あり、重複/古さなし。")},
		}
		if len(m.memoryReview.items) > 1 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Navigate candidates", "メモリ候補を移動")})
		}
		return actions
	}
}

func (m cockpitModel) liveLocalHelp() string {
	parts := []string{Localize("r refresh", "r 再取得"), Localize("End/G newest", "End/G 最新")}
	if len(m.live.events) > 1 {
		parts = append(parts, Localize("↑/↓ scroll", "↑/↓ スクロール"))
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) topLocalHelp() string {
	if m.cockpitTopDetailOpen() {
		parts := []string{Localize("esc close detail", "esc 詳細を閉じる")}
		if len(m.cockpitTopDetailLines()) > 1 {
			parts = append(parts, Localize("↑/↓ scroll", "↑/↓ スクロール"))
		}
		return strings.Join(parts, " · ")
	}
	parts := []string{Localize("r refresh", "r 再取得")}
	rows := m.cockpitTopRows()
	if cockpitTopHasSelectableRow(rows) {
		parts = append(parts, Localize("↑/↓ select", "↑/↓ 選択"))
		if m.top.selected >= 0 && m.top.selected < len(rows) && rows[m.top.selected].selectable {
			parts = append(parts, Localize("enter detail", "enter 詳細"))
		}
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) doctorLocalHelp() string {
	parts := []string{Localize("r refresh", "r 再取得")}
	if len(m.doctorLines()) > 3 {
		parts = append(parts, Localize("↑/↓ scroll", "↑/↓ スクロール"))
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) detailLocalHelp() string {
	if len(m.detailLines()) > 1 {
		return Localize("↑/↓ scroll", "↑/↓ スクロール")
	}
	return ""
}

func (m cockpitModel) memoryReviewLocalHelp() string {
	switch {
	case m.memoryReview.loading, m.memoryReview.err != nil:
		return Localize("q quit", "q 終了")
	case m.memoryReview.applying:
		return Localize("applying decisions", "判断を適用中")
	case len(m.memoryReview.items) == 0:
		return Localize("q finish review", "q 確認終了")
	}
	switch m.memoryReview.review.mode {
	case reviewModeEdit:
		return Localize("enter commit · esc cancel · backspace edit", "enter 確定 · esc キャンセル · backspace 編集")
	case reviewModeAttach:
		return Localize("enter queue evidence · esc cancel", "enter evidence 保留 · esc キャンセル")
	case reviewModeViewEvidence:
		return Localize("r attach evidence · v/esc close evidence · q finish/apply", "r evidence 追加 · v/esc evidence を閉じる · q 終了/適用")
	case reviewModeHelp:
		return Localize("?/esc close help · q finish/apply", "?/esc help を閉じる · q 終了/適用")
	default:
		if m.memoryReview.review.cursor >= 0 && m.memoryReview.review.cursor < len(m.memoryReview.items) && memoryReviewBlocksAccept(m.memoryReview.items[m.memoryReview.review.cursor]) {
			return Localize("a unavailable (evidence required) · r attach evidence · x reject · s skip · v evidence · q finish/apply", "a 不可 (evidence 必須) · r evidence 追加 · x reject · s skip · v evidence · q 終了/適用")
		}
		return Localize("a accept as-is · x reject · s skip · e edit/distill · r attach evidence · v evidence · q finish/apply", "a accept as-is · x reject · s skip · e edit/distill · r evidence 追加 · v evidence · q 終了/適用")
	}
}

func (m cockpitModel) detailLines() []string {
	if m.detail.loading {
		return []string{Localize("Loading...", "読み込み中...")}
	}
	return m.detail.lines
}

func (m cockpitModel) formatCockpitLiveEventRow(event *model.Event, loc *time.Location) string {
	extras := compactRowExtras{}
	if event != nil && m.live.extras != nil {
		extras = m.live.extras[event.EventID()]
	}
	return formatCockpitLiveEventRow(event, loc, m.cockpitLiveRowWidth(), extras, true)
}

func formatCockpitLiveEventRow(event *model.Event, loc *time.Location, targetWidth int, extras compactRowExtras, colorEnabled bool) string {
	if event == nil {
		return "-"
	}
	displayEvent := event
	truncationMarker := ""
	out := newTruncatedEventOutput(event, apptypes.DefaultTopSnapshotBodyLimit)
	if out.Truncated {
		truncationMarker = " [truncated]"
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
	rowWidth := targetWidth
	if rowWidth > runeLen(truncationMarker)+8 {
		rowWidth -= runeLen(truncationMarker)
	}
	row := formatEventCompactRow(displayEvent, eventTextFormatOptions{location: loc, targetWidth: rowWidth, messageMinWidth: 8, hardTargetWidth: true, colorEnabled: colorEnabled}, extras)
	return row + truncationMarker
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

// cockpitIO resolves the stdin/stdout pair the cockpit TUI should drive. Cobra
// hands production runs os.Stdin / os.Stdout; tests or embedded callers may pass
// wrapped streams, which intentionally refuse the cockpit and keep the command
// on the non-interactive fallback path.
func cockpitIO(input io.Reader, output io.Writer) (*os.File, *os.File, bool) {
	stdin, _ := input.(*os.File)
	stdout, _ := output.(*os.File)
	return stdin, stdout, stdin != nil && stdout != nil
}
