package main

import (
	"bytes"
	"os"
	"runtime/debug"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/xerrors"
)

func TestIsHookCommandArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "direct hook command", args: []string{"traceary", "hook", "prompt", "claude"}, want: true},
		{name: "global flag before hook", args: []string{"traceary", "--config", "config.json", "hook", "prompt", "claude"}, want: true},
		{name: "ordinary command", args: []string{"traceary", "doctor", "--client", "claude"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isHookCommandArgs(tt.args); got != tt.want {
				t.Fatalf("isHookCommandArgs(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestIsDetachedHookWorkerArgs(t *testing.T) {
	t.Parallel()
	if !isDetachedHookWorkerArgs([]string{"traceary", "hook", "memory-extract-worker", "--job", "x"}) {
		t.Fatal("memory-extract-worker should be detached")
	}
	if !isDetachedHookWorkerArgs([]string{"traceary", "hook", "grok-transcript-worker", "--job", "x"}) {
		t.Fatal("grok-transcript-worker should be detached")
	}
	if isDetachedHookWorkerArgs([]string{"traceary", "hook", "prompt", "claude"}) {
		t.Fatal("prompt must not be treated as detached worker")
	}
}

func TestResolveHookSoftDeadline(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv(hookSoftDeadlineEnvKey, "")
		_ = os.Unsetenv(hookSoftDeadlineEnvKey)
		if got := resolveHookSoftDeadline(); got != defaultHookSoftDeadline {
			t.Fatalf("got %v, want %v", got, defaultHookSoftDeadline)
		}
	})
	t.Run("off", func(t *testing.T) {
		t.Setenv(hookSoftDeadlineEnvKey, "off")
		if got := resolveHookSoftDeadline(); got != 0 {
			t.Fatalf("got %v, want 0", got)
		}
	})
	t.Run("duration", func(t *testing.T) {
		t.Setenv(hookSoftDeadlineEnvKey, "4s")
		if got := resolveHookSoftDeadline(); got != 4*time.Second {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("seconds number", func(t *testing.T) {
		t.Setenv(hookSoftDeadlineEnvKey, "6")
		if got := resolveHookSoftDeadline(); got != 6*time.Second {
			t.Fatalf("got %v", got)
		}
	})
}

func TestCommandContext_AppliesSoftDeadlineToHooks(t *testing.T) {
	t.Setenv(hookSoftDeadlineEnvKey, "50ms")
	ctx, cancel := commandContext([]string{"traceary", "hook", "prompt", "claude"})
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("hook context must have a soft deadline")
	}
	if remaining := time.Until(deadline); remaining > 200*time.Millisecond || remaining < 0 {
		t.Fatalf("deadline remaining = %v", remaining)
	}
}

func TestCommandContext_DetachedWorkerHasNoSoftDeadline(t *testing.T) {
	t.Setenv(hookSoftDeadlineEnvKey, "50ms")
	ctx, cancel := commandContext([]string{"traceary", "hook", "memory-extract-worker", "--job", "x"})
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("detached worker must not receive host soft deadline")
	}
}

func TestWriteCLIError(t *testing.T) {
	t.Parallel()

	t.Run("prints user-facing error as plain text", func(t *testing.T) {
		t.Parallel()

		buffer := &bytes.Buffer{}
		if err := writeCLIError(buffer, testError("boom")); err != nil {
			t.Fatalf("writeCLIError() error = %v", err)
		}
		if diff := cmp.Diff("Error: boom\n", buffer.String()); diff != "" {
			t.Fatalf("buffer mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("prints nothing for nil error", func(t *testing.T) {
		t.Parallel()

		buffer := &bytes.Buffer{}
		if err := writeCLIError(buffer, nil); err != nil {
			t.Fatalf("writeCLIError() error = %v", err)
		}
		if diff := cmp.Diff("", buffer.String()); diff != "" {
			t.Fatalf("buffer mismatch (-want +got):\n%s", diff)
		}
	})
}

type testError string

func (e testError) Error() string { return string(e) }

func TestIsSilentCLIExitError(t *testing.T) {
	t.Parallel()

	t.Run("recognizes wrapped broken pipe", func(t *testing.T) {
		t.Parallel()

		err := xerrors.Errorf("failed to execute CLI command: %w", syscall.EPIPE)
		if !isSilentCLIExitError(err) {
			t.Fatal("isSilentCLIExitError() = false, want true")
		}
	})

	t.Run("keeps ordinary errors loud", func(t *testing.T) {
		t.Parallel()

		if isSilentCLIExitError(testError("database query failed")) {
			t.Fatal("isSilentCLIExitError() = true, want false")
		}
	})
}

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

		gotVersion, gotCommit, gotDate := resolveBuildMetadata("v1.2.3", "commit-explicit", "2026-04-08T00:00:00Z", func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{
				Main: debug.Module{Version: "v9.9.9"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "commit-buildinfo"},
					{Key: "vcs.time", Value: "2026-04-07T00:00:00Z"},
				},
			}, true
		})

		if gotVersion != "v1.2.3" || gotCommit != "commit-explicit" || gotDate != "2026-04-08T00:00:00Z" {
			t.Fatalf("resolveBuildMetadata() = (%q, %q, %q)", gotVersion, gotCommit, gotDate)
		}
	})

	t.Run("dev build fills from build info", func(t *testing.T) {
		t.Parallel()

		gotVersion, gotCommit, gotDate := resolveBuildMetadata("dev", "none", "unknown", func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{
				Main: debug.Module{Version: "v0.1.7"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abcdef123456"},
					{Key: "vcs.time", Value: "2026-04-08T03:00:00Z"},
				},
			}, true
		})

		if diff := cmp.Diff("v0.1.7", gotVersion); diff != "" {
			t.Fatalf("version mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("abcdef123456", gotCommit); diff != "" {
			t.Fatalf("commit mismatch (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff("2026-04-08T03:00:00Z", gotDate); diff != "" {
			t.Fatalf("date mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("keeps default values when build info is unavailable", func(t *testing.T) {
		t.Parallel()

		gotVersion, gotCommit, gotDate := resolveBuildMetadata("dev", "none", "unknown", func() (*debug.BuildInfo, bool) {
			return nil, false
		})
		if diff := cmp.Diff([]string{"dev", "none", "unknown"}, []string{gotVersion, gotCommit, gotDate}); diff != "" {
			t.Fatalf("resolveBuildMetadata() mismatch (-want +got):\n%s", diff)
		}
	})
}
