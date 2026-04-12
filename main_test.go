package main

import (
	"bytes"
	"errors"
	"os"
	"runtime/debug"
	"testing"
)

func TestWriteCLIError(t *testing.T) {
	t.Parallel()

	t.Run("prints user-facing error as plain text", func(t *testing.T) {
		t.Parallel()

		buffer := &bytes.Buffer{}
		if err := writeCLIError(buffer, testError("boom")); err != nil {
			t.Fatalf("writeCLIError() error = %v", err)
		}
		if buffer.String() != "Error: boom\n" {
			t.Fatalf("buffer = %q, want %q", buffer.String(), "Error: boom\n")
		}
	})

	t.Run("prints nothing for nil error", func(t *testing.T) {
		t.Parallel()

		buffer := &bytes.Buffer{}
		if err := writeCLIError(buffer, nil); err != nil {
			t.Fatalf("writeCLIError() error = %v", err)
		}
		if buffer.Len() != 0 {
			t.Fatalf("buffer = %q, want empty", buffer.String())
		}
	})
}

func TestCLICommandError_Unwrap(t *testing.T) {
	t.Parallel()

	baseErr := testError("boom")
	err := cliCommandError{err: baseErr}

	if err.Error() != "boom" {
		t.Fatalf("err.Error() = %q, want %q", err.Error(), "boom")
	}
	if !errors.Is(err, baseErr) {
		t.Fatal("errors.Is(err, baseErr) = false, want true")
	}
}

type testError string

func (e testError) Error() string { return string(e) }

func TestSetupLogger(t *testing.T) {
	t.Run("valid LOG_LEVEL is set without error", func(t *testing.T) {
		for _, level := range []string{"debug", "info", "warn", "warning", "error", "DEBUG", "Info"} {
			t.Setenv("LOG_LEVEL", level)
			if err := setupLogger(); err != nil {
				t.Errorf("setupLogger() with LOG_LEVEL=%q returned error: %v", level, err)
			}
		}
	})

	t.Run("invalid LOG_LEVEL returns error", func(t *testing.T) {
		t.Setenv("LOG_LEVEL", "invalid")
		if err := setupLogger(); err == nil {
			t.Fatal("setupLogger() with LOG_LEVEL=invalid returned nil, want error")
		}
	})

	t.Run("no error when LOG_LEVEL is unset", func(t *testing.T) {
		if err := os.Unsetenv("LOG_LEVEL"); err != nil {
			t.Fatal(err)
		}
		if err := setupLogger(); err != nil {
			t.Fatalf("setupLogger() without LOG_LEVEL returned error: %v", err)
		}
	})

	t.Run("no error when LOG_OPTION=development", func(t *testing.T) {
		t.Setenv("LOG_OPTION", "development")
		if err := setupLogger(); err != nil {
			t.Fatalf("setupLogger() with LOG_OPTION=development returned error: %v", err)
		}
	})
}

func TestResolveBuildMetadata(t *testing.T) {
	t.Parallel()

	t.Run("explicit values take precedence over build info", func(t *testing.T) {
		t.Parallel()

		got := resolveBuildMetadata("v1.2.3", "commit-explicit", "2026-04-08T00:00:00Z", func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{
				Main: debug.Module{Version: "v9.9.9"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "commit-buildinfo"},
					{Key: "vcs.time", Value: "2026-04-07T00:00:00Z"},
				},
			}, true
		})

		if got.version != "v1.2.3" || got.commit != "commit-explicit" || got.date != "2026-04-08T00:00:00Z" {
			t.Fatalf("resolveBuildMetadata() = %+v", got)
		}
	})

	t.Run("dev build fills from build info", func(t *testing.T) {
		t.Parallel()

		got := resolveBuildMetadata("dev", "none", "unknown", func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{
				Main: debug.Module{Version: "v0.1.7"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abcdef123456"},
					{Key: "vcs.time", Value: "2026-04-08T03:00:00Z"},
				},
			}, true
		})

		if got.version != "v0.1.7" {
			t.Fatalf("version = %q, want %q", got.version, "v0.1.7")
		}
		if got.commit != "abcdef123456" {
			t.Fatalf("commit = %q, want %q", got.commit, "abcdef123456")
		}
		if got.date != "2026-04-08T03:00:00Z" {
			t.Fatalf("date = %q, want %q", got.date, "2026-04-08T03:00:00Z")
		}
	})

	t.Run("keeps default values when build info is unavailable", func(t *testing.T) {
		t.Parallel()

		got := resolveBuildMetadata("dev", "none", "unknown", func() (*debug.BuildInfo, bool) {
			return nil, false
		})
		if got.version != "dev" || got.commit != "none" || got.date != "unknown" {
			t.Fatalf("resolveBuildMetadata() = %+v", got)
		}
	})
}
