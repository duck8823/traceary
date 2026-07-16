package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/application/hostcoverage"
)

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
// capabilities driven by application/hostcoverage (the same matrix that
// generates docs/hooks/host-coverage.md). Checks return pass with descriptive
// messages so operators see the expected wired lifecycle set without treating
// a complete install as a regression.
func inspectHostCapabilityGaps(client, configPath string) []doctorCheck {
	matrix, err := hostcoverage.Load()
	if err != nil {
		return []doctorCheck{{
			Name:    client + "-host-capabilities",
			Status:  doctorStatusFail,
			Message: localizef("failed to load host coverage matrix: %v", "host coverage matrix の読み込みに失敗しました: %v", err),
		}}
	}
	host, ok := matrix.HostByDoctorClient(client)
	if !ok {
		return nil
	}

	wired := matrix.WiredLifecycleEvents(client)
	wiredList := strings.Join(wired, ", ")
	if wiredList == "" {
		wiredList = "(none)"
	}

	checks := []doctorCheck{{
		Name:   client + "-host-capabilities",
		Status: doctorStatusPass,
		Message: localizef(
			"%s host: Traceary matrix promises wired lifecycle coverage for %s (source application/hostcoverage; hooks config: %s)",
			"%s ホスト: Traceary の matrix は %s の lifecycle coverage を wired として約束しています (正本 application/hostcoverage; hooks config: %s)",
			client,
			wiredList,
			configPath,
		),
	}}

	// Keep the codex memory-flag and gemini compact-boundary footnotes that are
	// not expressible as simple lifecycle cells.
	switch client {
	case "codex":
		codexConfigPath := describeCodexConfigPath()
		checks[0].Message = localizef(
			"codex host: matrix-wired lifecycle coverage is %s. Compact and subagent hooks are part of the managed set; memory features still depend on the per-install feature flag in %s (Traceary's `memory import codex` works regardless). hooks config context: %s",
			"codex ホスト: matrix 上の wired lifecycle は %s です。compact / subagent hook は managed set に含まれます。memory 機能は per-install feature flag (%s) に依存しますが、Traceary の `memory import codex` は flag 状態に関わらず動作します。hooks config 文脈: %s",
			wiredList,
			codexConfigPath,
			configPath,
		)
	case "gemini":
		checks[0].Message = localizef(
			"gemini host: matrix-wired lifecycle coverage is %s. Memory manager agent and auto-memory remain experimental on Gemini CLI and are not Traceary lifecycle events (hooks config: %s)",
			"gemini ホスト: matrix 上の wired lifecycle は %s です。memory manager agent / auto-memory は Gemini CLI の experimental 機能であり Traceary lifecycle event ではありません (hooks config: %s)",
			wiredList,
			configPath,
		)
		if cell, ok := host.Events["compact_summary"]; ok && cell.Status == hostcoverage.StatusWired {
			checks = append(checks, doctorCheck{
				Name:   "gemini-compact-coverage",
				Status: doctorStatusPass,
				Message: Localize(
					"gemini host: compact summaries are captured at the pre-compact boundary only (PreCompress marker). Gemini CLI exposes no post-compress hook — PreCompress is advisory-only and fires asynchronously before compression — so a missing post-compact digest is expected upstream behavior, not a broken install",
					"gemini ホスト: compact summary は pre-compact 境界のみで捕捉します (PreCompress marker)。Gemini CLI に post-compress hook は存在せず、PreCompress は compression 前に非同期で発火する advisory-only hook です。post-compact digest が無いのは upstream の想定挙動であり、インストール不良ではありません",
				),
			})
		}
	}
	return checks
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
		if c.configHasTracearyHooks(outputPath) {
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

// configHasTracearyHooks returns true iff the host hook file at outputPath is
// a valid JSON object with a hooks field that contains at least one
// Traceary-managed hook entry. Missing files, unreadable files, and malformed
// JSON all return false so host-specific plugin detection can interpret them
// as "no manual Traceary hook registered here".
func (c *RootCLI) configHasTracearyHooks(outputPath string) bool {
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

func (c *RootCLI) inspectDoctorConfigFile(_ context.Context, client string, outputPath string, projectDir string) doctorCheck {
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
var codexManagedEvents = []string{"SessionStart", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "UserPromptSubmit", "Stop", "PostToolUse"}

// codexManagedEventKeys maps each expected Codex event to the stable managed
// key the Traceary hook runtime installs for it. The key is reused as the
// single source of truth when comparing hooks.json entries against a real
// Traceary install — a substring check on the command string would
// misclassify user-managed commands that happen to contain "hook" and
// "codex".
var codexManagedEventKeys = map[string][]string{
	"SessionStart":     []string{"traceary-session.sh:codex:start"},
	"SubagentStart":    []string{"traceary-subagent-start.sh:codex"},
	"SubagentStop":     []string{"traceary-subagent-stop.sh:codex"},
	"PreCompact":       []string{"traceary-compact.sh:codex:pre-compact"},
	"PostCompact":      []string{"traceary-compact.sh:codex:post-compact"},
	"UserPromptSubmit": []string{"traceary-prompt.sh:codex"},
	"Stop":             []string{"traceary-transcript.sh:codex", "traceary-session.sh:codex:stop"},
	"PostToolUse":      []string{"traceary-audit.sh:codex"},
}

// expectedCodexPluginHookCount returns the number of command hooks in the
// current packaged Codex contract. Codex app-server currently exposes plugin
// identity and trust state per command, but not the event or command identity,
// so exact cardinality is the fail-closed completeness boundary available to
// doctor before it permits removal of the manual fallback.
func expectedCodexPluginHookCount() int {
	count := 0
	for _, keys := range codexManagedEventKeys {
		count += len(keys)
	}
	return count
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
