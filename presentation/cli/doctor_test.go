package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

type doctorInitializeStoreUsecaseStub struct {
	paths []string
	err   error
}

func (s *doctorInitializeStoreUsecaseStub) Run(_ context.Context, dbPath string) error {
	s.paths = append(s.paths, dbPath)
	return s.err
}

type doctorReportJSON struct {
	DBPath         string            `json:"db_path"`
	HookScriptsDir string            `json:"hook_scripts_dir"`
	Clients        []string          `json:"clients"`
	Checks         []doctorCheckJSON `json:"checks"`
}

type doctorCheckJSON struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func TestRootCLI_DoctorCommand(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir()
	scriptsDir := filepath.Join(t.TempDir(), "hook-scripts")
	t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", scriptsDir)
	cli.SetUserHomeDirFunc(func() (string, error) {
		return homeDir, nil
	})
	t.Cleanup(cli.ResetUserHomeDirFunc)

	t.Run("all clients without config only warns", func(t *testing.T) {
		initializeStore := &doctorInitializeStoreUsecaseStub{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initializeStore,
		}).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"doctor",
			"--project-dir", projectDir,
			"--json",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		var report doctorReportJSON
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if len(report.Clients) != 3 {
			t.Fatalf("len(report.Clients) = %d, want 3", len(report.Clients))
		}
		if len(initializeStore.paths) != 1 {
			t.Fatalf("len(initializeStore.paths) = %d, want 1", len(initializeStore.paths))
		}
		assertDoctorCheckStatus(t, report.Checks, "claude-config", "warn")
		assertDoctorCheckStatus(t, report.Checks, "codex-config", "warn")
		assertDoctorCheckStatus(t, report.Checks, "gemini-config", "warn")
	})

	t.Run("specific client without config warns", func(t *testing.T) {
		initializeStore := &doctorInitializeStoreUsecaseStub{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initializeStore,
		}).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"doctor",
			"--client", "claude",
			"--project-dir", projectDir,
			"--json",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		var report doctorReportJSON
		if unmarshalErr := json.Unmarshal(stdout.Bytes(), &report); unmarshalErr != nil {
			t.Fatalf("json.Unmarshal() error = %v", unmarshalErr)
		}
		assertDoctorCheckStatus(t, report.Checks, "claude-config", "warn")
	})

	t.Run("hook script materialization issues warn instead of failing", func(t *testing.T) {
		initializeStore := &doctorInitializeStoreUsecaseStub{}
		blockedPath := filepath.Join(t.TempDir(), "blocked")
		if err := os.WriteFile(blockedPath, []byte("not-a-directory"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		t.Setenv("TRACEARY_HOOK_SCRIPTS_DIR", blockedPath)

		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initializeStore,
		}).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"doctor",
			"--client", "claude",
			"--project-dir", projectDir,
			"--json",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		var report doctorReportJSON
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		assertDoctorCheckStatus(t, report.Checks, "hook-scripts", "warn")
	})

	t.Run("existing Traceary config passes", func(t *testing.T) {
		settingsPath := filepath.Join(projectDir, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(settingsPath, []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "bash '/tmp/scripts/traceary-session.sh' 'claude' 'start'"
          }
        ]
      }
    ]
  }
}
`), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}

		initializeStore := &doctorInitializeStoreUsecaseStub{}
		rootCmd := cli.NewRootCLI(cli.RootCLIOptions{
			InitializeStoreUsecase: initializeStore,
		}).Command()
		stdout := &bytes.Buffer{}
		rootCmd.SetOut(stdout)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{
			"doctor",
			"--client", "claude",
			"--project-dir", projectDir,
			"--json",
		})

		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}

		var report doctorReportJSON
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		assertDoctorCheckStatus(t, report.Checks, "claude-config", "pass")
		if report.HookScriptsDir == "" {
			t.Fatalf("HookScriptsDir = empty, want non-empty")
		}
	})
}

func assertDoctorCheckStatus(t *testing.T, checks []doctorCheckJSON, name string, want string) {
	t.Helper()

	for _, check := range checks {
		if check.Name == name {
			if check.Status != want {
				t.Fatalf("check %s status = %q, want %q", name, check.Status, want)
			}
			return
		}
	}

	t.Fatalf("check %s not found", name)
}
