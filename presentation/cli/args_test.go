package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ArgumentErrorsDefaultToEnglish(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "log の引数不足は英語",
			args:    []string{"log"},
			wantErr: "expected exactly 1 positional argument(s) (received: 0)",
		},
		{
			name:    "audit の引数不足は英語",
			args:    []string{"audit"},
			wantErr: "either --command or positional argument 1 is required",
		},
		{
			name:    "search の引数超過は英語",
			args:    []string{"search", "foo", "bar"},
			wantErr: "expected at most 1 positional argument(s) (received: 2)",
		},
		{
			name:    "init の余分な引数は英語",
			args:    []string{"init", "extra"},
			wantErr: "this command does not accept positional arguments (received: 1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rootCmd := cli.NewRootCLI().Command()
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetErr(&bytes.Buffer{})
			rootCmd.SetArgs(tt.args)

			err := rootCmd.Execute()
			if err == nil {
				t.Fatal("Execute() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRootCLI_ArgumentErrorsCanUseJapanese(t *testing.T) {
	t.Setenv("TRACEARY_LANG", "ja")

	rootCmd := cli.NewRootCLI().Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"log"})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "引数はちょうど 1 個必要です (受け取った引数数: 0)") {
		t.Fatalf("error = %q", err.Error())
	}
}
