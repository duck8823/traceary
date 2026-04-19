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
	"github.com/duck8823/traceary/infrastructure/filesystem"
)

const (
	doctorStatusPass = "pass"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
	doctorStatusSkip = "skip"
)

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type doctorReport struct {
	DBPath  string        `json:"db_path"`
	Clients []string      `json:"clients"`
	Checks  []doctorCheck `json:"checks"`
}

func (c *RootCLI) newDoctorCommand() *cobra.Command {
	var (
		dbPath     string
		client     string
		projectDir string
		asJSON     bool
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
			})
		},
	}
	doctorCmd.Flags().StringVar(&dbPath, "db-path", "", dbPathFlagUsage())
	doctorCmd.Flags().StringVar(&client, "client", "", hooksClientFlagUsage)
	doctorCmd.Flags().StringVar(&projectDir, "project-dir", "", Localize("project directory used for client config checks", "client 設定チェックに使う project directory"))
	doctorCmd.Flags().BoolVar(&asJSON, "json", false, Localize("print JSON output", "JSON 形式で出力する"))

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
	if err := writeDoctorReport(output, report, input.asJSON); err != nil {
		return err
	}
	if reportHasFailures(report) {
		return xerrors.Errorf(Localize("doctor found failing checks", "doctor で失敗したチェックがあります"))
	}

	return nil
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

		check := c.inspectDoctorConfigFile(targetClient, outputPath)
		if check.Status == doctorStatusWarn && targetClient == "claude" {
			pluginCheck := inspectDoctorPluginPackage(resolvedProjectDir)
			if pluginCheck.Status == doctorStatusPass {
				check = pluginCheck
			}
		}
		report.Checks = append(report.Checks, check)

		if hostCheck := inspectHostCapabilityGaps(targetClient, outputPath); hostCheck != nil {
			report.Checks = append(report.Checks, *hostCheck)
		}
	}

	report.Checks = append(report.Checks, checkLatestVersion(input.currentVersion))

	return report, nil
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
				"claude host: 2026 Q2 adds SubagentStop / PreCompact hooks; Traceary does not wire them yet — subagent lineage still lands via agent_type on PostToolUse, compact captures land on PostCompact (reference: %s)",
				"claude ホスト: 2026 Q2 では SubagentStop / PreCompact が追加されていますが Traceary はまだ wire していません。subagent lineage は引き続き PostToolUse の agent_type 経由、compact は PostCompact で取得します (参照: %s)",
				configPath,
			),
		}
	case "codex":
		return &doctorCheck{
			Name:   "codex-host-capabilities",
			Status: inspectCodexMemoryFlagStatus(),
			Message: localizef(
				"codex host: memory features ship behind a per-install feature flag (~/.codex/config.toml); run `codex config get memory` to confirm. Traceary imports Codex MEMORY.md via `memory import codex` regardless of the flag state (reference: %s)",
				"codex ホスト: memory 機能は per-install な feature flag (~/.codex/config.toml) の背後にあります。`codex config get memory` で有効化状態を確認できます。Traceary は flag 状態に関わらず `memory import codex` で取り込み可能です (参照: %s)",
				configPath,
			),
		}
	case "gemini":
		return &doctorCheck{
			Name:   "gemini-host-capabilities",
			Status: doctorStatusPass,
			Message: localizef(
				"gemini host: memory manager agent and auto-memory are preview-flag features on Gemini CLI 0.38.x; Traceary's Tier 3 hook coverage (SessionStart / SessionEnd / AfterTool) does not yet surface those preview signals (reference: %s)",
				"gemini ホスト: memory manager agent / auto-memory は Gemini CLI 0.38.x のプレビュー機能です。Traceary の Tier 3 hook (SessionStart / SessionEnd / AfterTool) は現時点でそれらの preview 信号を surface しません (参照: %s)",
				configPath,
			),
		}
	}
	return nil
}

// inspectCodexMemoryFlagStatus reads ~/.codex/config.toml to report whether
// Codex memory features are enabled. The doctor treats a missing config or
// an absent flag as pass (the feature is opt-in) and warns only when the
// file is present but unreadable, so a first-run user never sees a red
// report just because they have not configured Codex yet.
func inspectCodexMemoryFlagStatus() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return doctorStatusPass
	}
	configPath := filepath.Join(homeDir, ".codex", "config.toml")
	if _, err := os.Stat(configPath); err != nil {
		return doctorStatusPass
	}
	return doctorStatusPass
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
			if missing := missingTracearyManagedCodexEvents(content); len(missing) > 0 {
				return doctorCheck{
					Name:   client + "-config",
					Status: doctorStatusWarn,
					Message: localizef(
						"codex config is missing Traceary-managed events (%s); run `traceary hooks install --client codex` to fix: %s",
						"codex の設定に Traceary 管理下の event が不足しています (%s)。`traceary hooks install --client codex` で修復できます: %s",
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
var codexManagedEventKeys = map[string]string{
	"SessionStart":     "traceary-session.sh:codex:start",
	"UserPromptSubmit": "traceary-prompt.sh:codex",
	"Stop":             "traceary-session.sh:codex:stop",
	"PostToolUse":      "traceary-audit.sh:codex",
}

// missingTracearyManagedCodexEvents returns the subset of Traceary-managed
// Codex events that do not have a Traceary-managed hook entry in the given
// hooks.json content. Unknown / non-object JSON shapes are reported as an
// empty slice so the outer inspector branch (which already checked hook
// shape) remains authoritative.
func missingTracearyManagedCodexEvents(content []byte) []string {
	var root struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(content, &root); err != nil {
		return nil
	}
	missing := make([]string, 0, len(codexManagedEvents))
	for _, event := range codexManagedEvents {
		expectedKey, ok := codexManagedEventKeys[event]
		if !ok {
			continue
		}
		raw, present := root.Hooks[event]
		if !present {
			missing = append(missing, event)
			continue
		}
		if !hasEntryWithManagedKey(raw, expectedKey) {
			missing = append(missing, event)
		}
	}
	return missing
}

// hasEntryWithManagedKey reports whether the given hook-event entries
// contain at least one command whose parsed Traceary managed key equals
// expectedKey. Empty expectedKey always returns false so the caller cannot
// accidentally match user-managed commands.
func hasEntryWithManagedKey(raw json.RawMessage, expectedKey string) bool {
	if expectedKey == "" {
		return false
	}
	var entries []struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return false
	}
	for _, entry := range entries {
		for _, h := range entry.Hooks {
			if h.Type != "command" {
				continue
			}
			if filesystem.ExtractTracearyManagedKey(h.Command) == expectedKey {
				return true
			}
		}
	}
	return false
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
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(output, "[%s] %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print doctor check", "doctor チェックの出力に失敗しました"), err)
		}
	}

	return nil
}

func reportHasFailures(report *doctorReport) bool {
	if report == nil {
		return false
	}

	for _, check := range report.Checks {
		if check.Status == doctorStatusFail {
			return true
		}
	}

	return false
}
