package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/presentation"
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
	DBPath         string        `json:"db_path"`
	HookScriptsDir string        `json:"hook_scripts_dir,omitempty"`
	Clients        []string      `json:"clients"`
	Checks         []doctorCheck `json:"checks"`
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

type doctorCommandInput struct {
	dbPath         string
	client         string
	projectDir     string
	currentVersion string
	asJSON         bool
}

func (c *RootCLI) runDoctor(ctx context.Context, output io.Writer, input doctorCommandInput) error {
	if c.storeMaintenance == nil {
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
	resolvedClients, err := resolveDoctorClients(input.client)
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
	report.DBPath = resolvedDBPath
	report.Checks = append(report.Checks, doctorCheck{
		Name:    "db-path",
		Status:  doctorStatusPass,
		Message: localizef("resolved DB path: %s", "解決した DB パス: %s", resolvedDBPath),
	})

	report.Checks = append(report.Checks, inspectDoctorConfig())

	if err := c.storeMaintenance.Initialize(ctx); err != nil {
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

	hookScriptsDir, err := resolveHooksScriptsDir()
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{
			Name:    "hook-scripts",
			Status:  doctorStatusFail,
			Message: localizef("failed to resolve hook scripts directory: %v", "hook script directory の解決に失敗しました: %v", err),
		})
	} else {
		report.HookScriptsDir = hookScriptsDir
		if _, ensureErr := ensureHookScriptsInstalled(); ensureErr != nil {
			report.Checks = append(report.Checks, doctorCheck{
				Name:   "hook-scripts",
				Status: doctorStatusWarn,
				Message: localizef(
					"portable hook scripts are not ready yet at %s: %v",
					"portable hook script はまだ %s で利用可能ではありません: %v",
					hookScriptsDir,
					ensureErr,
				),
			})
		} else {
			report.Checks = append(report.Checks, doctorCheck{
				Name:    "hook-scripts",
				Status:  doctorStatusPass,
				Message: localizef("hook scripts are available: %s", "hook script を利用できます: %s", hookScriptsDir),
			})
		}
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
		outputPath, pathErr := resolveHooksInstallOutputPath(targetClient, resolvedProjectDir, "")
		if pathErr != nil {
			report.Checks = append(report.Checks, doctorCheck{
				Name:    targetClient + "-config",
				Status:  doctorStatusFail,
				Message: localizef("failed to resolve %s config path: %v", "%s の設定パス解決に失敗しました: %v", targetClient, pathErr),
			})
			continue
		}

		check := inspectDoctorConfigFile(targetClient, outputPath)
		if check.Status == doctorStatusWarn && targetClient == "claude" {
			pluginCheck := inspectDoctorPluginPackage(resolvedProjectDir)
			if pluginCheck.Status == doctorStatusPass {
				check = pluginCheck
			}
		}
		report.Checks = append(report.Checks, check)
	}

	report.Checks = append(report.Checks, checkLatestVersion(input.currentVersion))

	return report, nil
}

func resolveDoctorClients(client string) ([]string, error) {
	if strings.TrimSpace(client) == "" {
		return []string{"claude", "codex", "gemini"}, nil
	}

	resolvedClient, err := normalizeHooksClient(client)
	if err != nil {
		return nil, err
	}

	return []string{resolvedClient}, nil
}

func inspectDoctorConfig() doctorCheck {
	configResult := presentation.InspectConfig()
	switch configResult.Status {
	case presentation.ConfigLoadStatusLoaded:
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusPass,
			Message: localizef("loaded config file: %s", "設定ファイルを読み込みました: %s", configResult.Path),
		}
	case presentation.ConfigLoadStatusMissing:
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusPass,
			Message: localizef("optional config file is not present yet; built-in redaction defaults remain active: %s", "オプション設定ファイルはまだありません。組み込みの redaction 既定値を使います: %s", configResult.Path),
		}
	case presentation.ConfigLoadStatusInvalid:
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusFail,
			Message: localizef("config file is invalid JSON, so extra redaction patterns are disabled: %s (%v)", "設定ファイルの JSON が不正なため、追加 redaction pattern は無効です: %s (%v)", configResult.Path, configResult.Err),
		}
	case presentation.ConfigLoadStatusUnreadable:
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusFail,
			Message: localizef("config file could not be read, so extra redaction patterns are disabled: %s (%v)", "設定ファイルを読み込めないため、追加 redaction pattern は無効です: %s (%v)", configResult.Path, configResult.Err),
		}
	case presentation.ConfigLoadStatusHomeDirFailure:
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusFail,
			Message: localizef("failed to resolve the config path, so extra redaction patterns are disabled: %v", "設定ファイルのパスを解決できないため、追加 redaction pattern は無効です: %v", configResult.Err),
		}
	default:
		return doctorCheck{
			Name:    "config",
			Status:  doctorStatusFail,
			Message: localizef("config state is unknown", "設定ファイルの状態を判定できません"),
		}
	}
}

func inspectDoctorConfigFile(client string, outputPath string) doctorCheck {
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

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal(content, &root); err != nil {
		return doctorCheck{
			Name:    client + "-config",
			Status:  doctorStatusFail,
			Message: localizef("%s config file must be a JSON object: %s", "%s の設定ファイルは JSON object である必要があります: %s", client, outputPath),
		}
	}

	hooksValue, ok := root["hooks"]
	if !ok {
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

	hooksMap := map[string][]hookMatcher{}
	if err := json.Unmarshal(hooksValue, &hooksMap); err != nil {
		return doctorCheck{
			Name:    client + "-config",
			Status:  doctorStatusFail,
			Message: localizef("%s hooks field must be an object of hook arrays: %s", "%s の hooks フィールドは hook 配列を値に持つ object である必要があります: %s", client, outputPath),
		}
	}

	for _, matchers := range hooksMap {
		for _, matcher := range matchers {
			for _, hook := range matcher.Hooks {
				if isTracearyManagedHookCommand(hook) {
					return doctorCheck{
						Name:    client + "-config",
						Status:  doctorStatusPass,
						Message: localizef("%s config contains Traceary-managed hooks: %s", "%s の設定には Traceary 管理下の hook があります: %s", client, outputPath),
					}
				}
			}
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
	if report.HookScriptsDir != "" {
		if _, err := fmt.Fprintf(output, "HOOK_SCRIPTS_DIR: %s\n", report.HookScriptsDir); err != nil {
			return xerrors.Errorf("%s: %w", Localize("failed to print hook scripts directory", "hook script directory の出力に失敗しました"), err)
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
