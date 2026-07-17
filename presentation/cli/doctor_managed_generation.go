package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

// managedHookTimeouts maps managedKey -> timeout millis for Traceary-managed
// commands found in a hooks-shaped JSON document. Entries without a timeout
// field are recorded as -1 so "missing vs present" also counts as drift.
func managedHookTimeouts(content []byte, extractKey func(name, command string) string) (map[string]int, error) {
	var root struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Name    string `json:"name"`
				Command string `json:"command"`
				Timeout *int   `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(content, &root); err != nil {
		return nil, xerrors.Errorf("failed to decode hooks document for generation drift: %w", err)
	}
	out := map[string]int{}
	for _, matchers := range root.Hooks {
		for _, matcher := range matchers {
			for _, command := range matcher.Hooks {
				key := extractKey(command.Name, command.Command)
				if key == "" {
					continue
				}
				timeout := -1
				if command.Timeout != nil {
					timeout = *command.Timeout
				}
				// First occurrence wins; duplicates are handled by other checks.
				if _, exists := out[key]; !exists {
					out[key] = timeout
				}
			}
		}
	}
	return out, nil
}

// managedHookTimeoutDrift returns human-readable reasons when installed
// Traceary-managed hook timeouts diverge from what the current generation
// would write. Empty when generation is current (or no managed hooks).
func managedHookTimeoutDrift(installed, desired []byte, extractKey func(name, command string) string) []string {
	installedTimeouts, err := managedHookTimeouts(installed, extractKey)
	if err != nil || len(installedTimeouts) == 0 {
		return nil
	}
	desiredTimeouts, err := managedHookTimeouts(desired, extractKey)
	if err != nil || len(desiredTimeouts) == 0 {
		return nil
	}
	reasons := []string{}
	for key, want := range desiredTimeouts {
		got, ok := installedTimeouts[key]
		if !ok {
			continue // missing keys are coverage/upgrade gaps, not generation drift
		}
		if got != want {
			reasons = append(reasons, fmt.Sprintf("%s timeout installed=%s desired=%s", key, formatHookTimeout(got), formatHookTimeout(want)))
		}
	}
	return reasons
}

func formatHookTimeout(ms int) string {
	if ms < 0 {
		return "unset"
	}
	return fmt.Sprintf("%dms", ms)
}

// attachManagedGenerationCheck upgrades a would-be PASS client-config check
// into WARN when installed managed hook timeouts lag the current generation.
func (c *RootCLI) attachManagedGenerationCheck(ctx context.Context, check doctorCheck, client string, content []byte, outputPath, projectDir string) doctorCheck {
	if check.Status != doctorStatusPass {
		return check
	}
	if c.hooksOrchestrator == nil || c.hooksInspector == nil {
		return check
	}
	// Plugin-managed Claude must not rewrite project settings from doctor.
	if client == "claude" && c.detectClaudeTracearyPluginForCLI().Active {
		return check
	}
	desired, err := c.hooksOrchestrator.Generate(ctx, client, "traceary")
	if err != nil {
		return check
	}
	reasons := managedHookTimeoutDrift(content, desired, c.hooksInspector.ExtractManagedKeyFromEntry)
	if len(reasons) == 0 {
		return check
	}
	dryRunCommand := fmt.Sprintf("traceary doctor --fix --dry-run --client %s --project-dir %s", client, shellQuote(projectDir))
	check.Status = doctorStatusWarn
	check.Message = localizef(
		"%s config has Traceary-managed hooks but their generation is stale (%s). Host budgets or command payloads lag the installed Traceary version; prompt/transcript coverage can silently drop: %s",
		"%s config に Traceary 管理 hook はありますが generation が古いです (%s)。host budget や command payload がインストール済み Traceary より遅れ、prompt/transcript coverage が黙って欠けることがあります: %s",
		client,
		strings.Join(reasons, "; "),
		outputPath,
	)
	check.Hint = localizef(
		"preview the non-destructive refresh with `%s`; it rewrites only Traceary-managed entries (timeouts, commands) and preserves non-Traceary hooks",
		"`%s` で非破壊 refresh をプレビューしてください。Traceary 管理エントリ（timeout / command）だけを更新し、Traceary 以外の hook は保持します",
		dryRunCommand,
	)
	check.FixCommand = dryRunCommand
	check.AutoFixAvailable = true
	check.FixFunc = func(ctx context.Context, dryRun bool) (string, error) {
		if dryRun {
			return localizef("would refresh stale Traceary-managed hooks in %s", "%s の古い Traceary 管理 hook を更新します", outputPath), nil
		}
		if c.hooksOrchestrator == nil {
			return "", xerrors.New("hooks orchestrator is not configured")
		}
		_, _, err := c.hooksOrchestrator.UpgradeWithMatcher(ctx, client, "traceary", projectDir, types.Some(outputPath), "")
		if err != nil {
			return "", xerrors.Errorf("failed to refresh stale managed hooks: %w", err)
		}
		return localizef("refreshed Traceary-managed hooks in %s", "%s の Traceary 管理 hook を更新しました", outputPath), nil
	}
	return check
}
