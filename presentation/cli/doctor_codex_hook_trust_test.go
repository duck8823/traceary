package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

var testCodexManagedKeyExtractor = filesystem.NewHooksInspector().ExtractManagedKeyFromEntry

func TestProbeCodexPluginHookTrustUsesHooksList(t *testing.T) {
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	script := `#!/usr/bin/python3
import json
import sys

initialize = json.loads(sys.stdin.readline())
assert initialize["id"] == 0
assert initialize["method"] == "initialize"
print(json.dumps({"id": 0, "result": {"userAgent": "synthetic"}}), flush=True)

hooks_list = json.loads(sys.stdin.readline())
assert hooks_list["id"] == 1
assert hooks_list["method"] == "hooks/list"
assert hooks_list["params"]["cwds"] == ["/tmp/project"]
print(json.dumps({"id": 1, "result": {"data": [{
    "cwd": "/tmp/project",
    "hooks": [{"pluginId": "traceary@market", "enabled": True, "trustStatus": "trusted"}] * ` + fmt.Sprintf("%d", expectedCodexPluginHookCount()) + `,
    "warnings": [],
    "errors": []
}]}}), flush=True)
`
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("PATH", binDir)

	got := probeCodexPluginHookTrust(context.Background(), "/tmp/project", "traceary@market", testCodexManagedKeyExtractor)
	if got.Status != codexPluginHookTrustTrusted || got.HookCount != expectedCodexPluginHookCount() {
		t.Fatalf("probeCodexPluginHookTrust() = %+v, want trusted hook", got)
	}
}

func TestJSONRPCErrorPresent(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "absent", raw: "", want: false},
		{name: "null", raw: "null", want: false},
		{name: "error object", raw: `{"code":-32600}`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonRPCErrorPresent(json.RawMessage(tt.raw)); got != tt.want {
				t.Fatalf("jsonRPCErrorPresent(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestClassifyCodexPluginHookTrust(t *testing.T) {
	tests := []struct {
		name       string
		hooksJSON  string
		wantStatus codexPluginHookTrustStatus
		wantCount  int
	}{
		{
			name:       "all current hooks trusted",
			hooksJSON:  trustedCodexHooksJSON("traceary@market", expectedCodexPluginHookCount()),
			wantStatus: codexPluginHookTrustTrusted,
			wantCount:  expectedCodexPluginHookCount(),
		},
		{
			name:       "trusted obsolete hook set is incomplete",
			hooksJSON:  trustedCodexHooksJSON("traceary@market", expectedCodexPluginHookCount()-1),
			wantStatus: codexPluginHookTrustIncomplete,
			wantCount:  expectedCodexPluginHookCount() - 1,
		},
		{
			name:       "unexpected extra trusted hook is incomplete",
			hooksJSON:  trustedCodexHooksJSON("traceary@market", expectedCodexPluginHookCount()+1),
			wantStatus: codexPluginHookTrustIncomplete,
			wantCount:  expectedCodexPluginHookCount() + 1,
		},
		{
			name:       "untrusted hook",
			hooksJSON:  `{"data":[{"hooks":[{"pluginId":"traceary@market","enabled":true,"trustStatus":"untrusted"}]}]}`,
			wantStatus: codexPluginHookTrustUntrusted,
			wantCount:  1,
		},
		{
			name:       "modified hook takes precedence over untrusted",
			hooksJSON:  `{"data":[{"hooks":[{"pluginId":"traceary@market","enabled":true,"trustStatus":"untrusted"},{"pluginId":"traceary@market","enabled":true,"trustStatus":"modified"}]}]}`,
			wantStatus: codexPluginHookTrustModified,
			wantCount:  2,
		},
		{
			name:       "disabled hook takes precedence",
			hooksJSON:  `{"data":[{"hooks":[{"pluginId":"traceary@market","enabled":true,"trustStatus":"modified"},{"pluginId":"traceary@market","enabled":false,"trustStatus":"trusted"}]}]}`,
			wantStatus: codexPluginHookTrustDisabled,
			wantCount:  2,
		},
		{
			name:       "no plugin hook metadata is undetectable",
			hooksJSON:  `{"data":[{"hooks":[{"pluginId":"other@market","enabled":true,"trustStatus":"trusted"}]}]}`,
			wantStatus: codexPluginHookTrustUndetectable,
			wantCount:  0,
		},
		{
			name:       "unknown trust status is undetectable",
			hooksJSON:  `{"data":[{"hooks":[{"pluginId":"traceary@market","enabled":true,"trustStatus":"future"}]}]}`,
			wantStatus: codexPluginHookTrustUndetectable,
			wantCount:  1,
		},
		{
			name:       "partial load warning prevents trusted result",
			hooksJSON:  `{"data":[{"hooks":[{"pluginId":"traceary@market","enabled":true,"trustStatus":"trusted"}],"warnings":["synthetic warning"]}]}`,
			wantStatus: codexPluginHookTrustUndetectable,
			wantCount:  0,
		},
		{
			name:       "partial load error prevents trusted result",
			hooksJSON:  `{"data":[{"hooks":[{"pluginId":"traceary@market","enabled":true,"trustStatus":"trusted"}],"errors":[{"path":"/tmp/plugin/hooks.json","message":"synthetic error"}]}]}`,
			wantStatus: codexPluginHookTrustUndetectable,
			wantCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response codexHooksListResponse
			if err := json.Unmarshal([]byte(tt.hooksJSON), &response); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			got := classifyCodexPluginHookTrust("traceary@market", response, testCodexManagedKeyExtractor)
			if got.Status != tt.wantStatus || got.HookCount != tt.wantCount {
				t.Fatalf("classifyCodexPluginHookTrust() = status %q count %d, want status %q count %d", got.Status, got.HookCount, tt.wantStatus, tt.wantCount)
			}
		})
	}
}

// TestClassifyCodexPluginHookTrust_NamesExtraCommand covers the case that
// motivated this diagnostic: Codex still trusts a command from a previous
// package generation (here, an old `traceary-compact.sh` phase name no
// package generation ships anymore) alongside every currently-expected
// command. Doctor should name the surplus command, not just report a count
// mismatch.
func TestClassifyCodexPluginHookTrust_NamesExtraCommand(t *testing.T) {
	response := codexHooksListResponse{}
	response.Data = append(response.Data, struct {
		Hooks    []codexHookListMetadata `json:"hooks"`
		Warnings []string                `json:"warnings"`
		Errors   []codexHookErrorInfo    `json:"errors"`
	}{Hooks: currentCodexHooks("traceary@market")})
	response.Data[0].Hooks = append(response.Data[0].Hooks, codexHookListMetadata{
		PluginID:   "traceary@market",
		Enabled:    true,
		TrustState: "trusted",
		Command:    "TRACEARY_BIN='traceary' bash '/scripts/traceary-compact.sh' 'codex' 'legacy-phase'",
	})

	got := classifyCodexPluginHookTrust("traceary@market", response, testCodexManagedKeyExtractor)

	if got.Status != codexPluginHookTrustIncomplete {
		t.Fatalf("Status = %q, want %q", got.Status, codexPluginHookTrustIncomplete)
	}
	want := []string{"traceary-compact.sh:codex:legacy-phase"}
	if diff := cmpDiffStrings(got.ExtraCommands, want); diff != "" {
		t.Fatalf("ExtraCommands mismatch (-got +want):\n%s", diff)
	}
	if len(got.MissingCommands) != 0 {
		t.Fatalf("MissingCommands = %v, want empty", got.MissingCommands)
	}
}

// TestClassifyCodexPluginHookTrust_NamesMissingCommand covers an obsolete
// hook set (fewer commands than the package expects) naming which specific
// expected command is absent, not just the count.
func TestClassifyCodexPluginHookTrust_NamesMissingCommand(t *testing.T) {
	hooks := currentCodexHooks("traceary@market")
	var withoutUsage []codexHookListMetadata
	for _, hook := range hooks {
		if strings.Contains(hook.Command, "'usage'") {
			continue
		}
		withoutUsage = append(withoutUsage, hook)
	}
	response := codexHooksListResponse{}
	response.Data = append(response.Data, struct {
		Hooks    []codexHookListMetadata `json:"hooks"`
		Warnings []string                `json:"warnings"`
		Errors   []codexHookErrorInfo    `json:"errors"`
	}{Hooks: withoutUsage})

	got := classifyCodexPluginHookTrust("traceary@market", response, testCodexManagedKeyExtractor)

	if got.Status != codexPluginHookTrustIncomplete {
		t.Fatalf("Status = %q, want %q", got.Status, codexPluginHookTrustIncomplete)
	}
	want := []string{"traceary-usage.sh:codex"}
	if diff := cmpDiffStrings(got.MissingCommands, want); diff != "" {
		t.Fatalf("MissingCommands mismatch (-got +want):\n%s", diff)
	}
	if len(got.ExtraCommands) != 0 {
		t.Fatalf("ExtraCommands = %v, want empty", got.ExtraCommands)
	}
}

// currentCodexHooks builds one enabled+trusted codexHookListMetadata entry
// per command in the current packaged Codex contract, with literal command
// text matching what Codex's hooks/list reports for the shipped
// plugins/traceary/hooks.json (verified against a live `codex app-server`
// probe during investigation of issue 1975).
func currentCodexHooks(pluginKey string) []codexHookListMetadata {
	commands := []string{
		"'traceary' 'hook' 'session' 'codex' 'start'",
		"'traceary' 'hook' 'subagent-start' 'codex'",
		"'traceary' 'hook' 'subagent-stop' 'codex'",
		"'traceary' 'hook' 'compact' 'codex' 'pre-compact'",
		"'traceary' 'hook' 'compact' 'codex' 'post-compact'",
		"'traceary' 'hook' 'prompt' 'codex'",
		"'traceary' 'hook' 'usage' 'codex'",
		"'traceary' 'hook' 'transcript' 'codex'",
		"'traceary' 'hook' 'session' 'codex' 'stop'",
		"'traceary' 'hook' 'audit' 'codex'",
	}
	hooks := make([]codexHookListMetadata, 0, len(commands))
	for _, command := range commands {
		hooks = append(hooks, codexHookListMetadata{
			PluginID:   pluginKey,
			Enabled:    true,
			TrustState: "trusted",
			Command:    command,
		})
	}
	return hooks
}

func cmpDiffStrings(got, want []string) string {
	sortedGot := append([]string(nil), got...)
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedGot)
	sort.Strings(sortedWant)
	if len(sortedGot) != len(sortedWant) {
		return fmt.Sprintf("got %v, want %v", sortedGot, sortedWant)
	}
	for i := range sortedGot {
		if sortedGot[i] != sortedWant[i] {
			return fmt.Sprintf("got %v, want %v", sortedGot, sortedWant)
		}
	}
	return ""
}

// TestCodexPluginHookTrustCheck_NamesCommandDiff verifies the incomplete
// message names the specific extra/missing commands (issue 1975) rather than
// only reporting a bare count mismatch.
func TestCodexPluginHookTrustCheck_NamesCommandDiff(t *testing.T) {
	check := codexPluginHookTrustCheck(codexPluginHookTrustResult{
		PluginKey:       "traceary@market",
		Status:          codexPluginHookTrustIncomplete,
		HookCount:       11,
		ExtraCommands:   []string{"traceary-compact.sh:codex:legacy-phase"},
		MissingCommands: nil,
	})
	if !strings.Contains(check.Message, "extra: traceary-compact.sh:codex:legacy-phase") {
		t.Fatalf("Message = %q, want it to name the extra command", check.Message)
	}
}

func TestCodexPluginHookTrustCheck(t *testing.T) {
	tests := []struct {
		name        string
		status      codexPluginHookTrustStatus
		wantDoctor  string
		wantMessage string
		wantFix     string
	}{
		{name: "trusted", status: codexPluginHookTrustTrusted, wantDoctor: doctorStatusPass, wantMessage: "enabled and trusted"},
		{name: "incomplete", status: codexPluginHookTrustIncomplete, wantDoctor: doctorStatusWarn, wantMessage: "incomplete", wantFix: "codex"},
		{name: "untrusted", status: codexPluginHookTrustUntrusted, wantDoctor: doctorStatusWarn, wantMessage: "untrusted", wantFix: "codex"},
		{name: "modified", status: codexPluginHookTrustModified, wantDoctor: doctorStatusWarn, wantMessage: "modified", wantFix: "codex"},
		{name: "disabled", status: codexPluginHookTrustDisabled, wantDoctor: doctorStatusWarn, wantMessage: "disabled", wantFix: "codex"},
		{name: "undetectable", status: codexPluginHookTrustUndetectable, wantDoctor: doctorStatusWarn, wantMessage: "not a pass", wantFix: "codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := codexPluginHookTrustCheck(codexPluginHookTrustResult{
				PluginKey: "traceary@market", Status: tt.status, HookCount: 5, Reason: "synthetic probe result",
			})
			if check.Status != tt.wantDoctor {
				t.Fatalf("Status = %q, want %q", check.Status, tt.wantDoctor)
			}
			if !strings.Contains(check.Message, tt.wantMessage) {
				t.Fatalf("Message = %q, want substring %q", check.Message, tt.wantMessage)
			}
			if check.FixCommand != tt.wantFix {
				t.Fatalf("FixCommand = %q, want %q", check.FixCommand, tt.wantFix)
			}
		})
	}
}

func trustedCodexHooksJSON(pluginKey string, count int) string {
	hooks := make([]string, count)
	for i := range hooks {
		hooks[i] = `{"pluginId":` + strconv.Quote(pluginKey) + `,"enabled":true,"trustStatus":"trusted"}`
	}
	return `{"data":[{"hooks":[` + strings.Join(hooks, ",") + `]}]}`
}
