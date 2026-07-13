package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
	appusecase "github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

const (
	doctorStatusPass = "pass"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
	doctorStatusSkip = "skip"

	doctorSeverityPass = "PASS"
	doctorSeverityWarn = "WARN"
	doctorSeverityFail = "FAIL"

	defaultDoctorCoverageThreshold = 0.5
	doctorEventCoverageScanLimit   = 500
	doctorEventCoverageMinSample   = 3
)

type doctorCheck struct {
	Name             string        `json:"name"`
	Status           string        `json:"status"`
	Severity         string        `json:"severity"`
	Section          string        `json:"section"`
	Message          string        `json:"message"`
	Hint             string        `json:"hint"`
	FixCommand       string        `json:"fix_command"`
	AutoFixAvailable bool          `json:"auto_fix_available"`
	FixFunc          doctorFixFunc `json:"-"`
}

type doctorFixFunc func(context.Context, bool) (string, error)

type doctorFixLog struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	Before string `json:"before"`
	After  string `json:"after"`
	Error  string `json:"error,omitempty"`
}

type doctorSection struct {
	Name   string        `json:"name"`
	Checks []doctorCheck `json:"checks"`
}

type doctorSummary struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
}

type doctorReport struct {
	DBPath   string          `json:"db_path"`
	Clients  []string        `json:"clients"`
	Checks   []doctorCheck   `json:"checks"`
	Sections []doctorSection `json:"sections"`
	Summary  doctorSummary   `json:"summary"`
	ExitCode int             `json:"exit_code"`
	Fixes    []doctorFixLog  `json:"fixes,omitempty"`
}

type doctorExitError struct {
	message  string
	exitCode int
}

func (e doctorExitError) Error() string { return e.message }
func (e doctorExitError) ExitCode() int { return e.exitCode }

func (c *RootCLI) newDoctorCommand() *cobra.Command {
	var (
		dbPath            string
		client            string
		projectDir        string
		asJSON            bool
		fix               bool
		dryRun            bool
		strict            bool
		warningsOK        bool
		coverageThreshold float64
	)

	doctorCmd := &cobra.Command{
		Use:     "doctor",
		Aliases: []string{"status"},
		Short:   Localize("Diagnose Traceary DB and hooks configuration", "Traceary の DB と hooks 設定を診断する"),
		Args:    noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runDoctor(cmd.Context(), cmd.OutOrStdout(), doctorCommandInput{
				dbPath:            dbPath,
				client:            client,
				projectDir:        projectDir,
				currentVersion:    cmd.Root().Version,
				asJSON:            asJSON,
				fix:               fix,
				dryRun:            dryRun,
				strict:            strict,
				warningsOK:        warningsOK,
				coverageThreshold: coverageThreshold,
			})
		},
	}
	doctorCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	doctorCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	doctorCmd.Flags().StringVar(&projectDir, "project-dir", "", Localize("project directory used for client config checks", "client 設定チェックに使う project directory"))
	doctorCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	doctorCmd.Flags().BoolVar(&fix, "fix", false, Localize("apply known safe remediations for warning and failing checks", "警告・失敗チェックに対して既知の安全な修復を適用する"))
	doctorCmd.Flags().BoolVar(&dryRun, "dry-run", false, Localize("preview --fix actions without writing files", "ファイルを書き込まずに --fix の処理をプレビューする"))
	doctorCmd.Flags().BoolVar(&strict, "strict", false, Localize("audit-reliability / content-event-reliability: report every exact duplicate group regardless of time, not only near-simultaneous writes", "audit-reliability / content-event-reliability: 時間に関係なく完全一致する duplicate group をすべて報告する（near-simultaneous な書き込みだけに限定しない）"))
	doctorCmd.Flags().BoolVar(&warningsOK, "warnings-ok", false, Localize("exit 0 when doctor finds warnings but no failures (for CI/smoke automation)", "doctor が警告のみを見つけた場合は exit 0 にする（CI / smoke automation 向け）"))
	doctorCmd.Flags().Float64Var(&coverageThreshold, "coverage-threshold", defaultDoctorCoverageThreshold, Localize("client event coverage: warn when the recent prompt/transcript-missing session ratio is above this value (0.0 to 1.0)", "client event coverage: recent session の prompt/transcript 欠落比率がこの値を超えたら警告する (0.0 から 1.0)"))

	return doctorCmd
}

func (c *RootCLI) runDoctor(ctx context.Context, output io.Writer, input doctorCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.New(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
	}
	if err := validateDoctorCoverageThreshold(input.coverageThreshold); err != nil {
		return err
	}

	report, err := c.buildDoctorReport(ctx, input)
	if err != nil {
		return err
	}
	if input.fix {
		fixes := c.applyDoctorFixes(ctx, report, input.dryRun)
		after, err := c.buildDoctorReport(ctx, input)
		if err != nil {
			return err
		}
		annotateDoctorFixesAfter(fixes, after)
		after.Fixes = fixes
		report = after
	}
	if err := writeDoctorReport(output, report, input.asJSON, input.warningsOK); err != nil {
		return err
	}
	if report.ExitCode != 0 {
		message := Localize("doctor found warning checks", "doctor で警告のチェックがあります")
		if report.ExitCode == 1 {
			message = Localize("doctor found failing checks", "doctor で失敗したチェックがあります")
		}
		return doctorExitError{message: message, exitCode: report.ExitCode}
	}

	return nil
}

func validateDoctorCoverageThreshold(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return xerrors.Errorf(
			"%s: %g",
			Localize("--coverage-threshold must be between 0.0 and 1.0", "--coverage-threshold は 0.0 から 1.0 の範囲で指定してください"),
			value,
		)
	}
	return nil
}

func (c *RootCLI) applyDoctorFixes(ctx context.Context, report *doctorReport, dryRun bool) []doctorFixLog {
	if report == nil {
		return nil
	}
	fixes := []doctorFixLog{}
	for _, check := range report.Checks {
		if check.Severity != doctorSeverityWarn && check.Severity != doctorSeverityFail {
			continue
		}
		log := doctorFixLog{Name: check.Name, Before: check.Status}
		if !check.AutoFixAvailable || check.FixFunc == nil {
			log.Action = guidedDoctorFixAction(check)
			fixes = append(fixes, log)
			continue
		}
		action, err := check.FixFunc(ctx, dryRun)
		log.Action = action
		if err != nil {
			log.Error = err.Error()
		}
		fixes = append(fixes, log)
	}
	return fixes
}

func guidedDoctorFixAction(check doctorCheck) string {
	if check.FixCommand != "" {
		return "skip: guided remediation only; run `" + check.FixCommand + "`"
	}
	return "skip: no automatic remediation is available"
}

func annotateDoctorFixesAfter(fixes []doctorFixLog, report *doctorReport) {
	if report == nil {
		return
	}
	statusByName := map[string]string{}
	for _, check := range report.Checks {
		statusByName[check.Name] = check.Status
	}
	for i := range fixes {
		if status, ok := statusByName[fixes[i].Name]; ok {
			fixes[i].After = status
		}
	}
}

func (c *RootCLI) buildDoctorReport(ctx context.Context, input doctorCommandInput) (*doctorReport, error) {
	resolvedClients, err := resolveDoctorClients(c, input.client)
	if err != nil {
		return nil, err
	}

	report := &doctorReport{
		Clients: resolvedClients,
		Checks:  make([]doctorCheck, 0, 8),
	}
	defer func() {
		finalizeDoctorReport(report, input.warningsOK)
	}()

	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:    "db-path",
			Status:  doctorStatusFail,
			Message: localizef("failed to resolve DB path: %v", "DB パスの解決に失敗しました: %v", err),
		})
		return report, nil
	}
	c.applyDatabasePath(resolvedDBPath)
	report.DBPath = resolvedDBPath
	report.Checks = append(report.Checks, doctorCheck{
		Name:    "db-path",
		Status:  doctorStatusPass,
		Message: localizef("resolved DB path: %s", "解決した DB パス: %s", resolvedDBPath),
	})
	report.Checks = append(report.Checks, inspectTracearyOnPath())

	report.Checks = append(report.Checks, inspectDoctorConfig())

	if err := c.storeManagement.Initialize(ctx); err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:    "db-write",
			Status:  doctorStatusFail,
			Message: localizef("failed to initialize the SQLite store: %v", "SQLite ストアの初期化に失敗しました: %v", err),
		})
	} else {
		report.Checks = append(report.Checks, doctorCheck{
			Name:    "db-write",
			Status:  doctorStatusPass,
			Message: localizef("initialized SQLite store: %s", "SQLite ストアを初期化しました: %s", resolvedDBPath),
		})
		report.Checks = append(report.Checks, c.inspectStaleActiveSessions(ctx))
		report.Checks = append(report.Checks, c.inspectCommandAuditReliability(ctx, input.strict))
		report.Checks = append(report.Checks, c.inspectContentEventReliability(ctx, input.strict))
	}

	resolvedProjectDir, err := resolveHooksProjectDir(input.projectDir)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:    "project-dir",
			Status:  doctorStatusFail,
			Message: localizef("failed to resolve project directory: %v", "project directory の解決に失敗しました: %v", err),
		})
		return report, nil
	}
	report.Checks = append(report.Checks, doctorCheck{
		Name:    "project-dir",
		Status:  doctorStatusPass,
		Message: localizef("resolved project directory: %s", "解決した project directory: %s", resolvedProjectDir),
	})
	report.Checks = append(report.Checks, inspectHookSpoolDiagnostics(resolvedClients))
	report.Checks = append(report.Checks, inspectHookMemoryExtractDiagnostics(time.Now().UTC()))

	for _, targetClient := range resolvedClients {
		if targetClient == "antigravity" {
			// Antigravity supports three independent hook install routes
			// (workspace .agents/hooks.json, user-level
			// ~/.gemini/config/hooks.json, and the `agy` CLI plugin). Each is
			// optional on its own, so a missing workspace file must not warn
			// when another route is healthy. inspectAntigravityHookRoutes
			// reports each route separately plus an aggregate summary that
			// carries the actionable install message when no route is healthy.
			report.Checks = append(report.Checks, inspectAntigravityCapability())
			report.Checks = append(report.Checks, c.inspectAntigravityHookRoutes(resolvedProjectDir)...)
			// Capture levels are a host-mode trait reported separately from route
			// install health above, so doctor does not imply full transcript
			// capture just because the hooks are installed (headless agy --print
			// emits no Stop, so its final turn is unavailable).
			report.Checks = append(report.Checks, buildAntigravityCaptureLevelsCheck())
			continue
		}

		outputPath, pathErr := c.hooksOrchestrator.ResolveInstallPath(targetClient, resolvedProjectDir, types.None[string]())
		if pathErr != nil {
			report.Checks = append(report.Checks, doctorCheck{
				Name:    targetClient + "-config",
				Status:  doctorStatusFail,
				Message: localizef("failed to resolve %s config path: %v", "%s の設定パス解決に失敗しました: %v", targetClient, pathErr),
			})
			continue
		}

		check := c.inspectClaudeOrConfigFile(ctx, targetClient, outputPath, resolvedProjectDir)
		c.attachDoctorConfigFix(&check, targetClient, outputPath, resolvedProjectDir)
		report.Checks = append(report.Checks, check)
		if targetClient == "gemini" || targetClient == "claude" {
			report.Checks = append(report.Checks, c.inspectClientEventCoverage(ctx, targetClient, outputPath, resolvedProjectDir, input.coverageThreshold))
		}
		if targetClient == "claude" {
			report.Checks = append(report.Checks, c.inspectClaudeHookCancellationDiagnostics(ctx, resolvedProjectDir))
		}
		report.Checks = append(report.Checks, c.inspectMCPRegistrationForClient(targetClient, outputPath))

		if globalCheck := c.inspectGlobalConfigForClient(targetClient); globalCheck != nil {
			report.Checks = append(report.Checks, *globalCheck)
		}

		if targetClient == "claude" {
			if cacheCheck := c.inspectClaudePluginCacheStatus(); cacheCheck != nil {
				report.Checks = append(report.Checks, *cacheCheck)
			}
		}

		report.Checks = append(report.Checks, inspectHostCapabilityGaps(targetClient, outputPath)...)
		if targetClient == "codex" {
			if activationCheck := c.inspectCodexMemoryActivationStatus(ctx, resolvedProjectDir); activationCheck != nil {
				report.Checks = append(report.Checks, *activationCheck)
			}
		}
		if targetClient == "claude" {
			if activationCheck := c.inspectClaudeMemoryActivationStatus(ctx, resolvedProjectDir); activationCheck != nil {
				report.Checks = append(report.Checks, *activationCheck)
			}
		}
		if targetClient == "gemini" {
			if activationCheck := c.inspectGeminiMemoryActivationStatus(ctx, resolvedProjectDir); activationCheck != nil {
				report.Checks = append(report.Checks, *activationCheck)
			}
		}
	}

	report.Checks = append(report.Checks, c.inspectPluginVersionChecks(input.currentVersion)...)
	report.Checks = append(report.Checks, checkLatestVersion(input.currentVersion))

	return report, nil
}

// inspectStaleActiveSessions reports how many unended sessions are
// older than the default stale threshold (24h). Stale active sessions
// silently shadow host context retrieval (top default view, session
// handoff implicit selection, MCP session_status), so doctor surfaces a
// count plus the actionable cleanup command. The check uses
// CloseStaleSessions with dryRun=true so it never mutates state; an
// underlying query error is reported as fail with the original error.
func (c *RootCLI) inspectStaleActiveSessions(ctx context.Context) doctorCheck {
	const checkName = "stale-active-sessions"
	if c.storeManagement == nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusSkip,
			Message: localizef("store management usecase is not configured", "ストア管理ユースケースが設定されていません"),
		}
	}
	result, err := c.storeManagement.CloseStaleSessions(ctx, defaultActiveSessionStaleAfter, true)
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to count stale active sessions: %v", "stale active session の集計に失敗しました: %v", err),
		}
	}
	count := result.ClosedCount()
	if count <= 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"no active sessions older than %s",
				"%s を超える active session はありません",
				defaultActiveSessionStaleAfter,
			),
		}
	}
	fixCommand := "traceary session gc --stale-after 24h"
	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint: Localize(
			"preview the cleanup with `traceary session gc --stale-after 24h --dry-run`, then drop --dry-run to close them",
			"`traceary session gc --stale-after 24h --dry-run` で確認後、--dry-run を外して終了処理を実行してください",
		),
		FixCommand: fixCommand,
		Message: localizef(
			"%d active session(s) older than %s; they shadow the default host context retrieval. Close them with `%s` (use --dry-run first to preview).",
			"%d 件の active session が %s を超えており、host context 取得の既定動作を阻害します。`%s` で終了処理を実行できます (まず --dry-run でプレビュー推奨)。",
			count,
			defaultActiveSessionStaleAfter,
			fixCommand,
		),
	}
}

const commandAuditReliabilityScanLimit = 200

// commandAuditDuplicateProximityWindow bounds how close in time two
// identity-matching command audits must be to count as a likely hook
// double-write (versus an intentional operator re-run). Genuine hook
// duplicates land near-simultaneously (the write-side guard suppresses exact
// repeats within 2s); intentional re-runs of the same command in a review/merge
// flow are minutes apart. This window sits an order of magnitude above the 2s
// write guard (to absorb clock skew and slow writes) yet far below the
// minute-scale spacing of deliberate re-runs, so the default diagnostic stays
// actionable. `traceary doctor --strict` ignores this window and reports every
// exact duplicate group for forensic analysis.
const commandAuditDuplicateProximityWindow = 10 * time.Second

type commandAuditReliabilityFindings struct {
	ScannedAuditCount     int
	DuplicateGroups       []commandAuditDuplicateGroup
	WorkspaceDriftSamples []commandAuditWorkspaceDriftSample
}

type commandAuditDuplicateGroup struct {
	EventIDs []string
	Count    int
}

type commandAuditDuplicateGroupKey struct {
	Client          string
	Agent           string
	SessionID       string
	Workspace       string
	Command         string
	Input           string
	Output          string
	InputTruncated  bool
	OutputTruncated bool
	ExitCode        string
	Failed          bool
}

type commandAuditWorkspaceDriftSample struct {
	EventID           string
	StoredWorkspace   string
	EvidenceWorkspace string
	CWD               string
}

func (c *RootCLI) inspectCommandAuditReliability(ctx context.Context, strict bool) doctorCheck {
	const checkName = "audit-reliability"
	if c.event == nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusSkip,
			Message: localizef("event usecase is not configured", "event usecase が設定されていません"),
		}
	}
	events, err := c.event.List(ctx, apptypes.NewEventListCriteriaBuilder(commandAuditReliabilityScanLimit).
		Kind(types.EventKindCommandExecuted).
		Build())
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to list recent command audits: %v", "recent command audit の取得に失敗しました: %v", err),
		}
	}
	details := make([]apptypes.EventDetails, 0, len(events))
	for _, event := range events {
		detail, err := c.event.Show(ctx, event.EventID())
		if err != nil {
			return doctorCheck{
				Name:    checkName,
				Status:  doctorStatusFail,
				Message: localizef("failed to inspect command audit %s: %v", "command audit %s の検査に失敗しました: %v", event.EventID(), err),
			}
		}
		details = append(details, detail)
	}
	return commandAuditReliabilityCheckFromFindings(commandAuditReliabilityFindingsFromDetails(ctx, details, strict), strict)
}

func commandAuditReliabilityCheckFromFindings(findings commandAuditReliabilityFindings, strict bool) doctorCheck {
	const checkName = "audit-reliability"
	duplicateRecordCount := 0
	for _, group := range findings.DuplicateGroups {
		duplicateRecordCount += group.Count
	}
	if len(findings.DuplicateGroups) == 0 && len(findings.WorkspaceDriftSamples) == 0 {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent command audit(s); no duplicate groups or workspace-drift candidates found",
				"%d 件の recent command audit を検査しました。duplicate group / workspace drift candidate はありません",
				findings.ScannedAuditCount,
			),
		}
	}

	hint := Localize(
		"likely hook duplicates (identity-matching audits within "+commandAuditDuplicateProximityWindow.String()+"); intentional re-runs minutes apart are excluded. Re-run with --strict to surface every exact duplicate group, then inspect with `traceary show <event_id>`",
		"hook 由来とみられる duplicate（"+commandAuditDuplicateProximityWindow.String()+" 以内の identity 一致 audit）です。数分離れた意図的な re-run は除外されます。完全一致する duplicate group をすべて見るには --strict を付け、`traceary show <event_id>` で確認してください",
	)
	if strict {
		hint = Localize(
			"--strict: every exact duplicate group is reported regardless of time gap, so intentional re-runs appear too; inspect the sampled event IDs with `traceary show <event_id>` before drawing process conclusions",
			"--strict: 時間差に関係なく完全一致する duplicate group をすべて報告します（意図的な re-run も含みます）。process の結論を出す前に sample event ID を `traceary show <event_id>` で確認してください",
		)
	}

	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint:   hint,
		Message: localizef(
			"scanned %d recent command audit(s); duplicate_groups=%d duplicate_records=%d workspace_drift_candidates=%d samples: duplicates=[%s] drift=[%s]",
			"%d 件の recent command audit を検査しました。duplicate_groups=%d duplicate_records=%d workspace_drift_candidates=%d samples: duplicates=[%s] drift=[%s]",
			findings.ScannedAuditCount,
			len(findings.DuplicateGroups),
			duplicateRecordCount,
			len(findings.WorkspaceDriftSamples),
			formatCommandAuditDuplicateSamples(findings.DuplicateGroups),
			formatCommandAuditWorkspaceDriftSamples(findings.WorkspaceDriftSamples),
		),
	}
}

func commandAuditReliabilityFindingsFromDetails(ctx context.Context, details []apptypes.EventDetails, strict bool) commandAuditReliabilityFindings {
	findings := commandAuditReliabilityFindings{}
	groups := map[commandAuditDuplicateGroupKey][]commandAuditDuplicateRecord{}
	for _, detail := range details {
		event := detail.Event()
		audit, ok := detail.CommandAudit().Value()
		if event == nil || !ok || audit == nil {
			continue
		}
		findings.ScannedAuditCount++
		key := newCommandAuditDuplicateGroupKey(event, audit)
		groups[key] = append(groups[key], commandAuditDuplicateRecord{
			eventID:   event.EventID().String(),
			createdAt: event.CreatedAt(),
		})

		if drift, ok := commandAuditWorkspaceDriftFromDetail(ctx, event, audit); ok {
			findings.WorkspaceDriftSamples = append(findings.WorkspaceDriftSamples, drift)
		}
	}
	for _, records := range groups {
		findings.DuplicateGroups = append(findings.DuplicateGroups, commandAuditDuplicateGroupsFromRecords(records, strict)...)
	}
	sort.Slice(findings.DuplicateGroups, func(i, j int) bool {
		return findings.DuplicateGroups[i].EventIDs[0] < findings.DuplicateGroups[j].EventIDs[0]
	})
	sort.Slice(findings.WorkspaceDriftSamples, func(i, j int) bool {
		return findings.WorkspaceDriftSamples[i].EventID < findings.WorkspaceDriftSamples[j].EventID
	})
	return findings
}

// commandAuditDuplicateRecord is one identity-matching audit considered for
// duplicate grouping, carrying the timestamp used for time-proximity clustering.
type commandAuditDuplicateRecord struct {
	eventID   string
	createdAt time.Time
}

// commandAuditDuplicateGroupsFromRecords turns the identity-matching records of
// a single group key into reportable duplicate groups. In strict mode any group
// of 2+ exact matches is reported regardless of time. By default the records are
// clustered by time proximity (consecutive records within
// commandAuditDuplicateProximityWindow) so that only near-simultaneous writes —
// the likely hook duplicates — are reported, and intentional re-runs minutes
// apart are excluded.
func commandAuditDuplicateGroupsFromRecords(records []commandAuditDuplicateRecord, strict bool) []commandAuditDuplicateGroup {
	if len(records) <= 1 {
		return nil
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].createdAt.Equal(records[j].createdAt) {
			return records[i].createdAt.Before(records[j].createdAt)
		}
		return records[i].eventID < records[j].eventID
	})

	groupFromRun := func(run []commandAuditDuplicateRecord) (commandAuditDuplicateGroup, bool) {
		if len(run) < 2 {
			return commandAuditDuplicateGroup{}, false
		}
		ids := make([]string, len(run))
		for i, record := range run {
			ids[i] = record.eventID
		}
		sort.Strings(ids)
		return commandAuditDuplicateGroup{EventIDs: ids, Count: len(ids)}, true
	}

	if strict {
		if group, ok := groupFromRun(records); ok {
			return []commandAuditDuplicateGroup{group}
		}
		return nil
	}

	var groups []commandAuditDuplicateGroup
	run := []commandAuditDuplicateRecord{records[0]}
	for _, record := range records[1:] {
		if record.createdAt.Sub(run[len(run)-1].createdAt) <= commandAuditDuplicateProximityWindow {
			run = append(run, record)
			continue
		}
		if group, ok := groupFromRun(run); ok {
			groups = append(groups, group)
		}
		run = []commandAuditDuplicateRecord{record}
	}
	if group, ok := groupFromRun(run); ok {
		groups = append(groups, group)
	}
	return groups
}

func newCommandAuditDuplicateGroupKey(event *model.Event, audit *model.CommandAudit) commandAuditDuplicateGroupKey {
	exitCode := "-"
	if value, ok := audit.ExitCode().Value(); ok {
		exitCode = strconv.Itoa(value)
	}
	return commandAuditDuplicateGroupKey{
		Client:          event.Client().String(),
		Agent:           event.Agent().String(),
		SessionID:       event.SessionID().String(),
		Workspace:       event.Workspace().String(),
		Command:         audit.Command(),
		Input:           audit.Input(),
		Output:          audit.Output(),
		InputTruncated:  audit.InputTruncated(),
		OutputTruncated: audit.OutputTruncated(),
		ExitCode:        exitCode,
		Failed:          audit.Failed(),
	}
}

func commandAuditWorkspaceDriftFromDetail(ctx context.Context, event *model.Event, audit *model.CommandAudit) (commandAuditWorkspaceDriftSample, bool) {
	cwd, ok := commandAuditInputCWD(audit.Input())
	if !ok {
		return commandAuditWorkspaceDriftSample{}, false
	}
	evidenceWorkspace := commandAuditWorkspaceEvidenceFromCWD(ctx, cwd)
	storedWorkspace := event.Workspace().String()
	if storedWorkspace == "" || evidenceWorkspace == "" || storedWorkspace == evidenceWorkspace {
		return commandAuditWorkspaceDriftSample{}, false
	}
	return commandAuditWorkspaceDriftSample{
		EventID:           event.EventID().String(),
		StoredWorkspace:   storedWorkspace,
		EvidenceWorkspace: evidenceWorkspace,
		CWD:               cwd,
	}, true
}

func commandAuditInputCWD(input string) (string, bool) {
	var value any
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		return "", false
	}
	return findCWDInJSONValue(value)
}

func findCWDInJSONValue(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				if cwd, ok := findCWDInJSONValue(item); ok {
					return cwd, true
				}
			}
		}
		return "", false
	}
	for _, key := range []string{"cwd", "workdir", "working_directory"} {
		if raw, ok := object[key]; ok {
			if cwd, ok := raw.(string); ok && strings.TrimSpace(cwd) != "" {
				return strings.TrimSpace(cwd), true
			}
		}
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if cwd, ok := findCWDInJSONValue(object[key]); ok {
			return cwd, true
		}
	}
	return "", false
}

func commandAuditWorkspaceEvidenceFromCWD(ctx context.Context, cwd string) string {
	trimmed := strings.TrimSpace(cwd)
	if trimmed == "" {
		return ""
	}
	if workspace, err := detectRepoContextFromDir(ctx, trimmed); err == nil && strings.TrimSpace(workspace) != "" {
		return workspace
	}
	return normalizeLocalWorkContextPath(trimmed)
}

func formatCommandAuditDuplicateSamples(groups []commandAuditDuplicateGroup) string {
	if len(groups) == 0 {
		return "-"
	}
	limit := len(groups)
	if limit > 3 {
		limit = 3
	}
	parts := make([]string, 0, limit)
	for _, group := range groups[:limit] {
		eventIDs := group.EventIDs
		if len(eventIDs) > 4 {
			eventIDs = eventIDs[:4]
		}
		parts = append(parts, fmt.Sprintf("count=%d event_ids=%s", group.Count, strings.Join(eventIDs, ",")))
	}
	return strings.Join(parts, "; ")
}

func formatCommandAuditWorkspaceDriftSamples(samples []commandAuditWorkspaceDriftSample) string {
	if len(samples) == 0 {
		return "-"
	}
	limit := len(samples)
	if limit > 3 {
		limit = 3
	}
	parts := make([]string, 0, limit)
	for _, sample := range samples[:limit] {
		parts = append(parts, fmt.Sprintf(
			"event_id=%s stored=%q cwd_workspace=%q",
			sample.EventID,
			sample.StoredWorkspace,
			sample.EvidenceWorkspace,
		))
	}
	return strings.Join(parts, "; ")
}

func (c *RootCLI) inspectClientEventCoverage(ctx context.Context, client, outputPath, projectDir string, threshold float64) doctorCheck {
	checkName := client + "-event-coverage"
	if c.event == nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusSkip,
			Message: localizef("event usecase is not configured", "event usecase が設定されていません"),
		}
	}

	workspace := resolveDoctorEventCoverageWorkspace(ctx, projectDir)
	criteria := apptypes.NewEventListCriteriaBuilder(doctorEventCoverageScanLimit).
		Agent(types.Agent(client))
	if workspace.String() != "" {
		criteria.Workspace(workspace)
	}
	events, err := c.event.List(ctx, criteria.Build())
	if err != nil {
		return doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to list recent %s events: %v", "recent %s event の取得に失敗しました: %v", client, err),
		}
	}

	inputs := make([]appusecase.EventCoverageInput, 0, len(events))
	for _, event := range events {
		if event == nil {
			continue
		}
		inputs = append(inputs, appusecase.EventCoverageInput{
			SessionID: event.SessionID().String(),
			Kind:      event.Kind(),
		})
	}
	coverage := appusecase.SummarizeSessionEventCoverage(inputs)
	ratio := coverage.PromptTranscriptMissingRatio()
	if coverage.Sessions < doctorEventCoverageMinSample {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent %s event(s); only %d complete session(s) observed (minimum sample %d), so event coverage is not judged yet",
				"%d 件の recent %s event を検査しました。完全に観測できた session は %d 件だけです (minimum sample %d) のため、event coverage はまだ判定しません",
				len(events),
				client,
				coverage.Sessions,
				doctorEventCoverageMinSample,
			),
		}
	}
	if ratio <= threshold {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"scanned %d recent %s event(s); prompt/transcript coverage is healthy (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d ratio=%.2f threshold=%.2f)",
				"%d 件の recent %s event を検査しました。prompt/transcript coverage は健全です (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d ratio=%.2f threshold=%.2f)",
				len(events),
				client,
				coverage.Sessions,
				coverage.PromptTranscriptMissing,
				coverage.Complete,
				coverage.Enriched,
				coverage.WithPrompt,
				coverage.WithTranscript,
				coverage.WithCommand,
				ratio,
				threshold,
			),
		}
	}

	configCoverage, configCoverageKnown, pluginManaged, pluginKey := c.managedCoverageForEventCoverage(outputPath, client)
	if configCoverageKnown {
		if missing := configCoverage.MissingEnrichment(); len(missing) > 0 {
			if pluginManaged {
				updateCommand := claudePluginUpdateCommand(pluginKey)
				return doctorCheck{
					Name:       checkName,
					Status:     doctorStatusWarn,
					FixCommand: updateCommand,
					Hint: localizef(
						"the plugin-managed Claude hook config is missing %s coverage; update the Claude plugin instead of writing project settings hooks",
						"plugin-managed Claude hook config に %s coverage がありません。project settings hook を書き込まず Claude plugin を更新してください",
						strings.Join(missing, ", "),
					),
					Message: localizef(
						"scanned %d recent %s event(s); %.0f%% of complete sessions lack prompt/transcript coverage (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%). The plugin-managed config is missing Traceary-managed %s hooks",
						"%d 件の recent %s event を検査しました。完全に観測できた session の %.0f%% で prompt/transcript coverage が不足しています (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%)。plugin-managed config に Traceary 管理の %s hook がありません",
						len(events),
						client,
						ratio*100,
						coverage.Sessions,
						coverage.PromptTranscriptMissing,
						coverage.Complete,
						coverage.Enriched,
						threshold*100,
						strings.Join(missing, ", "),
					),
				}
			}
			fixCommand := fmt.Sprintf("traceary doctor --client %s --project-dir %s --fix", client, shellQuote(projectDir))
			return doctorCheck{
				Name:       checkName,
				Status:     doctorStatusWarn,
				FixCommand: fixCommand,
				Hint: localizef(
					"the installed hook config is missing %s coverage; run `%s` to refresh Traceary-managed hooks without touching user hooks",
					"インストール済み hook config に %s coverage がありません。`%s` で Traceary 管理 hook を更新できます (user hook は保持)",
					strings.Join(missing, ", "),
					fixCommand,
				),
				Message: localizef(
					"scanned %d recent %s event(s); %.0f%% of complete sessions lack prompt/transcript coverage (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%). The installed config is missing Traceary-managed %s hooks: %s",
					"%d 件の recent %s event を検査しました。完全に観測できた session の %.0f%% で prompt/transcript coverage が不足しています (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d, threshold=%.0f%%)。インストール済み config に Traceary 管理の %s hook がありません: %s",
					len(events),
					client,
					ratio*100,
					coverage.Sessions,
					coverage.PromptTranscriptMissing,
					coverage.Complete,
					coverage.Enriched,
					threshold*100,
					strings.Join(missing, ", "),
					outputPath,
				),
			}
		}
	}

	hint := Localize(
		fmt.Sprintf("hook config appears to include enrichment coverage; inspect hook timeouts, host hook cancellations, host behavior, and recent event IDs with `traceary list --agent %s`", client),
		fmt.Sprintf("hook config には enrichment coverage が含まれているようです。hook timeout、host 側の hook cancellation、host 側の挙動、recent event ID を `traceary list --agent %s` で確認してください", client),
	)
	if !configCoverageKnown {
		if pluginManaged {
			updateCommand := claudePluginUpdateCommand(pluginKey)
			hint = localizef(
				"Claude hooks appear to be plugin-managed by %q; project settings hooks are intentionally absent. Inspect plugin cache/update state (`%s`), host hook cancellations/timeouts, and recent event IDs with `traceary list --agent %s`",
				"Claude hooks は plugin %q によって管理されているようです。project settings hook がないのは意図された状態です。plugin cache/update 状態 (`%s`)、host 側の hook cancellation/timeout、recent event ID を `traceary list --agent %s` で確認してください",
				pluginKey,
				updateCommand,
				client,
			)
		} else {
			hint = localizef(
				"the project hook config could not be inspected; first fix %s-config, then rerun doctor",
				"project hook config を検査できませんでした。先に %s-config を修復してから doctor を再実行してください",
				client,
			)
		}
	}
	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint:   hint,
		Message: localizef(
			"scanned %d recent %s event(s); %.0f%% of complete sessions lack prompt/transcript coverage (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d, threshold=%.0f%%)",
			"%d 件の recent %s event を検査しました。完全に観測できた session の %.0f%% で prompt/transcript coverage が不足しています (sessions=%d prompt_transcript_missing=%d complete=%d enriched=%d with_prompt=%d with_transcript=%d with_command=%d, threshold=%.0f%%)",
			len(events),
			client,
			ratio*100,
			coverage.Sessions,
			coverage.PromptTranscriptMissing,
			coverage.Complete,
			coverage.Enriched,
			coverage.WithPrompt,
			coverage.WithTranscript,
			coverage.WithCommand,
			threshold*100,
		),
	}
}

func resolveDoctorEventCoverageWorkspace(ctx context.Context, projectDir string) types.Workspace {
	if explicit := resolveExplicitWorkspaceValue(""); explicit != "" {
		return types.Workspace(explicit)
	}
	if detected, err := detectRepoContextFromDir(ctx, projectDir); err == nil {
		return types.Workspace(detected)
	}
	return types.Workspace(normalizeLocalWorkContextPath(projectDir))
}

func (c *RootCLI) managedCoverageForConfigFile(path string, client string) (application.HookManagedCoverage, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return application.HookManagedCoverage{}, false
	}
	coverage, err := c.hooksInspector.ManagedCoverage(content, client)
	if err != nil {
		return application.HookManagedCoverage{}, false
	}
	return coverage, true
}

func (c *RootCLI) managedCoverageForEventCoverage(outputPath, client string) (application.HookManagedCoverage, bool, bool, string) {
	if client == "claude" {
		detection := c.detectClaudeTracearyPluginForCLI()
		if detection.Active {
			coverage, known := c.managedCoverageForInstalledClaudePlugin(detection.PluginKey)
			return coverage, known, true, detection.PluginKey
		}
	}
	coverage, known := c.managedCoverageForConfigFile(outputPath, client)
	return coverage, known, false, ""
}

func (c *RootCLI) managedCoverageForInstalledClaudePlugin(pluginKey string) (application.HookManagedCoverage, bool) {
	result := c.inspectInstalledClaudePluginHookCoverage(pluginKey)
	return result.coverage, result.known
}

type installedClaudePluginHookCoverage struct {
	coverage  application.HookManagedCoverage
	known     bool
	cachePath string
}

func (c *RootCLI) inspectInstalledClaudePluginHookCoverage(pluginKey string) installedClaudePluginHookCoverage {
	if c.pluginCacheInspector == nil || pluginKey == "" {
		return installedClaudePluginHookCoverage{}
	}
	home, err := userHomeDirFunc()
	if err != nil {
		return installedClaudePluginHookCoverage{}
	}
	status := c.pluginCacheInspector.DetectClaudePluginCacheStatus(home, pluginKey)
	if status.CachePath != "" && status.CachedVersion != "" {
		cacheHooksPath := filepath.Join(status.CachePath, status.CachedVersion, "hooks", "hooks.json")
		if coverage, known := c.managedCoverageForConfigFile(cacheHooksPath, "claude"); known {
			return installedClaudePluginHookCoverage{coverage: coverage, known: true, cachePath: cacheHooksPath}
		}
		return installedClaudePluginHookCoverage{cachePath: cacheHooksPath}
	}
	if status.MarketplacePath != "" {
		pluginRoot := filepath.Dir(filepath.Dir(status.MarketplacePath))
		marketplaceHooksPath := filepath.Join(pluginRoot, "hooks", "hooks.json")
		if coverage, known := c.managedCoverageForConfigFile(marketplaceHooksPath, "claude"); known {
			return installedClaudePluginHookCoverage{coverage: coverage, known: true}
		}
	}
	return installedClaudePluginHookCoverage{}
}

func claudePluginUpdateCommand(pluginKey string) string {
	if pluginKey == "" {
		return "claude plugins update"
	}
	return "claude plugins update " + pluginKey
}

// inspectClaudePluginCacheStatus compares the cached Traceary Claude
// Code plugin version against the marketplace manifest the plugin was
// registered from. A stale cache is the exact failure mode v0.8.0
// dogfooding revealed: `brew upgrade traceary` does not refresh the
// plugin cache, so new hooks (#606 transcript / #605 matcher expansion)
// stay dark until the operator runs `claude plugins update`. The check
// returns nil when the plugin is not active or when either side cannot
// be resolved (reported indirectly by the existing claude-config check).

func (c *RootCLI) attachDoctorConfigFix(check *doctorCheck, client, outputPath, projectDir string) {
	if check == nil || check.Name != client+"-config" || check.Status != doctorStatusWarn {
		return
	}
	if check.AutoFixAvailable || check.FixFunc != nil {
		return
	}
	if client == "claude" {
		detection := c.detectClaudeTracearyPluginForCLI()
		if detection.Active {
			return
		}
	}
	check.AutoFixAvailable = true
	check.FixFunc = func(ctx context.Context, dryRun bool) (string, error) {
		action := fmt.Sprintf("upgrade Traceary-managed %s hooks at %s", client, outputPath)
		if dryRun {
			return "would: " + action, nil
		}
		_, _, err := c.hooksOrchestrator.UpgradeWithMatcher(ctx, client, "traceary", projectDir, types.Some(outputPath), "")
		if err != nil {
			return action, xerrors.Errorf("failed to upgrade Traceary-managed hooks: %w", err)
		}
		return action, nil
	}
}

func (c *RootCLI) inspectClaudePluginCacheStatus() *doctorCheck {
	detection := c.detectClaudeTracearyPluginForCLI()
	if !detection.Active {
		return nil
	}
	if c.pluginCacheInspector == nil {
		return nil
	}
	home, err := userHomeDirFunc()
	if err != nil {
		return nil
	}
	status := c.pluginCacheInspector.DetectClaudePluginCacheStatus(home, detection.PluginKey)
	if status.CachedVersion == "" || status.MarketplaceVersion == "" {
		return nil
	}
	// Evaluate the two WARN conditions together so a single cache
	// that is BOTH stale AND has multiple cached versions gets one
	// unified message — otherwise the stale branch would return
	// first and the #670 fresh-session remedy would be silently
	// dropped.
	staleSegment, multiSegment := "", ""
	if status.Stale() {
		staleSegment = localizef(
			"claude plugin cache %s is older than the marketplace manifest (cached %s, marketplace %s at %s). `brew upgrade traceary` does not refresh the plugin cache; run `claude plugins update %s` to activate the newer hooks",
			"claude plugin cache %s は marketplace manifest より古いバージョンです (cached %s, marketplace %s at %s)。`brew upgrade traceary` では plugin cache は更新されません。新しい hook を有効にするには `claude plugins update %s` を実行してください",
			status.CachePath, status.CachedVersion, status.MarketplaceVersion, status.MarketplacePath, detection.PluginKey,
		)
	}
	if status.HasMultipleCachedVersions() {
		others := strings.Join(status.CachedVersions[1:], ", ")
		multiSegment = localizef(
			"claude plugin cache %s has multiple cached versions (current %s, older also present: %s). A resumed Claude Code session (via `--continue` / cmux) can still be running the older snapshot. Fully restart Claude Code (no resume) so the new hooks fire; optionally remove `%s/<old>` to remove the ambiguity",
			"claude plugin cache %s に複数のバージョンが残っています (current %s、古いもの: %s)。`--continue` で resume されたセッションでは古いスナップショットの hook が動き続ける可能性があります。完全に再起動 (resume しない) すれば新しい hook が有効になります。古い subdir (`%s/<old>`) を削除すれば恒久的に解消します",
			status.CachePath, status.CachedVersion, others, status.CachePath,
		)
	}
	if staleSegment != "" || multiSegment != "" {
		var message string
		switch {
		case staleSegment != "" && multiSegment != "":
			message = staleSegment + " " + multiSegment
		case staleSegment != "":
			message = staleSegment
		default:
			message = multiSegment
		}
		return &doctorCheck{
			Name:    "claude-plugin-cache",
			Status:  doctorStatusWarn,
			Message: message,
		}
	}
	return &doctorCheck{
		Name:   "claude-plugin-cache",
		Status: doctorStatusPass,
		Message: localizef(
			"claude plugin cache matches the marketplace manifest (cached %s, marketplace %s)",
			"claude plugin cache は marketplace manifest と一致しています (cached %s, marketplace %s)",
			status.CachedVersion, status.MarketplaceVersion,
		),
	}
}

// inspectHostCapabilityGaps surfaces informational notes about host
// capabilities that Traceary does not yet wire into its managed hook set.
// The checks intentionally return pass status with descriptive messages so
// the operator sees the gap without treating it as a regression — the
// content was verified against each host's bundled hook reference docs
// (gemini-cli 0.43.0 as of v0.21.0).
func inspectHostCapabilityGaps(client, configPath string) []doctorCheck {
	switch client {
	case "claude":
		return []doctorCheck{{
			Name:   "claude-host-capabilities",
			Status: doctorStatusPass,
			Message: localizef(
				"claude host: SubagentStop and PreCompact hooks are wired into the Traceary-managed config alongside the existing SessionStart / SessionEnd / Stop / PostCompact coverage (hooks config: %s)",
				"claude ホスト: SubagentStop と PreCompact は既存の SessionStart / SessionEnd / Stop / PostCompact と並んで Traceary 管理の hook config に組み込み済みです (hooks config: %s)",
				configPath,
			),
		}}
	case "codex":
		codexConfigPath := describeCodexConfigPath()
		return []doctorCheck{{
			Name:   "codex-host-capabilities",
			Status: doctorStatusPass,
			Message: localizef(
				"codex host: memory features ship behind a per-install feature flag in %s; consult the Codex release notes for the exact flag name and your enablement state. Traceary's `memory import codex` works regardless of the flag state",
				"codex ホスト: memory 機能は per-install な feature flag (%s) の背後にあります。flag 名と有効化状態の確認方法は Codex のリリースノートを参照してください。Traceary は flag 状態に関わらず `memory import codex` で取り込み可能です",
				codexConfigPath,
			),
		}}
	case "gemini":
		return []doctorCheck{
			{
				Name:   "gemini-host-capabilities",
				Status: doctorStatusPass,
				Message: localizef(
					"gemini host: memory manager agent and auto-memory remain experimental features on Gemini CLI (verified against 0.43.0); Traceary's Tier 3 hook coverage (SessionStart / SessionEnd / BeforeAgent / AfterAgent / AfterTool / PreCompress) does not yet surface those experimental signals (hooks config: %s)",
					"gemini ホスト: memory manager agent / auto-memory は Gemini CLI の experimental 機能です (0.43.0 で確認済)。Traceary の Tier 3 hook (SessionStart / SessionEnd / BeforeAgent / AfterAgent / AfterTool / PreCompress) は現時点でそれらの experimental 信号を surface しません (hooks config: %s)",
					configPath,
				),
			},
			{
				Name:   "gemini-compact-coverage",
				Status: doctorStatusPass,
				Message: Localize(
					"gemini host: compact summaries are captured at the pre-compact boundary only (PreCompress marker). Gemini CLI exposes no post-compress hook — PreCompress is advisory-only and fires asynchronously before compression (verified against the gemini-cli 0.43.0 hook reference) — so a missing post-compact digest is expected upstream behavior, not a broken install",
					"gemini ホスト: compact summary は pre-compact 境界のみで捕捉します (PreCompress marker)。Gemini CLI に post-compress hook は存在せず、PreCompress は compression 前に非同期で発火する advisory-only hook です (gemini-cli 0.43.0 の hook reference で確認済)。post-compact digest が無いのは upstream の想定挙動であり、インストール不良ではありません",
				),
			},
		}
	}
	return nil
}

// describeCodexConfigPath returns the canonical ~/.codex/config.toml path
// the message should reference. The helper falls back to the literal
// tilde-prefixed form when the user's home directory cannot be resolved,
// so the message never leaks an empty path and still points the operator
// at the right file.
func describeCodexConfigPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "~/.codex/config.toml"
	}
	return filepath.Join(homeDir, ".codex", "config.toml")
}

func resolveDoctorClients(c *RootCLI, client string) ([]string, error) {
	if strings.TrimSpace(client) == "" {
		return []string{"claude", "codex", "gemini"}, nil
	}

	// antigravity is a doctor-only diagnostic client: it has no hook
	// install path and is not registered with the hooks orchestrator.
	if strings.EqualFold(strings.TrimSpace(client), "antigravity") {
		return []string{"antigravity"}, nil
	}

	resolvedClient, err := c.hooksOrchestrator.NormalizeClient(client)
	if err != nil {
		return nil, xerrors.Errorf("failed to normalize client: %w", err)
	}

	return []string{resolvedClient}, nil
}

// inspectDoctorConfig inspects the optional Traceary config file
// (~/.config/traceary/config.json) and returns a doctorCheck describing
// the outcome. The function keeps all filesystem logic inlined here so
// the presentation layer does not need a dedicated "Config" data carrier.
func inspectDoctorConfig() doctorCheck {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusFail,
			Message: localizef("failed to resolve the config path, so config-backed features fall back to built-in defaults: %v", "設定ファイルのパスを解決できないため、config 由来の機能は組み込み既定値にフォールバックします: %v", err),
		}
	}

	configPath := filepath.Join(homeDir, ".config", "traceary", "config.json")
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return doctorCheck{
				Name:    "config",
				Status:  doctorStatusPass,
				Message: localizef("optional config file is not present yet; built-in defaults remain active: %s", "オプション設定ファイルはまだありません。組み込みの既定値を使います: %s", configPath),
			}
		}
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusFail,
			Message: localizef("config file could not be read, so config-backed features fall back to built-in defaults: %s (%v)", "設定ファイルを読み込めないため、config 由来の機能は組み込み既定値にフォールバックします: %s (%v)", configPath, readErr),
		}
	}

	var root map[string]json.RawMessage
	if unmarshalErr := json.Unmarshal(data, &root); unmarshalErr != nil {
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusFail,
			Message: localizef("config file is invalid JSON, so config-backed features fall back to built-in defaults: %s (%v)", "設定ファイルの JSON が不正なため、config 由来の機能は組み込み既定値にフォールバックします: %s (%v)", configPath, unmarshalErr),
		}
	}

	return doctorCheck{
		Name:    "config",
		Status:  doctorStatusPass,
		Message: localizef("loaded config file: %s", "設定ファイルを読み込みました: %s", configPath),
	}
}

// inspectClaudeOrConfigFile routes the Claude client through plugin
// detection (so we can report double-registration or plugin-managed
// pass states) and falls back to the generic config-file inspection
// for every other client.
//
// Even when the plugin is active, a malformed project settings.json is
// still a real problem — Claude Code itself will reject it. We always
// run the file-level inspection first and only short-circuit to the
// plugin-managed pass branch when the file is either missing or
// structurally valid.
func (c *RootCLI) inspectClaudeOrConfigFile(ctx context.Context, client, outputPath, projectDir string) doctorCheck {
	if client != "claude" {
		return c.inspectDoctorConfigFile(ctx, client, outputPath, projectDir)
	}

	detection := c.detectClaudeTracearyPluginForCLI()
	configCheck := c.inspectDoctorConfigFile(ctx, client, outputPath, projectDir)

	if detection.Active {
		// Structural failures (invalid JSON, malformed hooks field) are
		// reported as-is so `doctor` still surfaces a broken file even
		// when the plugin would otherwise claim the hooks.
		if configCheck.Status == doctorStatusFail {
			return configCheck
		}
		if c.claudeConfigHasTracearyHooks(outputPath) {
			return doctorCheck{
				Name:   "claude-config",
				Status: doctorStatusWarn,
				Hint:   "choose one registration path: remove Traceary hooks from settings.json or disable the Claude plugin",
				Message: localizef(
					"claude plugin %q is active in %s and %s also registers Traceary hooks. Every audit event will be recorded twice — remove the settings.json hooks or disable the plugin",
					"claude plugin %q が %s で有効ですが %s にも Traceary hook が登録されています。audit が二重記録されます — settings.json 側の hook を削除するか plugin を無効化してください",
					detection.PluginKey,
					detection.SettingsPath,
					outputPath,
				),
			}
		}
		pluginCoverage := c.inspectInstalledClaudePluginHookCoverage(detection.PluginKey)
		if pluginCoverage.known {
			if missing := pluginCoverage.coverage.MissingEnrichment(); len(missing) > 0 {
				updateCommand := claudePluginUpdateCommand(detection.PluginKey)
				return doctorCheck{
					Name:       "claude-config",
					Status:     doctorStatusWarn,
					FixCommand: updateCommand,
					Hint: localizef(
						"the plugin-managed Claude hook config is missing %s coverage; update the Claude plugin instead of writing project settings hooks",
						"plugin-managed Claude hook config に %s coverage がありません。project settings hook を書き込まず Claude plugin を更新してください",
						strings.Join(missing, ", "),
					),
					Message: localizef(
						"claude hooks are delivered by plugin %q (%s), but the installed plugin hook config is missing Traceary-managed enrichment coverage (%s). Update the plugin with `%s`",
						"claude hooks は plugin %q によって提供されています (%s) が、installed plugin hook config に Traceary 管理の enrichment coverage (%s) がありません。`%s` で plugin を更新してください",
						detection.PluginKey,
						detection.SettingsPath,
						strings.Join(missing, ", "),
						updateCommand,
					),
				}
			}
		}
		if !pluginCoverage.known && pluginCoverage.cachePath != "" {
			updateCommand := claudePluginUpdateCommand(detection.PluginKey)
			return doctorCheck{
				Name:       "claude-config",
				Status:     doctorStatusWarn,
				FixCommand: updateCommand,
				Hint: localizef(
					"the plugin-managed Claude hook config could not be inspected at %s; update the Claude plugin instead of trusting marketplace source hooks",
					"plugin-managed Claude hook config を %s で検査できませんでした。marketplace source hook ではなく Claude plugin を更新してください",
					pluginCoverage.cachePath,
				),
				Message: localizef(
					"claude hooks are delivered by plugin %q (%s), but the installed cached hook config could not be inspected at %s. Update the plugin with `%s`",
					"claude hooks は plugin %q によって提供されています (%s) が、installed cache の hook config を %s で検査できませんでした。`%s` で plugin を更新してください",
					detection.PluginKey,
					detection.SettingsPath,
					pluginCoverage.cachePath,
					updateCommand,
				),
			}
		}
		return doctorCheck{
			Name:   "claude-config",
			Status: doctorStatusPass,
			Message: localizef(
				"claude hooks are delivered by plugin %q (%s); no settings.json install is required",
				"claude の hooks は plugin %q によって提供されています (%s)。settings.json への install は不要です",
				detection.PluginKey,
				detection.SettingsPath,
			),
		}
	}

	if configCheck.Status == doctorStatusWarn {
		pluginCheck := inspectDoctorPluginPackage(projectDir)
		if pluginCheck.Status == doctorStatusPass {
			return pluginCheck
		}
	}
	return configCheck
}

// globalConfigLocationForClient returns the directory under $HOME where a
// given client stores its user-level settings file. Codex already lives
// under ~/.codex via the default install path so we report no separate
// global check for it.
func globalConfigLocationForClient(client string) (relDir, fileName string, ok bool) {
	switch client {
	case "claude":
		return ".claude", "settings.json", true
	case "gemini":
		return ".gemini", "settings.json", true
	}
	return "", "", false
}

// inspectGlobalConfigForClient reports whether the client's user-level
// hook settings file contains Traceary-managed hooks. A missing file is
// reported as skip — the typical state for users who either use the
// plugin or install per-project. Clients without a distinct global
// config (e.g. Codex, whose default install is already user-level)
// return nil and are omitted from the doctor report.
func (c *RootCLI) inspectGlobalConfigForClient(client string) *doctorCheck {
	relDir, fileName, ok := globalConfigLocationForClient(client)
	if !ok {
		return nil
	}
	checkName := client + "-global-config"

	home, err := userHomeDirFunc()
	if err != nil {
		return &doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to resolve home directory for global %s config: %v", "global %s config のホーム解決に失敗しました: %v", client, err),
		}
	}
	if !filepath.IsAbs(home) {
		return &doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("refusing to inspect global %s config because resolved home is not absolute: %q", "解決されたホームが絶対パスではないため global %s config を検査できません: %q", client, home),
		}
	}
	globalPath := filepath.Join(home, relDir, fileName)
	content, err := os.ReadFile(globalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &doctorCheck{
				Name:    checkName,
				Status:  doctorStatusSkip,
				Message: localizef("global %s config not present (skipped): %s", "global %s config はありません (skip): %s", client, globalPath),
			}
		}
		return &doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("failed to read global %s config: %v", "global %s config の読み込みに失敗しました: %v", client, err),
		}
	}

	_, hasTracearyHook, inspectErr := c.hooksInspector.Inspect(content)
	if inspectErr != nil {
		return &doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("global %s config is not a valid hooks-shaped JSON object: %s", "global %s config は hooks 形式の JSON として解釈できません: %s", client, globalPath),
		}
	}
	if duplicates, duplicateErr := c.hooksInspector.DuplicateManagedHooks(content); duplicateErr != nil {
		return &doctorCheck{
			Name:    checkName,
			Status:  doctorStatusFail,
			Message: localizef("global %s config is not a valid hooks-shaped JSON object: %s", "global %s config は hooks 形式の JSON として解釈できません: %s", client, globalPath),
		}
	} else if len(duplicates) > 0 {
		return &doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Hint: Localize(
				"remove the duplicate Traceary-managed entries manually or regenerate the global hooks after reviewing the file; keep non-Traceary hooks",
				"ファイルを確認した上で Traceary 管理の重複エントリだけを手動削除するか、global hooks を再生成してください。Traceary 以外の hook は残してください",
			),
			Message: localizef(
				"global %s config registers duplicate Traceary-managed hooks (%s). Matching host events can be recorded more than once: %s",
				"global %s config に Traceary 管理 hook の重複があります (%s)。該当する host event が複数回記録される可能性があります: %s",
				client,
				formatHookDuplicateSummary(duplicates),
				globalPath,
			),
		}
	}
	if hasTracearyHook {
		if client == "gemini" || client == "claude" {
			coverage, coverageErr := c.hooksInspector.ManagedCoverage(content, client)
			if coverageErr != nil {
				return &doctorCheck{
					Name:    checkName,
					Status:  doctorStatusFail,
					Message: localizef("global %s config is not a valid hooks-shaped JSON object: %s", "global %s config は hooks 形式の JSON として解釈できません: %s", client, globalPath),
				}
			}
			if missing := coverage.MissingEnrichment(); len(missing) > 0 {
				check := missingClientHookCoverageCheck(client, checkName, globalPath, home, missing, false)
				return &check
			}
		}
		return &doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"global %s config contains Traceary-managed hooks (applies to every project): %s",
				"global %s config に Traceary 管理下の hook があります (全プロジェクトで有効): %s",
				client,
				globalPath,
			),
		}
	}
	return &doctorCheck{
		Name:    checkName,
		Status:  doctorStatusSkip,
		Message: localizef("global %s config exists but has no Traceary-managed hooks: %s", "global %s config はありますが Traceary 管理下の hook はありません: %s", client, globalPath),
	}
}

// claudeConfigHasTracearyHooks returns true iff the Claude settings file
// at outputPath is a valid JSON object with a hooks field that contains
// at least one Traceary-managed hook entry. Missing files, unreadable
// files, and malformed JSON all return false so the plugin-detection
// branch interprets them as "no Traceary hook registered here".
func (c *RootCLI) claudeConfigHasTracearyHooks(outputPath string) bool {
	content, err := os.ReadFile(outputPath)
	if err != nil {
		return false
	}
	_, hasTracearyHook, err := c.hooksInspector.Inspect(content)
	if err != nil {
		return false
	}
	return hasTracearyHook
}

func (c *RootCLI) inspectDoctorConfigFile(ctx context.Context, client string, outputPath string, projectDir string) doctorCheck {
	content, err := os.ReadFile(outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			if client == "codex" {
				if state := c.detectCodexPluginHookFallback(); state.pluginHooksConfirmedActive() {
					return codexPluginManagedHooksCheck(state, outputPath)
				} else if state.PluginEnabled {
					return codexPluginHookFallbackCheck(state, outputPath, localizef("does not exist", "が存在しません"))
				}
			}
			return doctorCheck{
				Name:   client + "-config",
				Status: doctorStatusWarn,
				Message: localizef(
					"%s config file does not exist yet (run `traceary hooks install --client %s` to fix) (first-run / pre-install state): %s",
					"%s の設定ファイルはまだありません (`traceary hooks install --client %s` で設定できます) (first-run / pre-install 状態): %s",
					client,
					client,
					outputPath,
				),
			}
		}

		return doctorCheck{
			Name:    client + "-config",
			Status:  doctorStatusFail,
			Message: localizef("failed to read %s config file: %v", "%s の設定ファイル読み込みに失敗しました: %v", client, err),
		}
	}

	hasHooksField, hasTracearyManagedHook, inspectErr := c.hooksInspector.Inspect(content)
	if inspectErr != nil {
		if errors.Is(inspectErr, application.ErrHookConfigInvalidHooksField) {
			return doctorCheck{
				Name:    client + "-config",
				Status:  doctorStatusFail,
				Message: localizef("%s hooks field must be an object of hook arrays: %s", "%s の hooks フィールドは hook 配列を値に持つ object である必要があります: %s", client, outputPath),
			}
		}
		return doctorCheck{
			Name:    client + "-config",
			Status:  doctorStatusFail,
			Message: localizef("%s config file must be a JSON object: %s", "%s の設定ファイルは JSON object である必要があります: %s", client, outputPath),
		}
	}

	if !hasHooksField {
		if client == "codex" {
			if state := c.detectCodexPluginHookFallback(); state.pluginHooksConfirmedActive() {
				return codexPluginManagedHooksCheck(state, outputPath)
			} else if state.PluginEnabled {
				return codexPluginHookFallbackCheck(state, outputPath, localizef("has no hooks field", "には hooks フィールドがありません"))
			}
		}
		return doctorCheck{
			Name:   client + "-config",
			Status: doctorStatusWarn,
			Message: localizef(
				"%s config exists but does not contain a hooks field yet (run `traceary hooks install --client %s` to fix): %s",
				"%s の設定はありますが hooks フィールドはまだありません (`traceary hooks install --client %s` で設定できます): %s",
				client,
				client,
				outputPath,
			),
		}
	}

	if hasTracearyManagedHook {
		if client == "codex" {
			if state := c.detectCodexPluginHookFallback(); state.pluginHooksConfirmedActive() {
				return c.codexDuplicateRegistrationCheck(ctx, state, outputPath)
			}
		}
		duplicates, duplicateErr := c.hooksInspector.DuplicateManagedHooks(content)
		if duplicateErr != nil {
			return doctorCheck{
				Name:    client + "-config",
				Status:  doctorStatusFail,
				Message: localizef("%s config file must be a hooks-shaped JSON object: %s", "%s の設定ファイルは hooks 形式の JSON object である必要があります: %s", client, outputPath),
			}
		}
		if len(duplicates) > 0 {
			return duplicateTracearyHookCheck(client, client+"-config", outputPath, projectDir, duplicates)
		}
		if client == "codex" {
			if missing := c.missingTracearyManagedCodexEvents(content); len(missing) > 0 {
				return doctorCheck{
					Name:   client + "-config",
					Status: doctorStatusWarn,
					Message: localizef(
						"codex config is missing Traceary-managed events (%s); prompt/transcript gaps starve durable-memory extraction. Run `traceary hooks install --client codex --upgrade` to fix: %s",
						"codex の設定に Traceary 管理下の event が不足しています (%s)。prompt/transcript が欠けると durable-memory extraction に入力が渡りません。`traceary hooks install --client codex --upgrade` で修復できます: %s",
						strings.Join(missing, ", "),
						outputPath,
					),
				}
			}
		}
		if client == "gemini" || client == "claude" {
			coverage, coverageErr := c.hooksInspector.ManagedCoverage(content, client)
			if coverageErr != nil {
				return doctorCheck{
					Name:    client + "-config",
					Status:  doctorStatusFail,
					Message: localizef("%s config file must be a hooks-shaped JSON object: %s", "%s の設定ファイルは hooks 形式の JSON object である必要があります: %s", client, outputPath),
				}
			}
			if missing := coverage.MissingEnrichment(); len(missing) > 0 {
				return missingClientHookCoverageCheck(client, client+"-config", outputPath, projectDir, missing, true)
			}
		}
		return doctorCheck{
			Name:    client + "-config",
			Status:  doctorStatusPass,
			Message: localizef("%s config contains Traceary-managed hooks: %s", "%s の設定には Traceary 管理下の hook があります: %s", client, outputPath),
		}
	}

	if client == "codex" {
		if state := c.detectCodexPluginHookFallback(); state.pluginHooksConfirmedActive() {
			return codexPluginManagedHooksCheck(state, outputPath)
		} else if state.PluginEnabled {
			return codexPluginHookFallbackCheck(state, outputPath, localizef("has hook entries but none are Traceary-managed", "には hook エントリはありますが Traceary 管理のものがありません"))
		}
	}

	return doctorCheck{
		Name:   client + "-config",
		Status: doctorStatusWarn,
		Message: localizef(
			"%s config exists but no Traceary-managed hook was found yet (run `traceary hooks install --client %s` to fix): %s",
			"%s の設定はありますが Traceary 管理下の hook はまだ見つかっていません (`traceary hooks install --client %s` で設定できます): %s",
			client,
			client,
			outputPath,
		),
	}
}

func duplicateTracearyHookCheck(client, checkName, outputPath, projectDir string, duplicates []application.HookDuplicate) doctorCheck {
	summary := formatHookDuplicateSummary(duplicates)
	dryRunCommand := fmt.Sprintf("traceary doctor --fix --dry-run --client %s --project-dir %s", client, shellQuote(projectDir))
	return doctorCheck{
		Name:       checkName,
		Status:     doctorStatusWarn,
		FixCommand: dryRunCommand,
		Hint: localizef(
			"preview cleanup first with `%s`; the repair refreshes only Traceary-managed entries and preserves non-Traceary hooks",
			"`%s` で先に cleanup をプレビューしてください。修復は Traceary 管理のエントリだけを更新し、Traceary 以外の hook は保持します",
			dryRunCommand,
		),
		Message: localizef(
			"%s config registers duplicate Traceary-managed hooks (%s). Matching host events can be recorded more than once. Preview the non-destructive cleanup with `%s`: %s",
			"%s の設定に Traceary 管理 hook の重複があります (%s)。該当する host event が複数回記録される可能性があります。`%s` で非破壊 cleanup をプレビューしてください: %s",
			client,
			summary,
			dryRunCommand,
			outputPath,
		),
	}
}

func missingClientHookCoverageCheck(client, checkName, outputPath, projectDir string, missing []string, projectConfig bool) doctorCheck {
	joinedMissing := strings.Join(missing, ", ")
	fixCommand := fmt.Sprintf("traceary doctor --fix --dry-run --client %s --project-dir %s", client, shellQuote(projectDir))
	hint := localizef(
		"preview the non-destructive refresh with `%s`; it rewrites only Traceary-managed entries and preserves non-Traceary hooks",
		"`%s` で非破壊 refresh をプレビューしてください。Traceary 管理エントリだけを更新し、Traceary 以外の hook は保持します",
		fixCommand,
	)
	if !projectConfig {
		fixCommand = fmt.Sprintf("traceary hooks install --client %s --global --upgrade", client)
		if client == "gemini" {
			hint = localizef(
				"refresh the user-level Gemini settings with `%s`; if you use the Gemini extension package instead, run `gemini extensions update traceary`",
				"`%s` で user-level Gemini settings を更新してください。Gemini extension package を使っている場合は `gemini extensions update traceary` を実行してください",
				fixCommand,
			)
		} else {
			hint = localizef(
				"refresh the user-level %s settings with `%s`; if the host uses plugin-managed hooks instead, update the plugin package",
				"user-level %s settings を `%s` で更新してください。host が plugin-managed hooks を使う場合は plugin package を更新してください",
				client,
				fixCommand,
			)
		}
	}
	return doctorCheck{
		Name:       checkName,
		Status:     doctorStatusWarn,
		FixCommand: fixCommand,
		Hint:       hint,
		Message: localizef(
			"%s config contains Traceary-managed hooks but is missing enrichment coverage (%s). Prompt/transcript gaps leave sessions without conversation coverage; refresh the managed hooks: %s",
			"%s config に Traceary 管理 hook はありますが enrichment coverage (%s) が不足しています。prompt/transcript が欠けると session に会話内容の coverage が残りません。管理 hook を更新してください: %s",
			client,
			joinedMissing,
			outputPath,
		),
	}
}

func formatHookDuplicateSummary(duplicates []application.HookDuplicate) string {
	parts := make([]string, 0, len(duplicates))
	for _, duplicate := range duplicates {
		matcher := duplicate.Matcher
		if matcher == "" {
			matcher = "<default>"
		}
		parts = append(parts, fmt.Sprintf("%s matcher=%q key=%s count=%d", duplicate.Event, matcher, duplicate.ManagedKey, duplicate.Count))
	}
	return strings.Join(parts, "; ")
}

// codexManagedEvents is the canonical list of hook events Traceary installs
// into Codex CLI. Doctor uses this to flag a partial install that predates
// the v0.7 UserPromptSubmit rollout.
var codexManagedEvents = []string{"SessionStart", "UserPromptSubmit", "Stop", "PostToolUse"}

// codexManagedEventKeys maps each expected Codex event to the stable managed
// key the Traceary hook runtime installs for it. The key is reused as the
// single source of truth when comparing hooks.json entries against a real
// Traceary install — a substring check on the command string would
// misclassify user-managed commands that happen to contain "hook" and
// "codex".
var codexManagedEventKeys = map[string][]string{
	"SessionStart":     []string{"traceary-session.sh:codex:start"},
	"UserPromptSubmit": []string{"traceary-prompt.sh:codex"},
	"Stop":             []string{"traceary-transcript.sh:codex", "traceary-session.sh:codex:stop"},
	"PostToolUse":      []string{"traceary-audit.sh:codex"},
}

// missingTracearyManagedCodexEvents returns the subset of Traceary-managed
// Codex events that do not have a Traceary-managed hook entry in the given
// hooks.json content. Unknown / non-object JSON shapes are reported as an
// empty slice so the outer inspector branch (which already checked hook
// shape) remains authoritative.
func (c *RootCLI) missingTracearyManagedCodexEvents(content []byte) []string {
	var root struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(content, &root); err != nil {
		return nil
	}
	missing := make([]string, 0, len(codexManagedEvents))
	for _, event := range codexManagedEvents {
		expectedKeys, ok := codexManagedEventKeys[event]
		if !ok {
			continue
		}
		raw, present := root.Hooks[event]
		if !present {
			missing = append(missing, event)
			continue
		}
		if !c.hasEntriesWithManagedKeys(raw, expectedKeys) {
			missing = append(missing, event)
		}
	}
	return missing
}

// hasEntriesWithManagedKeys reports whether the given hook-event entries
// contain commands for every expected Traceary managed key. Empty key sets
// always return false so the caller cannot accidentally match user-managed
// commands.
func (c *RootCLI) hasEntriesWithManagedKeys(raw json.RawMessage, expectedKeys []string) bool {
	if len(expectedKeys) == 0 {
		return false
	}
	if c.hooksInspector == nil {
		return false
	}
	var entries []struct {
		Hooks []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return false
	}
	remaining := make(map[string]struct{}, len(expectedKeys))
	for _, expectedKey := range expectedKeys {
		if expectedKey == "" {
			return false
		}
		remaining[expectedKey] = struct{}{}
	}
	for _, entry := range entries {
		for _, h := range entry.Hooks {
			if h.Type != "command" {
				continue
			}
			// Use the Name-aware extractor so entries installed via
			// `--traceary-bin <non-traceary-basename>` (dev builds)
			// are still recognized through the `traceary-*` Name.
			delete(remaining, c.hooksInspector.ExtractManagedKeyFromEntry(h.Name, h.Command))
		}
	}
	return len(remaining) == 0
}

func inspectDoctorPluginPackage(projectDir string) doctorCheck {
	pluginHooksPath := filepath.Join(projectDir, "integrations", "claude-plugin", "hooks", "hooks.json")
	content, err := os.ReadFile(pluginHooksPath)
	if err != nil {
		return doctorCheck{
			Name:   "claude-config",
			Status: doctorStatusWarn,
			Message: localizef(
				"claude plugin package not found at %s",
				"claude plugin パッケージが見つかりません: %s",
				pluginHooksPath,
			),
		}
	}

	var hooksMap map[string]json.RawMessage
	if err := json.Unmarshal(content, &hooksMap); err != nil {
		return doctorCheck{
			Name:   "claude-config",
			Status: doctorStatusWarn,
			Message: localizef(
				"claude plugin hooks.json is not valid JSON: %s",
				"claude plugin の hooks.json が不正な JSON です: %s",
				pluginHooksPath,
			),
		}
	}

	if _, ok := hooksMap["hooks"]; !ok {
		return doctorCheck{
			Name:   "claude-config",
			Status: doctorStatusWarn,
			Message: localizef(
				"claude plugin hooks.json does not contain a hooks field: %s",
				"claude plugin の hooks.json に hooks フィールドがありません: %s",
				pluginHooksPath,
			),
		}
	}

	return doctorCheck{
		Name:   "claude-config",
		Status: doctorStatusPass,
		Message: localizef(
			"claude hooks are managed by plugin package: %s",
			"claude の hooks は plugin パッケージで管理されています: %s",
			pluginHooksPath,
		),
	}
}

func writeDoctorReport(output io.Writer, report *doctorReport, asJSON bool, warningsOK bool) error {
	if report == nil {
		return xerrors.New(Localize("doctor report must not be nil", "doctor report は nil にできません"))
	}
	finalizeDoctorReport(report, warningsOK)

	if asJSON {
		return writeJSON(output, report)
	}

	if _, err := fmt.Fprintln(output, "TRACEARY DOCTOR"); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print doctor header", "doctor ヘッダーの出力に失敗しました"), err)
	}
	if report.DBPath != "" {
		if _, err := fmt.Fprintf(output, "DB_PATH: %s\n", report.DBPath); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print DB path", "DB パスの出力に失敗しました"), err)
		}
	}
	if _, err := fmt.Fprintf(output, "SUMMARY: pass=%d warn=%d fail=%d exit_code=%d\n", report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.ExitCode); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to print doctor summary", "doctor サマリーの出力に失敗しました"), err)
	}
	if len(report.Fixes) > 0 {
		if _, err := fmt.Fprintln(output, "\nFixes"); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print doctor fixes", "doctor 修復結果の出力に失敗しました"), err)
		}
		for _, fix := range report.Fixes {
			line := fmt.Sprintf("- %s: %s (before=%s after=%s)", fix.Name, fix.Action, fix.Before, fix.After)
			if fix.Error != "" {
				line += ": " + fix.Error
			}
			if _, err := fmt.Fprintln(output, line); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print doctor fix", "doctor 修復結果の出力に失敗しました"), err)
			}
		}
	}

	for _, section := range report.Sections {
		if len(section.Checks) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(output, "\n%s\n", section.Name); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print doctor section", "doctor セクションの出力に失敗しました"), err)
		}
		for _, check := range section.Checks {
			if _, err := fmt.Fprintf(output, "[%s] %s: %s\n", check.Severity, check.Name, check.Message); err != nil {
				return xerrors.Errorf("%s: %w", Localize("failed to print doctor check", "doctor チェックの出力に失敗しました"), err)
			}
			if check.Hint != "" {
				if _, err := fmt.Fprintf(output, "  hint: %s\n", check.Hint); err != nil {
					return xerrors.Errorf("%s: %w", Localize("failed to print doctor check hint", "doctor チェックヒントの出力に失敗しました"), err)
				}
			}
		}
	}

	return nil
}

func finalizeDoctorReport(report *doctorReport, warningsOK bool) {
	if report == nil {
		return
	}
	for i := range report.Checks {
		applyDoctorSeverity(&report.Checks[i])
		report.Checks[i].Section = doctorSectionNameForCheck(report.Checks[i].Name)
	}
	report.Summary = doctorSummary{}
	for _, check := range report.Checks {
		switch check.Severity {
		case doctorSeverityFail:
			report.Summary.Fail++
		case doctorSeverityWarn:
			report.Summary.Warn++
		default:
			report.Summary.Pass++
		}
	}
	switch {
	case report.Summary.Fail > 0:
		report.ExitCode = 1
	case report.Summary.Warn > 0:
		if warningsOK {
			report.ExitCode = 0
		} else {
			report.ExitCode = 2
		}
	default:
		report.ExitCode = 0
	}
	report.Sections = buildDoctorSections(report.Checks)
}

func applyDoctorSeverity(check *doctorCheck) {
	if check == nil {
		return
	}
	if check.Status == "" {
		check.Status = doctorStatusPass
	}
	switch check.Status {
	case doctorStatusFail:
		check.Severity = doctorSeverityFail
	case doctorStatusWarn:
		check.Severity = doctorSeverityWarn
	default:
		check.Severity = doctorSeverityPass
	}
}

func buildDoctorSections(checks []doctorCheck) []doctorSection {
	sections := []doctorSection{
		{Name: "Environment", Checks: []doctorCheck{}},
		{Name: "Database", Checks: []doctorCheck{}},
		{Name: "Plugins", Checks: []doctorCheck{}},
		{Name: "MCP", Checks: []doctorCheck{}},
		{Name: "Hooks", Checks: []doctorCheck{}},
	}
	index := map[string]int{}
	for i, section := range sections {
		index[section.Name] = i
	}
	for _, check := range checks {
		sectionName := doctorSectionNameForCheck(check.Name)
		sections[index[sectionName]].Checks = append(sections[index[sectionName]].Checks, check)
	}
	return sections
}

func doctorSectionNameForCheck(name string) string {
	switch {
	case name == "db-path" || name == "db-write" || name == "stale-active-sessions" || name == "audit-reliability" || name == "content-event-reliability":
		return "Database"
	case name == "config" || name == "project-dir" || name == "version" || name == "path":
		return "Environment"
	case strings.Contains(name, "plugin"):
		return "Plugins"
	case strings.HasSuffix(name, "-host-capabilities") || strings.HasSuffix(name, "-mcp") || strings.HasPrefix(name, "mcp"):
		return "MCP"
	default:
		return "Hooks"
	}
}
