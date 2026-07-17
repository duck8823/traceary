package cli

import (
	"context"
	"io"
	"math"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

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
	report.Checks = append(report.Checks, inspectStoreSizeBudget(resolvedDBPath))
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
		report.Checks = append(report.Checks, c.inspectRetryLoops(ctx))
		report.Checks = append(report.Checks, c.inspectSensitiveAccessAuditCoverage(ctx))
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
	report.Checks = append(report.Checks, c.inspectHookSpoolDiagnostics(resolvedClients))
	report.Checks = append(report.Checks, inspectHookMemoryExtractDiagnostics(time.Now().UTC()))
	report.Checks = append(report.Checks, inspectHookGrokTranscriptDiagnostics(time.Now().UTC()))

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
			report.Checks = append(report.Checks, c.inspectAntigravityMCPRegistration())
			// Configured capture levels and observed event coverage are reported
			// separately from route health. A valid hooks file does not prove that
			// transcriptPath was readable or events reached the database.
			report.Checks = append(report.Checks, buildAntigravityCaptureLevelsCheck())
			report.Checks = append(report.Checks, c.inspectAntigravityEventCoverage(ctx, resolvedProjectDir, input.coverageThreshold))
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
		if targetClient == "grok" {
			state, probeErr := probeGrokDoctorState(ctx, resolvedProjectDir)
			if probeErr != nil {
				report.Checks = append(report.Checks, doctorCheck{Name: "grok-inspect", Status: doctorStatusWarn, Message: localizef("failed to inspect Grok installation: %v", "Grok installation の検査に失敗しました: %v", probeErr), Hint: Localize("run `grok inspect --json` and retry doctor after resolving the host error", "`grok inspect --json` のエラーを解消してから doctor を再実行してください")})
			} else {
				report.Checks = append(report.Checks, buildGrokDoctorChecks(state, input.currentVersion)...)
			}
			report.Checks = append(report.Checks, c.inspectClientEventCoverage(ctx, targetClient, outputPath, resolvedProjectDir, input.coverageThreshold))
			continue
		}

		var check doctorCheck
		if targetClient == "codex" {
			pluginState := c.detectCodexPluginHookFallback()
			if pluginState.PluginEnabled {
				trust := codexPluginHookTrustProbeFunc(ctx, resolvedProjectDir, pluginState.PluginKey)
				report.Checks = append(report.Checks, codexPluginHookTrustCheck(trust))
				check = c.inspectCodexConfigWithHookTrust(ctx, outputPath, resolvedProjectDir, trust)
			} else {
				check = c.inspectClaudeOrConfigFile(ctx, targetClient, outputPath, resolvedProjectDir)
			}
		} else {
			check = c.inspectClaudeOrConfigFile(ctx, targetClient, outputPath, resolvedProjectDir)
		}
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
// idle beyond the default stale threshold (24h), using each session
// latest event as activity. Stale active sessions
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
	result, err := c.storeManagement.CloseStaleSessions(ctx, defaultActiveSessionStaleAfter, true, nil)
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
				"no active sessions idle for more than %s",
				"%s を超えて活動のない active session はありません",
				defaultActiveSessionStaleAfter,
			),
		}
	}
	fixCommand := "traceary session gc --stale-after 24h"
	return doctorCheck{
		Name:   checkName,
		Status: doctorStatusWarn,
		Hint: Localize(
			"normal hook starts retry activity-aware cleanup automatically; preview immediately with `traceary session gc --stale-after 24h --dry-run`, then drop --dry-run to close them",
			"通常の hook start 後に activity-aware cleanup が自動再試行されます。すぐ確認する場合は `traceary session gc --stale-after 24h --dry-run` を実行し、終了処理には --dry-run を外してください",
		),
		FixCommand: fixCommand,
		Message: localizef(
			"%d active session(s) have no activity within %s; they shadow the default host context retrieval. Normal hook starts clean them automatically, or run `%s` (use --dry-run first to preview).",
			"%d 件の active session は %s の間活動がなく、host context 取得の既定動作を阻害します。通常の hook start 後に自動 cleanup されます。手動では `%s` を実行できます (まず --dry-run でプレビュー推奨)。",
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
