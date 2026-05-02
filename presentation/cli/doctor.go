package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
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
		dbPath     string
		client     string
		projectDir string
		asJSON     bool
		fix        bool
		dryRun     bool
	)

	doctorCmd := &cobra.Command{
		Use:     "doctor",
		Aliases: []string{"status"},
		Short:   Localize("Diagnose Traceary DB and hooks configuration", "Traceary の DB と hooks 設定を診断する"),
		Args:    noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runDoctor(cmd.Context(), cmd.OutOrStdout(), doctorCommandInput{
				dbPath:         dbPath,
				client:         client,
				projectDir:     projectDir,
				currentVersion: cmd.Root().Version,
				asJSON:         asJSON,
				fix:            fix,
				dryRun:         dryRun,
			})
		},
	}
	doctorCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	doctorCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	doctorCmd.Flags().StringVar(&projectDir, "project-dir", "", Localize("project directory used for client config checks", "client 設定チェックに使う project directory"))
	doctorCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))
	doctorCmd.Flags().BoolVar(&fix, "fix", false, Localize("apply known safe remediations for warning and failing checks", "警告・失敗チェックに対して既知の安全な修復を適用する"))
	doctorCmd.Flags().BoolVar(&dryRun, "dry-run", false, Localize("preview --fix actions without writing files", "ファイルを書き込まずに --fix の処理をプレビューする"))

	return doctorCmd
}

func (c *RootCLI) runDoctor(ctx context.Context, output io.Writer, input doctorCommandInput) error {
	if c.storeManagement == nil {
		return xerrors.Errorf(Localize("initialize store usecase is not configured", "ストア初期化ユースケースが設定されていません"))
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
	if err := writeDoctorReport(output, report, input.asJSON); err != nil {
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
	defer finalizeDoctorReport(report)

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

	for _, targetClient := range resolvedClients {
		outputPath, pathErr := c.hooksOrchestrator.ResolveInstallPath(targetClient, resolvedProjectDir, types.None[string]())
		if pathErr != nil {
			report.Checks = append(report.Checks, doctorCheck{
				Name:    targetClient + "-config",
				Status:  doctorStatusFail,
				Message: localizef("failed to resolve %s config path: %v", "%s の設定パス解決に失敗しました: %v", targetClient, pathErr),
			})
			continue
		}

		check := c.inspectClaudeOrConfigFile(targetClient, outputPath, resolvedProjectDir)
		c.attachDoctorConfigFix(&check, targetClient, outputPath, resolvedProjectDir)
		report.Checks = append(report.Checks, check)
		report.Checks = append(report.Checks, c.inspectMCPRegistrationForClient(targetClient, outputPath))

		if globalCheck := c.inspectGlobalConfigForClient(targetClient); globalCheck != nil {
			report.Checks = append(report.Checks, *globalCheck)
		}

		if targetClient == "claude" {
			if cacheCheck := c.inspectClaudePluginCacheStatus(); cacheCheck != nil {
				report.Checks = append(report.Checks, *cacheCheck)
			}
		}

		if hostCheck := inspectHostCapabilityGaps(targetClient, outputPath); hostCheck != nil {
			report.Checks = append(report.Checks, *hostCheck)
		}
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
	if client == "claude" {
		detection := c.detectClaudeTracearyPluginForCLI()
		if detection.Active && c.claudeConfigHasTracearyHooks(outputPath) {
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

// inspectHostCapabilityGaps surfaces informational notes about 2026 Q2 host
// capabilities that Traceary does not yet wire into its managed hook set.
// The check intentionally returns pass status with a descriptive message so
// the operator sees the gap without treating it as a regression — the
// content was verified against each host's current official docs during
// v0.7-4.
func inspectHostCapabilityGaps(client, configPath string) *doctorCheck {
	switch client {
	case "claude":
		return &doctorCheck{
			Name:   "claude-host-capabilities",
			Status: doctorStatusPass,
			Message: localizef(
				"claude host: SubagentStop and PreCompact hooks are wired into the Traceary-managed config alongside the existing SessionStart / SessionEnd / Stop / PostCompact coverage (hooks config: %s)",
				"claude ホスト: SubagentStop と PreCompact は既存の SessionStart / SessionEnd / Stop / PostCompact と並んで Traceary 管理の hook config に組み込み済みです (hooks config: %s)",
				configPath,
			),
		}
	case "codex":
		codexConfigPath := describeCodexConfigPath()
		return &doctorCheck{
			Name:   "codex-host-capabilities",
			Status: doctorStatusPass,
			Message: localizef(
				"codex host: memory features ship behind a per-install feature flag in %s; consult the Codex release notes for the exact flag name and your enablement state. Traceary's `memory import codex` works regardless of the flag state",
				"codex ホスト: memory 機能は per-install な feature flag (%s) の背後にあります。flag 名と有効化状態の確認方法は Codex のリリースノートを参照してください。Traceary は flag 状態に関わらず `memory import codex` で取り込み可能です",
				codexConfigPath,
			),
		}
	case "gemini":
		return &doctorCheck{
			Name:   "gemini-host-capabilities",
			Status: doctorStatusPass,
			Message: localizef(
				"gemini host: memory manager agent and auto-memory are preview-flag features on Gemini CLI 0.38.x; Traceary's Tier 3 hook coverage (SessionStart / SessionEnd / AfterAgent / AfterTool) does not yet surface those preview signals (hooks config: %s)",
				"gemini ホスト: memory manager agent / auto-memory は Gemini CLI 0.38.x のプレビュー機能です。Traceary の Tier 3 hook (SessionStart / SessionEnd / AfterAgent / AfterTool) は現時点でそれらの preview 信号を surface しません (hooks config: %s)",
				configPath,
			),
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
func (c *RootCLI) inspectClaudeOrConfigFile(client, outputPath, projectDir string) doctorCheck {
	if client != "claude" {
		return c.inspectDoctorConfigFile(client, outputPath)
	}

	detection := c.detectClaudeTracearyPluginForCLI()
	configCheck := c.inspectDoctorConfigFile(client, outputPath)

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
	if hasTracearyHook {
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

func (c *RootCLI) inspectDoctorConfigFile(client string, outputPath string) doctorCheck {
	content, err := os.ReadFile(outputPath)
	if err != nil {
		if os.IsNotExist(err) {
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
		return doctorCheck{
			Name:    client + "-config",
			Status:  doctorStatusPass,
			Message: localizef("%s config contains Traceary-managed hooks: %s", "%s の設定には Traceary 管理下の hook があります: %s", client, outputPath),
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

func writeDoctorReport(output io.Writer, report *doctorReport, asJSON bool) error {
	if report == nil {
		return xerrors.Errorf(Localize("doctor report must not be nil", "doctor report は nil にできません"))
	}
	finalizeDoctorReport(report)

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

func finalizeDoctorReport(report *doctorReport) {
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
		report.ExitCode = 2
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
	case name == "db-path" || name == "db-write":
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
