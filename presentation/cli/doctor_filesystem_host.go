package cli

import (
	"context"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// appendFilesystemHostDoctorChecks emits host-file hook and config checks
// that never open the Traceary SQLite store. Large-store doctor uses this
// so operators still see gemini-config / hook queues / cancellations.
func (c *RootCLI) appendFilesystemHostDoctorChecks(
	ctx context.Context,
	report *doctorReport,
	clients []string,
	projectDir, dbPath string,
) {
	if report == nil {
		return
	}
	now := time.Now().UTC()
	report.Checks = append(report.Checks, inspectDoctorConfig())
	report.Checks = append(report.Checks, c.inspectHookMemoryExtractDiagnostics(now))
	report.Checks = append(report.Checks, c.inspectHookGrokTranscriptDiagnostics(now))
	if c.hooksOrchestrator == nil {
		return
	}
	for _, targetClient := range clients {
		switch targetClient {
		case "kimi", "antigravity", "grok":
			// Native package identity already covers these hosts.
			continue
		}
		outputPath, pathErr := c.hooksOrchestrator.ResolveInstallPath(targetClient, projectDir, types.None[string]())
		if pathErr != nil {
			report.Checks = append(report.Checks, doctorCheck{
				Name:    targetClient + "-config",
				Status:  doctorStatusFail,
				Message: localizef("failed to resolve %s config path: %v", "%s の設定パス解決に失敗しました: %v", targetClient, pathErr),
			})
			continue
		}
		var check doctorCheck
		if targetClient == "codex" {
			pluginState := c.detectCodexPluginHookFallback()
			if pluginState.PluginEnabled {
				trust := codexPluginHookTrustProbeFunc(ctx, projectDir, pluginState.PluginKey, c.hooksInspector.ExtractManagedKeyFromEntry)
				report.Checks = append(report.Checks, codexPluginHookTrustCheck(trust))
				check = c.inspectCodexConfigWithHookTrust(ctx, outputPath, projectDir, trust)
			} else {
				check = c.inspectClaudeOrConfigFile(ctx, targetClient, outputPath, projectDir)
			}
		} else {
			check = c.inspectClaudeOrConfigFile(ctx, targetClient, outputPath, projectDir)
		}
		c.attachDoctorConfigFix(&check, targetClient, outputPath, projectDir)
		report.Checks = append(report.Checks, check)
		if targetClient == "claude" {
			report.Checks = append(report.Checks, c.inspectClaudeHookCancellationDiagnosticsFilesystem(ctx, dbPath, projectDir))
			if cacheCheck := c.inspectClaudePluginCacheStatus(); cacheCheck != nil {
				report.Checks = append(report.Checks, *cacheCheck)
			}
		}
		if globalCheck := c.inspectGlobalConfigForClient(targetClient); globalCheck != nil {
			report.Checks = append(report.Checks, *globalCheck)
		}
		report.Checks = append(report.Checks, inspectHostCapabilityGaps(targetClient, outputPath)...)
	}
}
