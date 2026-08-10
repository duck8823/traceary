package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
)

func TestApplyCommandDeprecation(t *testing.T) {
	tests := []struct {
		name       string
		language   string
		wantNotice string
	}{
		{
			name:       "English notice",
			wantNotice: "DEPRECATED: this command is deprecated, use `traceary sessions` instead. Removal target: v0.35.\n",
		},
		{
			name:       "Japanese notice",
			language:   "ja",
			wantNotice: "DEPRECATED: このコマンドは非推奨です。代わりに `traceary sessions` を使用してください。削除予定: v0.35。\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.language != "" {
				t.Setenv(cliLanguageEnvKey, tt.language)
			}
			var stdout, stderr bytes.Buffer
			preRunCalls := 0
			cmd := &cobra.Command{
				Use: "test",
				PreRunE: func(*cobra.Command, []string) error {
					preRunCalls++
					return nil
				},
				Run: func(cmd *cobra.Command, _ []string) {
					_, _ = cmd.OutOrStdout().Write([]byte("command output\n"))
				},
			}
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			applyCommandDeprecation(cmd, "traceary sessions", "v0.35")

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantNotice, stderr.String()); diff != "" {
				t.Errorf("stderr notice mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff("command output\n", stdout.String()); diff != "" {
				t.Errorf("stdout mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(1, preRunCalls); diff != "" {
				t.Errorf("PreRunE calls mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(1, strings.Count(stderr.String(), "\n")); diff != "" {
				t.Errorf("notice line count mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWriteDeprecationNoticeWithoutReplacement(t *testing.T) {
	tests := []struct {
		name       string
		language   string
		wantNotice string
	}{
		{
			name:       "English notice",
			wantNotice: "DEPRECATED: this command is deprecated with no replacement. Removal target: v0.35.\n",
		},
		{
			name:       "Japanese notice",
			language:   "ja",
			wantNotice: "DEPRECATED: このコマンドは非推奨です。置き換え先はありません。削除予定: v0.35。\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.language != "" {
				t.Setenv(cliLanguageEnvKey, tt.language)
			}
			var stderr bytes.Buffer
			cmd := &cobra.Command{Use: "test"}
			cmd.SetErr(&stderr)

			writeDeprecationNotice(cmd, "this command", "このコマンド", "", "v0.35")

			if diff := cmp.Diff(tt.wantNotice, stderr.String()); diff != "" {
				t.Errorf("stderr notice mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// The live dashboard runs for both `top` and `sessions`, but `top` already
// carries a command-level notice. Only the decision is unit-testable: the TTY
// branch itself reads the real stdin/stdout and has no injection seam.
func TestInteractiveDashboardNoticeApplies(t *testing.T) {
	tests := []struct {
		name        string
		commandName string
		want        bool
	}{
		{name: "sessions owns the mode notice", commandName: "sessions", want: true},
		{name: "top is covered by its command notice", commandName: "top", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, interactiveDashboardNoticeApplies(tt.commandName)); diff != "" {
				t.Errorf("interactiveDashboardNoticeApplies(%q) mismatch (-want +got):\n%s", tt.commandName, diff)
			}
		})
	}
}
