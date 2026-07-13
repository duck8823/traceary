package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const codexHookTrustProbeTimeout = 5 * time.Second

type codexPluginHookTrustStatus string

const (
	codexPluginHookTrustAbsent       codexPluginHookTrustStatus = "absent"
	codexPluginHookTrustTrusted      codexPluginHookTrustStatus = "trusted"
	codexPluginHookTrustIncomplete   codexPluginHookTrustStatus = "incomplete"
	codexPluginHookTrustUntrusted    codexPluginHookTrustStatus = "untrusted"
	codexPluginHookTrustModified     codexPluginHookTrustStatus = "modified"
	codexPluginHookTrustDisabled     codexPluginHookTrustStatus = "disabled"
	codexPluginHookTrustUndetectable codexPluginHookTrustStatus = "undetectable"
)

type codexPluginHookTrustResult struct {
	PluginKey string
	Status    codexPluginHookTrustStatus
	HookCount int
	Reason    string
}

type codexHookListMetadata struct {
	PluginID   string `json:"pluginId"`
	Enabled    bool   `json:"enabled"`
	TrustState string `json:"trustStatus"`
}

type codexHookErrorInfo struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type codexHooksListResponse struct {
	Data []struct {
		Hooks    []codexHookListMetadata `json:"hooks"`
		Warnings []string                `json:"warnings"`
		Errors   []codexHookErrorInfo    `json:"errors"`
	} `json:"data"`
}

type codexAppServerEnvelope struct {
	ID     json.RawMessage        `json:"id"`
	Result codexHooksListResponse `json:"result"`
	Error  json.RawMessage        `json:"error"`
}

var codexPluginHookTrustProbeFunc = probeCodexPluginHookTrust

func probeCodexPluginHookTrust(ctx context.Context, projectDir, pluginKey string) codexPluginHookTrustResult {
	result := codexPluginHookTrustResult{PluginKey: pluginKey, Status: codexPluginHookTrustUndetectable}
	if strings.TrimSpace(pluginKey) == "" {
		result.Status = codexPluginHookTrustAbsent
		return result
	}

	probeCtx, cancel := context.WithTimeout(ctx, codexHookTrustProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "codex", "app-server", "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		result.Reason = "could not open Codex app-server input"
		return result
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Reason = "could not open Codex app-server output"
		return result
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		result.Reason = "Codex app-server is unavailable"
		return result
	}

	request := func(id int, method string, params any) error {
		return json.NewEncoder(stdin).Encode(map[string]any{
			"id": id, "method": method, "params": params,
		})
	}
	if err := request(0, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "traceary-doctor", "title": "Traceary Doctor", "version": "dev"},
	}); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		result.Reason = "could not initialize Codex app-server"
		return result
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	initialized := false
	for scanner.Scan() {
		var envelope codexAppServerEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		if string(envelope.ID) == "0" {
			if jsonRPCErrorPresent(envelope.Error) {
				break
			}
			initialized = true
			break
		}
	}
	if !initialized || request(1, "hooks/list", map[string]any{"cwds": []string{projectDir}}) != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		result.Reason = "Codex app-server initialization did not complete"
		return result
	}

	var hooksResponse *codexHooksListResponse
	for scanner.Scan() {
		var envelope codexAppServerEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil || string(envelope.ID) != "1" {
			continue
		}
		if !jsonRPCErrorPresent(envelope.Error) {
			hooksResponse = &envelope.Result
		}
		break
	}
	_ = stdin.Close()
	waitErr := cmd.Wait()
	if hooksResponse == nil {
		if probeCtx.Err() != nil {
			result.Reason = "Codex app-server hook inspection timed out"
		} else if err := scanner.Err(); err != nil {
			result.Reason = "Codex app-server hook response was unreadable"
		} else if waitErr != nil {
			result.Reason = "Codex app-server could not inspect hook trust"
		} else {
			result.Reason = "Codex app-server returned no hook trust result"
		}
		return result
	}
	return classifyCodexPluginHookTrust(pluginKey, *hooksResponse)
}

func jsonRPCErrorPresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func classifyCodexPluginHookTrust(pluginKey string, response codexHooksListResponse) codexPluginHookTrustResult {
	result := codexPluginHookTrustResult{PluginKey: pluginKey, Status: codexPluginHookTrustUndetectable}
	var matched []codexHookListMetadata
	for _, entry := range response.Data {
		if len(entry.Warnings) > 0 || len(entry.Errors) > 0 {
			result.Reason = "Codex reported hook discovery warnings or errors"
			return result
		}
		for _, hook := range entry.Hooks {
			if hook.PluginID == pluginKey {
				matched = append(matched, hook)
			}
		}
	}
	result.HookCount = len(matched)
	if len(matched) == 0 {
		result.Reason = "the enabled plugin exposed no hook metadata"
		return result
	}

	result.Status = codexPluginHookTrustTrusted
	for _, hook := range matched {
		if !hook.Enabled {
			result.Status = codexPluginHookTrustDisabled
			continue
		}
		switch hook.TrustState {
		case "trusted", "managed":
		case "modified":
			if result.Status != codexPluginHookTrustDisabled {
				result.Status = codexPluginHookTrustModified
			}
		case "untrusted":
			if result.Status != codexPluginHookTrustDisabled && result.Status != codexPluginHookTrustModified {
				result.Status = codexPluginHookTrustUntrusted
			}
		default:
			if result.Status == codexPluginHookTrustTrusted {
				result.Status = codexPluginHookTrustUndetectable
				result.Reason = fmt.Sprintf("Codex returned an unknown hook trust status %q", hook.TrustState)
			}
		}
	}
	if result.Status == codexPluginHookTrustTrusted && result.HookCount != expectedCodexPluginHookCount() {
		result.Status = codexPluginHookTrustIncomplete
		result.Reason = fmt.Sprintf(
			"Codex returned %d Traceary plugin hook commands; the current package requires exactly %d",
			result.HookCount,
			expectedCodexPluginHookCount(),
		)
	}
	return result
}

func codexPluginHookTrustCheck(result codexPluginHookTrustResult) doctorCheck {
	const name = "codex-plugin-hooks"
	switch result.Status {
	case codexPluginHookTrustAbsent:
		return doctorCheck{Name: name, Status: doctorStatusSkip, Message: localizef("Traceary Codex plugin is not enabled", "Traceary Codex plugin は有効ではありません")}
	case codexPluginHookTrustTrusted:
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef(
			"Codex reports all %d Traceary plugin hook(s) as enabled and trusted for the current definitions: %s",
			"Codex は現在の定義に対する Traceary plugin hook %d 件をすべて有効かつ trusted と報告しています: %s",
			result.HookCount, result.PluginKey,
		)}
	case codexPluginHookTrustIncomplete:
		return doctorCheck{
			Name: name, Status: doctorStatusWarn, FixCommand: "codex",
			Hint: Localize(
				"update the Traceary Codex plugin, open `/hooks`, then review and trust every current hook before removing the manual fallback",
				"Traceary Codex plugin を更新し、`/hooks` を開いて現在の hook をすべて確認・trust してから手動 fallback を削除してください",
			),
			Message: localizef(
				"Traceary plugin hook coverage is incomplete: Codex reports %d command(s), but the current package requires exactly %d; manual fallback hooks will be preserved: %s",
				"Traceary plugin hook の coverage が不完全です。Codex は %d command を報告していますが、現在の package には正確に %d 件必要です。手動 fallback hook は保持されます: %s",
				result.HookCount, expectedCodexPluginHookCount(), result.PluginKey,
			),
		}
	case codexPluginHookTrustUntrusted, codexPluginHookTrustModified, codexPluginHookTrustDisabled:
		return doctorCheck{
			Name: name, Status: doctorStatusWarn, FixCommand: "codex",
			Hint: Localize(
				"start Codex in this project, open `/hooks`, review the Traceary plugin hooks, then trust and enable the current definitions",
				"この project で Codex を起動し、`/hooks` を開いて Traceary plugin hook を確認し、現在の定義を trust して有効化してください",
			),
			Message: localizef(
				"Codex reports Traceary plugin hooks as %s; untrusted, changed, or disabled hooks are skipped and Traceary capture is incomplete: %s",
				"Codex は Traceary plugin hook を %s と報告しています。untrusted・変更済み・無効な hook は実行されず、Traceary の capture が不完全になります: %s",
				result.Status, result.PluginKey,
			),
		}
	default:
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "effective hook state was not returned"
		}
		return doctorCheck{
			Name: name, Status: doctorStatusWarn, FixCommand: "codex",
			Hint: Localize(
				"start Codex in this project and inspect `/hooks` manually",
				"この project で Codex を起動し、`/hooks` を手動確認してください",
			),
			Message: localizef(
				"could not verify effective Traceary plugin hook trust via Codex app-server (%s); this is not a pass, so inspect `/hooks` manually: %s",
				"Codex app-server で Traceary plugin hook の有効な trust 状態を確認できませんでした (%s)。pass ではないため、`/hooks` で手動確認してください: %s",
				reason, result.PluginKey,
			),
		}
	}
}
