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

type cockpitTriageCard struct {
	category string
	severity string
	title    string
	summary  string
	action   string
	details  []string
}

func (s cockpitHomeSnapshot) triageCards() []cockpitTriageCard {
	cards := make([]cockpitTriageCard, 0, 8)
	cards = append(cards, s.problemCards()...)
	cards = append(cards, s.recentFailuresCard())
	cards = append(cards, s.newActivityCard())
	cards = append(cards, s.memoryInboxCard())
	cards = append(cards, s.activeSessionsCard())
	cards = append(cards, s.healthCard())
	return cards
}

func (s cockpitHomeSnapshot) problemCards() []cockpitTriageCard {
	cards := make([]cockpitTriageCard, 0, 4)
	if s.DoctorError != "" {
		cards = append(cards, cockpitTriageCard{
			category: Localize("PROBLEMS", "問題"),
			severity: "FAIL",
			title:    Localize("Doctor unavailable", "Doctor を利用できません"),
			summary:  s.DoctorError,
			action:   Localize("3 Doctor, or run `traceary doctor --json` outside the cockpit", "3 Doctor、または cockpit 外で `traceary doctor --json` を実行"),
		})
	}
	if s.DoctorFailCount > 0 {
		cards = append(cards, cockpitTriageCard{
			category: Localize("PROBLEMS", "問題"),
			severity: "FAIL",
			title:    Localize("Doctor failures", "Doctor 失敗"),
			summary:  Localizef("%d check(s) failing", "%d 件の check が失敗", s.DoctorFailCount),
			action:   Localize("3 Doctor shows remediation commands", "3 Doctor で修復 command を確認"),
		})
	}
	if s.HookFailCount > 0 {
		cards = append(cards, cockpitTriageCard{
			category: Localize("PROBLEMS", "問題"),
			severity: "FAIL",
			title:    Localize("Hook/MCP failures", "Hook/MCP 失敗"),
			summary:  Localizef("%d failing hook/MCP check(s)", "%d 件の Hook/MCP check が失敗", s.HookFailCount),
			action:   Localize("3 Doctor, then inspect Hooks/MCP remediation", "3 Doctor で Hooks/MCP の修復手順を確認"),
		})
	}
	if s.LargePayloadCount > 0 {
		cards = append(cards, cockpitTriageCard{
			category: Localize("PROBLEMS", "問題"),
			severity: "WARN",
			title:    Localize("Large payloads", "大きな payload"),
			summary:  Localizef("%d event(s) exceed the snapshot body limit", "%d 件の event が snapshot body limit を超過", s.LargePayloadCount),
			action:   Localize("2 Live for context; `traceary show <event_id>` for full payload", "2 Live で context を確認し、全文は `traceary show <event_id>`"),
		})
	}
	if len(cards) == 0 {
		cards = append(cards, cockpitTriageCard{
			category: Localize("PROBLEMS", "問題"),
			severity: "OK",
			title:    Localize("No immediate cockpit warnings", "直近の cockpit warning はありません"),
			summary:  Localize("no failing doctor, hook, MCP, or payload signals", "doctor / hook / MCP / payload の失敗 signal はありません"),
			action:   Localize("Continue with Live, Memory, Doctor, or Sessions as needed", "必要に応じて Live / Memory / Doctor / Sessions を確認"),
		})
	}
	return cards
}

func (s cockpitHomeSnapshot) newActivityCard() cockpitTriageCard {
	severity := "OK"
	summary := ""
	details := []string{}
	if s.NewEventKnown {
		if s.NewEventCount > 0 {
			severity = "NEW"
			summary = Localizef("new events=%d since %s", "新着 event=%d（%s 以降）", s.NewEventCount, formatCockpitCheckpoint(s.EventLastSeenAt))
		} else {
			summary = Localizef("no unseen events since %s", "%s 以降の未確認 event はありません", formatCockpitCheckpoint(s.EventLastSeenAt))
		}
	} else {
		severity = "INFO"
		summary = Localize("new events=untracked until cockpit state is initialized", "cockpit state 初期化まで新着 event は未追跡")
	}
	if s.NewEventScanLimited {
		details = append(details, Localize("event scan limited; count is capped", "event scan は制限中; 件数は上限で丸められています"))
	}
	if s.RecentCommandCount > 0 {
		details = append(details, Localizef("recent commands=%d", "最近の command=%d", s.RecentCommandCount))
	}
	return cockpitTriageCard{
		category: Localize("NEW ACTIVITY", "新着"),
		severity: severity,
		title:    Localize("Events", "イベント"),
		summary:  summary,
		action:   Localize("2 Live opens the event stream and marks current events seen", "2 Live で event stream を開き、現在の event を確認済みにする"),
		details:  details,
	}
}

func (s cockpitHomeSnapshot) memoryInboxCard() cockpitTriageCard {
	severity := "OK"
	summary := Localize("memory inbox is clear", "memory inbox は空です")
	details := []string{
		Localizef("candidate memories=%d", "candidate memory=%d", s.CandidateMemoryCount),
		Localizef("accepted memories=%d", "accepted memory=%d", s.AcceptedMemoryCount),
	}
	if s.NewCandidateMemoryKnown {
		details = append(details, Localizef("new memory checkpoint=%s", "新着 memory checkpoint=%s", formatCockpitCheckpoint(s.MemoryLastSeenAt)))
		if s.NewCandidateMemoryCount > 0 {
			severity = "NEW"
			summary = Localizef("new candidate memories=%d", "新着 candidate memory=%d", s.NewCandidateMemoryCount)
		} else {
			summary = Localizef("no unseen candidates since %s", "%s 以降の未確認 candidate はありません", formatCockpitCheckpoint(s.MemoryLastSeenAt))
		}
	} else {
		details = append(details, Localize("candidate memory new count=untracked", "candidate memory の新着件数=未追跡"))
		if s.CandidateMemoryCount > 0 {
			severity = "WARN"
			summary = Localizef("candidate memories=%d; new count untracked", "candidate memory=%d; 新着件数は未追跡", s.CandidateMemoryCount)
		}
	}
	if s.CandidateMemoryCount > 0 && severity == "OK" {
		severity = "WARN"
		summary = Localizef("candidate memories=%d", "candidate memory=%d", s.CandidateMemoryCount)
	}
	if s.RememberIntentCount > 0 {
		details = append(details, Localizef("remember-intent candidates=%d", "remember-intent candidate=%d", s.RememberIntentCount))
	}
	if s.LowQualityMemoryCount > 0 {
		details = append(details, Localizef("low-quality candidates=%d", "低品質 candidate=%d", s.LowQualityMemoryCount))
		if severity == "OK" {
			severity = "WARN"
			summary = Localizef("low-quality candidates=%d need review", "低品質 candidate=%d 件は review が必要", s.LowQualityMemoryCount)
		}
	}
	if s.StaleMemoryCount > 0 {
		details = append(details, Localizef("stale memories=%d", "stale memory=%d", s.StaleMemoryCount))
		if severity == "OK" {
			severity = "WARN"
			summary = Localizef("stale memories=%d", "stale memory=%d", s.StaleMemoryCount)
		}
	}
	if s.MemoryScanLimited {
		details = append(details, Localize("memory scan limited; counts may be capped", "memory scan は制限中; 件数は上限で丸められている可能性があります"))
	}
	return cockpitTriageCard{
		category: Localize("MEMORY INBOX", "メモリ inbox"),
		severity: severity,
		title:    Localize("Memory review", "メモリ review"),
		summary:  summary,
		action:   Localize("4 Memory opens inbox review; use edit/distill or skip for ambiguous candidates", "4 Memory で inbox review を開く。曖昧な候補は edit/distill または skip"),
		details:  details,
	}
}

func (s cockpitHomeSnapshot) activeSessionsCard() cockpitTriageCard {
	if s.StaleActiveSessionCount > 0 {
		return cockpitTriageCard{
			category: Localize("ACTIVE SESSIONS", "アクティブ session"),
			severity: "WARN",
			title:    Localize("Stale active sessions", "古い active session"),
			summary:  Localizef("stale active sessions=%d", "古い active session=%d", s.StaleActiveSessionCount),
			action:   Localize("5 Sessions for handoff context; `traceary session gc --stale-after 24h --dry-run` for cleanup", "5 Sessions で handoff context を確認。cleanup は `traceary session gc --stale-after 24h --dry-run`"),
		}
	}
	return cockpitTriageCard{
		category: Localize("ACTIVE SESSIONS", "アクティブ session"),
		severity: "OK",
		title:    Localize("Sessions", "セッション"),
		summary:  Localize("no stale active sessions detected", "古い active session は見つかっていません"),
		action:   Localize("5 Sessions lists handoff entry points", "5 Sessions で handoff 導線を確認"),
	}
}

func (s cockpitHomeSnapshot) recentFailuresCard() cockpitTriageCard {
	if s.RecentFailureCount > 0 {
		return cockpitTriageCard{
			category: Localize("RECENT FAILURES", "最近の失敗"),
			severity: "WARN",
			title:    Localize("Recent failures", "最近の失敗"),
			summary:  Localizef("recent failures=%d", "最近の失敗=%d", s.RecentFailureCount),
			action:   Localize("2 Live opens the event stream; press enter on a failure for detail", "2 Live で event stream を開き、失敗行で enter を押して詳細を確認"),
		}
	}
	return cockpitTriageCard{
		category: Localize("RECENT FAILURES", "最近の失敗"),
		severity: "OK",
		title:    Localize("Failures", "失敗"),
		summary:  Localize("no recent failure events in the top snapshot", "top snapshot に最近の失敗 event はありません"),
		action:   Localize("2 Live keeps watching for new events", "2 Live で新着 event を監視"),
	}
}

func (s cockpitHomeSnapshot) healthCard() cockpitTriageCard {
	severity := "OK"
	if s.DoctorFailCount > 0 || s.HookFailCount > 0 || s.DoctorError != "" {
		severity = "FAIL"
	} else if s.DoctorWarnCount > 0 || s.HookWarnCount > 0 {
		severity = "WARN"
	}
	details := []string{
		Localizef("doctor: pass=%d warn=%d fail=%d", "doctor: pass=%d warn=%d fail=%d", s.DoctorPassCount, s.DoctorWarnCount, s.DoctorFailCount),
		Localizef("hooks/mcp: warn=%d fail=%d", "hooks/mcp: warn=%d fail=%d", s.HookWarnCount, s.HookFailCount),
	}
	return cockpitTriageCard{
		category: Localize("HEALTH", "ヘルス"),
		severity: severity,
		title:    Localize("Doctor", "Doctor"),
		summary:  Localizef("doctor fail=%d warn=%d; hook/MCP fail=%d warn=%d", "doctor fail=%d warn=%d; hook/MCP fail=%d warn=%d", s.DoctorFailCount, s.DoctorWarnCount, s.HookFailCount, s.HookWarnCount),
		action:   Localize("3 Doctor refreshes health checks and shows remediation hints", "3 Doctor で health check を再取得し修復 hint を確認"),
		details:  details,
	}
}

func (s cockpitHomeSnapshot) allGreen() bool {
	for _, card := range s.triageCards() {
		if card.severity != "OK" && card.severity != "INFO" {
			return false
		}
	}
	return true
}

func formatCockpitCheckpoint(at time.Time) string {
	if at.IsZero() {
		return Localize("not recorded", "未記録")
	}
	return formatJSONTime(at)
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
	cockpitModeSettings
)

type cockpitSectionID int

const (
	cockpitSectionHome cockpitSectionID = iota
	cockpitSectionLive
	cockpitSectionDoctor
	cockpitSectionMemory
	cockpitSectionSessions
	cockpitSectionSettings
)

type cockpitNavigationSection struct {
	id  cockpitSectionID
	key string
}

type cockpitAction struct {
	key         string
	description string
}

var cockpitNavigationSections = []cockpitNavigationSection{
	{id: cockpitSectionHome, key: "1"},
	{id: cockpitSectionLive, key: "2"},
	{id: cockpitSectionDoctor, key: "3"},
	{id: cockpitSectionMemory, key: "4"},
	{id: cockpitSectionSessions, key: "5"},
	{id: cockpitSectionSettings, key: "6"},
}

// cockpitNavigationSectionLabel returns the localized label for a cockpit
// navigation section. Keys (1-5) and ids stay language-neutral so global
// navigation shortcuts and stored state do not drift with the operator's
// locale.
func cockpitNavigationSectionLabel(id cockpitSectionID) string {
	switch id {
	case cockpitSectionHome:
		return Localize("Home", "ホーム")
	case cockpitSectionLive:
		return Localize("Live", "ライブ")
	case cockpitSectionDoctor:
		return Localize("Doctor", "ドクター")
	case cockpitSectionMemory:
		return Localize("Memory", "メモリ")
	case cockpitSectionSessions:
		return Localize("Sessions", "セッション")
	case cockpitSectionSettings:
		return Localize("Settings", "設定")
	}
	return ""
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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.clampLiveSelection()
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
	if m.mode == cockpitModeSettings {
		return m.updateSettingsKey(msg)
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
	case '6':
		return cockpitSectionSettings, true
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
	case cockpitModeSettings:
		return cockpitSectionSettings
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
		case "s":
			return m.openCockpitSection(cockpitSectionSettings)
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
			return cockpitHomeMsg{seq: seq, err: xerrors.Errorf(Localize("cockpit home loader is not configured", "cockpit home loader が設定されていません"))}
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
			return cockpitDoctorLoadedMsg{seq: seq, err: xerrors.Errorf(Localize("cockpit doctor loader is not configured", "cockpit doctor loader が設定されていません"))}
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
			return cockpitMemoryReviewLoadedMsg{seq: seq, seenAt: seenAt, err: xerrors.Errorf(Localize("cockpit memory review loader is not configured", "cockpit memory review loader が設定されていません"))}
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
			return cockpitMemoryReviewAppliedMsg{err: xerrors.Errorf(Localize("cockpit memory review loader is not configured", "cockpit memory review loader が設定されていません"))}
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
			return cockpitLiveMsg{initial: initial, seq: seq, err: xerrors.Errorf(Localize("cockpit live loader is not configured", "cockpit live loader が設定されていません"))}
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
			return cockpitDetailLoadedMsg{request: req, err: xerrors.Errorf(Localize("cockpit detail loader is not configured", "cockpit detail loader が設定されていません"))}
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
	case cockpitModeSettings:
		return m.settingsView()
	}
	return m.homeView()
}

func (m cockpitModel) homeView() string {
	allGreen := m.home.allGreen()
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
	lines = append(lines, m.styles.Subtle.Render(Localize("TRIAGE BOARD", "トリアージ board")))
	currentCategory := ""
	for _, card := range m.home.triageCards() {
		if card.category != currentCategory {
			if currentCategory != "" {
				lines = append(lines, "")
			}
			currentCategory = card.category
			lines = append(lines, m.styles.Subtle.Render(currentCategory))
		}
		lines = append(lines, m.renderCockpitTriageCard(card)...)
	}
	if allGreen {
		lines = append(lines,
			"",
			m.styles.Success.Render(Localize("ALL GREEN", "すべて正常")),
			"• "+Localize("No immediate cockpit warnings or tracked new activity.", "直近の cockpit warning や追跡済みの新着 activity はありません。"),
			"• "+Localize("Next: 2 Live for ongoing work, 4 Memory for periodic review, 3 Doctor before release, 5 Sessions for handoff, 6 Settings for configuration.", "次: 作業中は 2 Live、定期確認は 4 Memory、release 前は 3 Doctor、handoff は 5 Sessions、設定は 6 Settings。"),
		)
	}
	lines = append(lines,
		"",
		m.styles.Subtle.Render(Localize("SIGNAL COUNTS", "signal 件数")),
		Localizef("• doctor: pass=%d warn=%d fail=%d", "• doctor: pass=%d warn=%d fail=%d", m.home.DoctorPassCount, m.home.DoctorWarnCount, m.home.DoctorFailCount),
		Localizef("• hooks/mcp: warn=%d fail=%d", "• hooks/mcp: warn=%d fail=%d", m.home.HookWarnCount, m.home.HookFailCount),
		Localizef("• sessions: stale_active=%d recent_failures=%d recent_commands=%d new_events=%s%s", "• sessions: stale_active=%d recent_failures=%d recent_commands=%d new_events=%s%s", m.home.StaleActiveSessionCount, m.home.RecentFailureCount, m.home.RecentCommandCount, formatCockpitNewEventCount(m.home), eventScanSuffix),
		Localizef("• memories: accepted(reviewed)=%d candidate(inbox)=%d new=%s remember-intent=%d low-quality=%d stale=%d%s", "• memories: accepted(reviewed)=%d candidate(inbox)=%d new=%s remember-intent=%d low-quality=%d stale=%d%s", m.home.AcceptedMemoryCount, m.home.CandidateMemoryCount, formatCockpitNewCandidateCount(m.home), m.home.RememberIntentCount, m.home.LowQualityMemoryCount, m.home.StaleMemoryCount, memoryScanSuffix),
		Localizef("• payloads: large=%d", "• payloads: large=%d", m.home.LargePayloadCount),
	)
	if !allGreen {
		lines = append(lines,
			"",
			m.styles.Subtle.Render(Localize("NEXT ACTIONS", "次の action")),
			"• "+Localize("2 Live: inspect new events, failures, and event details", "2 Live: 新着 event、失敗、event 詳細を確認"),
			"• "+Localize("4 Memory: review candidate memories conservatively", "4 Memory: candidate memory を慎重に review"),
			"• "+Localize("3 Doctor: check health and remediation commands", "3 Doctor: health と修復 command を確認"),
			"• "+Localize("5 Sessions: session list and handoff entry points", "5 Sessions: session 一覧と handoff 導線を確認"),
		)
	}
	return m.renderCockpitShell(Localize("home", "ホーム"), lines, "")
}

func (m cockpitModel) renderCockpitTriageCard(card cockpitTriageCard) []string {
	style := m.styles.Subtle
	switch card.severity {
	case "FAIL":
		style = m.styles.Error
	case "WARN", "NEW":
		style = m.styles.Warning
	case "OK":
		style = m.styles.Success
	}
	lines := []string{
		style.Render(fmt.Sprintf("• [%s] %s — %s", cockpitSeverityLabel(card.severity), card.title, card.summary)),
		Localize("  next: ", "  次: ") + card.action,
	}
	for _, detail := range card.details {
		lines = append(lines, Localize("  detail: ", "  詳細: ")+detail)
	}
	return lines
}

func cockpitSeverityLabel(severity string) string {
	switch severity {
	case "FAIL":
		return Localize("FAIL", "失敗")
	case "WARN":
		return Localize("WARN", "警告")
	case "NEW":
		return Localize("NEW", "新着")
	case "OK":
		return Localize("OK", "正常")
	case "INFO":
		return Localize("INFO", "情報")
	default:
		return severity
	}
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
	return Localizef("memory review applied: accepted=%d rejected=%d distilled=%d failures=%d", "memory review 適用済み: accepted=%d rejected=%d distilled=%d failures=%d", len(result.Accepted), len(result.Rejected), len(result.Distilled), len(result.Failures))
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
			m.styles.Subtle.Render(Localize("Press r to refresh checks or 1 to return Home.", "r で check を再取得、1 でホームへ戻ります。")),
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
			m.styles.Subtle.Render(Localize("Press r to refresh checks or 1 to return Home.", "r で check を再取得、1 でホームへ戻ります。")),
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
		lines = append(lines, m.styles.Subtle.Render(Localize("Loading memory inbox review queue...", "memory inbox review queue を読み込み中...")))
	case m.memoryReview.applying:
		lines = append(lines, m.styles.Subtle.Render(Localize("Applying memory review decisions...", "memory review の判断を適用中...")))
	case m.memoryReview.err != nil:
		lines = append(lines, m.styles.Error.Render(m.memoryReview.err.Error()))
	default:
		lines = append(lines, m.memoryReview.review.View())
	}
	return m.renderCockpitShell(Localize("memory review", "メモリ review"), lines, m.memoryReviewLocalHelp())
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
			lines = append(lines, m.styles.Subtle.Render(Localize("Loading live events...", "live event を読み込み中...")))
		} else {
			lines = append(lines, m.styles.Subtle.Render(Localize("No recent events. Press r to refresh or f to follow.", "最近の event はありません。r で再取得、f で follow します。")))
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
	return m.renderCockpitShell(Localize("live tail", "live tail"), lines, m.liveLocalHelp())
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

func (m cockpitModel) sessionsView() string {
	eventScanSuffix := ""
	if m.home.NewEventScanLimited {
		eventScanSuffix = " scan_limited=true"
	}
	lines := []string{
		m.styles.Subtle.Render(Localize("Session and handoff workflows", "Session と handoff workflow")),
		"",
		Localizef("• stale active sessions=%d", "• 古い active session=%d", m.home.StaleActiveSessionCount),
		Localizef("• recent failures=%d", "• 最近の失敗=%d", m.home.RecentFailureCount),
		Localizef("• recent commands=%d", "• 最近の command=%d", m.home.RecentCommandCount),
		Localizef("• new events=%s%s", "• 新着 event=%s%s", formatCockpitNewEventCount(m.home), eventScanSuffix),
		"",
		m.styles.Subtle.Render(Localize("Available today:", "現在利用可能:")),
		"traceary top --snapshot [--json]",
		"traceary session handoff",
		"traceary tail [--follow]",
		"",
		m.styles.Subtle.Render(Localize("Dedicated session list and handoff drill-down remain compatible with the existing subcommands.", "専用の session list / handoff drill-down までは既存 subcommand への導線を維持します。")),
	}
	return m.renderCockpitShell(Localize("sessions", "セッション"), lines, Localize("r refresh session summary", "r session summary 再取得"))
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
		label := section.key + " " + cockpitNavigationSectionLabel(section.id)
		if section.id == active {
			label = "[" + label + "]"
		}
		parts = append(parts, label)
	}
	return Localize("sections: ", "セクション: ") + strings.Join(parts, "  ")
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
		parts = append(parts, Localize("1-6 sections", "1-6 セクション"), Localize("tab/shift+tab next/prev", "tab/shift+tab 次/前"))
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
	if m.mode == cockpitModeSettings && (m.settings.editingPattern || m.settings.confirmSave) {
		return false
	}
	if m.mode != cockpitModeMemoryReview {
		return true
	}
	return !m.memoryReview.applying && m.memoryReview.review.mode != reviewModeEdit
}

func (m cockpitModel) cockpitContextualHelp() []string {
	lines := []string{m.styles.Subtle.Render(Localize("Action menu", "アクション menu"))}
	actions := m.cockpitContextualActions()
	if len(actions) == 0 {
		lines = append(lines, "• "+Localize("No local actions are available while this operation is in progress.", "この処理中に利用できる local action はありません。"))
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
		m.styles.Subtle.Render(Localize("Global navigation", "Global navigation")),
		Localize("1 Home      triage overview and notifications", "1 ホーム      triage overview と通知"),
		Localize("2 Live      event stream and event details", "2 ライブ      event stream と event 詳細"),
		Localize("3 Doctor    health checks and remediation hints", "3 Doctor    health check と修復 hint"),
		Localize("4 Memory    inbox review queue", "4 メモリ      inbox review queue"),
		Localize("5 Sessions  session and handoff entry points", "5 セッション  session と handoff 導線"),
		Localize("6 Settings  language, read defaults, redaction diagnostics", "6 設定        language / read 既定 / redaction 診断"),
		Localize("tab / shift+tab cycle sections", "tab / shift+tab で section を移動"),
		Localize("esc backs out to the previous cockpit level; q exits the TUI", "esc で前の cockpit 階層へ戻り、q で TUI を終了"),
		"",
		m.styles.Subtle.Render(Localize("Fallback commands available today:", "現在利用できる fallback command:")),
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
		actions := []cockpitAction{{key: "r", description: Localize("Refresh doctor checks", "Doctor check を再取得")}}
		if len(m.doctorLines()) > 3 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Scroll doctor output", "Doctor 出力を scroll")})
		}
		if !m.doctor.loading && m.doctor.err == nil && cockpitDoctorHasRemediation(m.doctor.snapshot) {
			actions = append(actions, cockpitAction{description: Localize("Remediation commands are shown inline; copy and run them outside the cockpit.", "修復 command は inline に表示されます。copy して cockpit 外で実行してください。")})
		}
		return actions
	case cockpitModeLive:
		actions := []cockpitAction{
			{key: "r", description: Localize("Refresh live events", "live event を再取得")},
			{key: "f", description: m.liveFollowActionDescription()},
		}
		if len(m.live.events) > 0 {
			actions = append(actions, cockpitAction{key: "enter", description: Localize("Open selected event detail", "選択中 event の詳細を開く")})
			if len(m.live.events) > 1 {
				actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Select an event", "event を選択")})
			}
		}
		return actions
	case cockpitModeDetail:
		actions := []cockpitAction{{key: "esc", description: Localize("Return to Live", "Live に戻る")}}
		if len(m.detailLines()) > 1 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Scroll event detail", "event 詳細を scroll")})
		}
		return actions
	case cockpitModeMemoryReview:
		return m.memoryReviewContextualActions()
	case cockpitModeSessions:
		return []cockpitAction{
			{key: "r", description: Localize("Refresh session summary", "session summary を再取得")},
			{description: Localize("Use `traceary session handoff` for the full handoff outside the cockpit.", "完全な handoff は cockpit 外で `traceary session handoff` を使ってください。")},
		}
	case cockpitModeSettings:
		return m.settingsContextualActions()
	default:
		actions := []cockpitAction{
			{key: "2", description: Localize("Open Live tail", "Live tail を開く")},
			{key: "3", description: Localize("Run Doctor checks", "Doctor check を実行")},
			{key: "4", description: Localize("Open Memory review", "Memory review を開く")},
			{key: "5", description: Localize("Open Sessions and handoff entry points", "Sessions と handoff 導線を開く")},
			{key: "6", description: Localize("Open Settings", "Settings を開く")},
		}
		if m.home.allGreen() {
			actions = append(actions, cockpitAction{description: Localize("All green: use Live for ongoing work, Memory for periodic review, Doctor before release, Sessions for handoff, Settings for config changes.", "すべて正常: 作業中は Live、定期確認は Memory、release 前は Doctor、handoff は Sessions、設定変更は Settings を使います。")})
		} else {
			actions = append(actions, cockpitAction{description: Localize("Home cards are ordered by urgency; each card shows its next action target.", "Home card は緊急度順です。各 card に次の action target が表示されます。")})
		}
		return actions
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
		return Localize("Pause follow mode", "follow mode を停止")
	}
	return Localize("Start follow mode", "follow mode を開始")
}

func (m cockpitModel) memoryReviewContextualActions() []cockpitAction {
	switch {
	case m.memoryReview.loading:
		return []cockpitAction{
			{key: "esc", description: Localize("Return to Home while the inbox loads", "inbox 読み込み中に Home へ戻る")},
			{key: "q", description: Localize("Quit without applying decisions", "判断を適用せず終了")},
		}
	case m.memoryReview.applying:
		return nil
	case m.memoryReview.err != nil:
		return []cockpitAction{
			{key: "esc", description: Localize("Return to Home", "Home へ戻る")},
			{key: "q", description: Localize("Quit without applying decisions", "判断を適用せず終了")},
		}
	}
	if len(m.memoryReview.items) == 0 {
		return []cockpitAction{
			{key: "q", description: Localize("Finish review and refresh Home", "review を終了して Home を再取得")},
			{key: "esc", description: Localize("Return to Home without applying", "適用せず Home へ戻る")},
		}
	}
	switch m.memoryReview.review.mode {
	case reviewModeEdit:
		return []cockpitAction{
			{key: "enter", description: Localize("Commit operator-authored fact", "operator が書いた fact を確定")},
			{key: "esc", description: Localize("Cancel edit", "edit をキャンセル")},
			{key: "backspace", description: Localize("Edit text", "text を編集")},
		}
	case reviewModeViewEvidence:
		return []cockpitAction{
			{key: "v / esc", description: Localize("Close evidence view", "evidence view を閉じる")},
			{key: "q", description: Localize("Finish review and apply queued decisions", "review を終了し予約済み判断を適用")},
		}
	case reviewModeHelp:
		return []cockpitAction{
			{key: "? / esc", description: Localize("Close memory review help", "memory review help を閉じる")},
			{key: "q", description: Localize("Finish review and apply queued decisions", "review を終了し予約済み判断を適用")},
		}
	default:
		acceptDescription := Localize("Accept as-is only when the checklist passes", "checklist を満たす場合だけ accept as-is")
		if len(m.memoryReview.items) > 0 && m.memoryReview.review.cursor >= 0 && m.memoryReview.review.cursor < len(m.memoryReview.items) && memoryReviewRequiresAcceptConfirmation(m.memoryReview.items[m.memoryReview.review.cursor]) {
			acceptDescription = Localize("Accept as-is (requires pressing a twice; prefer edit/distill if unsure)", "accept as-is には a を 2 回押す必要があります。不明なら edit/distill を優先")
		}
		actions := []cockpitAction{
			{key: "a", description: acceptDescription},
			{key: "x", description: Localize("Reject current candidate", "現在の candidate を reject")},
			{key: "s", description: Localize("Skip when more context is needed", "追加 context が必要なら skip")},
			{key: "e", description: Localize("Edit/distill into an operator-authored fact when wording is unclear", "文言が曖昧なら operator-authored fact に edit/distill")},
			{key: "v", description: Localize("View evidence and artifact refs", "evidence と artifact refs を表示")},
			{key: "q", description: Localize("Finish review and apply queued decisions", "review を終了し予約済み判断を適用")},
			{description: Localize("Accept checklist: factual, stable, useful later, scoped correctly, evidence-backed, not duplicate/stale.", "Accept checklist: 事実で安定、将来有用、scope が正しい、evidence あり、重複/古さなし。")},
		}
		if len(m.memoryReview.items) > 1 {
			actions = append(actions, cockpitAction{key: "↑/↓", description: Localize("Navigate candidates", "candidate を移動")})
		}
		return actions
	}
}

func (m cockpitModel) liveLocalHelp() string {
	parts := []string{Localize("r refresh", "r 再取得"), Localize("f follow", "f follow")}
	if m.live.follow {
		parts[1] = Localize("f pause", "f 停止")
	}
	if len(m.live.events) > 0 {
		parts = append(parts, Localize("enter detail", "enter 詳細"))
		if len(m.live.events) > 1 {
			parts = append(parts, Localize("↑/↓ select", "↑/↓ 選択"))
		}
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) doctorLocalHelp() string {
	parts := []string{Localize("r refresh", "r 再取得")}
	if len(m.doctorLines()) > 3 {
		parts = append(parts, Localize("↑/↓ scroll", "↑/↓ scroll"))
	}
	return strings.Join(parts, " · ")
}

func (m cockpitModel) detailLocalHelp() string {
	if len(m.detailLines()) > 1 {
		return Localize("↑/↓ scroll", "↑/↓ scroll")
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
		return Localize("q finish review", "q review 終了")
	}
	switch m.memoryReview.review.mode {
	case reviewModeEdit:
		return Localize("enter commit · esc cancel · backspace edit", "enter 確定 · esc キャンセル · backspace 編集")
	case reviewModeViewEvidence:
		return Localize("v/esc close evidence · q finish/apply", "v/esc evidence を閉じる · q 終了/適用")
	case reviewModeHelp:
		return Localize("?/esc close help · q finish/apply", "?/esc help を閉じる · q 終了/適用")
	default:
		return Localize("a accept as-is · x reject · s skip · e edit/distill · v evidence · q finish/apply", "a accept as-is · x reject · s skip · e edit/distill · v evidence · q 終了/適用")
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
