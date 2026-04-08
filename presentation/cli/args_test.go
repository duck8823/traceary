package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_ArgumentErrorsAreLocalized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "log の引数不足は日本語",
			args:    []string{"log"},
			wantErr: "引数はちょうど 1 個必要です (受け取った引数数: 0)",
		},
		{
			name:    "audit の引数不足は日本語",
			args:    []string{"audit", "go test ./..."},
			wantErr: "引数はちょうど 3 個必要です (受け取った引数数: 1)",
		},
		{
			name:    "search の引数超過は日本語",
			args:    []string{"search", "foo", "bar"},
			wantErr: "引数は最大 1 個まで指定できます (受け取った引数数: 2)",
		},
		{
			name:    "init の余分な引数は日本語",
			args:    []string{"init", "extra"},
			wantErr: "引数は不要です (受け取った引数数: 1)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
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
