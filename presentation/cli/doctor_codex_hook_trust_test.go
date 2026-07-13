package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProbeCodexPluginHookTrustUsesHooksList(t *testing.T) {
	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	script := `#!/bin/sh
read -r initialize
printf '%s\n' '{"id":0,"result":{"userAgent":"synthetic"}}'
read -r hooks_list
printf '%s\n' '{"id":1,"result":{"data":[{"cwd":"/tmp/project","hooks":[{"pluginId":"traceary@market","enabled":true,"trustStatus":"trusted"}]}]}}'
`
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("PATH", binDir)

	got := probeCodexPluginHookTrust(context.Background(), "/tmp/project", "traceary@market")
	if got.Status != codexPluginHookTrustTrusted || got.HookCount != 1 {
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
			name: "all current hooks trusted",
			hooksJSON: `{"data":[{"hooks":[
				{"pluginId":"traceary@market","enabled":true,"trustStatus":"trusted"},
				{"pluginId":"traceary@market","enabled":true,"trustStatus":"trusted"},
				{"pluginId":"other@market","enabled":true,"trustStatus":"untrusted"}
			]}]}`,
			wantStatus: codexPluginHookTrustTrusted,
			wantCount:  2,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var response codexHooksListResponse
			if err := json.Unmarshal([]byte(tt.hooksJSON), &response); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			got := classifyCodexPluginHookTrust("traceary@market", response)
			if got.Status != tt.wantStatus || got.HookCount != tt.wantCount {
				t.Fatalf("classifyCodexPluginHookTrust() = status %q count %d, want status %q count %d", got.Status, got.HookCount, tt.wantStatus, tt.wantCount)
			}
		})
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
		{name: "untrusted", status: codexPluginHookTrustUntrusted, wantDoctor: doctorStatusWarn, wantMessage: "untrusted", wantFix: "codex"},
		{name: "modified", status: codexPluginHookTrustModified, wantDoctor: doctorStatusWarn, wantMessage: "modified", wantFix: "codex"},
		{name: "disabled", status: codexPluginHookTrustDisabled, wantDoctor: doctorStatusWarn, wantMessage: "disabled", wantFix: "codex"},
		{name: "undetectable", status: codexPluginHookTrustUndetectable, wantDoctor: doctorStatusSkip, wantMessage: "not a pass"},
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
