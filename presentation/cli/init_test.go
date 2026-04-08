package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/presentation/cli"
)

type initializeStoreUsecaseStub struct {
	receivedPath string
	called       bool
	err          error
}

func (s *initializeStoreUsecaseStub) Run(_ context.Context, dbPath string) error {
	s.called = true
	s.receivedPath = dbPath
	return s.err
}

var _ usecase.InitializeStoreUsecase = (*initializeStoreUsecaseStub)(nil)

func TestRootCLI_InitCommand(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	stub := &initializeStoreUsecaseStub{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase: stub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"init", "--db-path", dbPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !stub.called {
		t.Fatalf("Run() was not called")
	}
	if stub.receivedPath != dbPath {
		t.Fatalf("Run() path = %q, want %q", stub.receivedPath, dbPath)
	}
	wantOutput := "Initialized: " + dbPath + "\n"
	if stdout.String() != wantOutput {
		t.Fatalf("stdout = %q, want %q", stdout.String(), wantOutput)
	}
}

func TestResolveDBPath(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		userHome   string
		wantSuffix string
		wantErr    bool
	}{
		{
			name:       "未指定時はホーム配下の .config を返す",
			input:      "",
			userHome:   t.TempDir(),
			wantSuffix: filepath.Join(".config", "traceary", "traceary.db"),
			wantErr:    false,
		},
		{
			name:       "指定時は指定パスを絶対パス化する",
			input:      filepath.Join(".", "tmp", "traceary.db"),
			userHome:   t.TempDir(),
			wantSuffix: filepath.Join("tmp", "traceary.db"),
			wantErr:    false,
		},
		{
			name:       "TRACEARY_DB_PATH があればそれを使う",
			input:      "",
			userHome:   t.TempDir(),
			wantSuffix: filepath.Join("env", "traceary.db"),
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "TRACEARY_DB_PATH があればそれを使う" {
				t.Setenv("TRACEARY_DB_PATH", filepath.Join(tt.userHome, "env", "traceary.db"))
			}
			cli.SetUserHomeDirFunc(func() (string, error) {
				return tt.userHome, nil
			})
			defer cli.ResetUserHomeDirFunc()

			got, err := cli.ResolveDBPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveDBPath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !strings.HasSuffix(got, tt.wantSuffix) {
				t.Fatalf("ResolveDBPath() path = %q, want suffix %q", got, tt.wantSuffix)
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("ResolveDBPath() path = %q, want absolute path", got)
			}
		})
	}
}

func TestRootCLI_InitCommand_UsesTracearyDBPathEnv(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "traceary.db")
	t.Setenv("TRACEARY_DB_PATH", dbPath)

	stub := &initializeStoreUsecaseStub{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
		InitializeStoreUsecase: stub,
	}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"init"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stub.receivedPath != dbPath {
		t.Fatalf("Run() path = %q, want %q", stub.receivedPath, dbPath)
	}
	wantOutput := "Initialized: " + dbPath + "\n"
	if stdout.String() != wantOutput {
		t.Fatalf("stdout = %q, want %q", stdout.String(), wantOutput)
	}
}

func TestRootCLI_InitHelp_ExplainsOptionalBootstrap(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	rootCmd := cli.NewRootCLI(cli.RootCLIOptions{}).Command()
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs([]string{"init", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "Other traceary commands create the DB and apply migrations on demand.") {
		t.Fatalf("stdout = %q, want init help to mention automatic DB creation", output)
	}
	if !strings.Contains(output, "Use init when you want to verify the DB path or write permissions before a session starts.") {
		t.Fatalf("stdout = %q, want init help to mention explicit bootstrap purpose", output)
	}
}
