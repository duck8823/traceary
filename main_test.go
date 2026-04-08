package main

import (
	"bytes"
	"errors"
	"runtime/debug"
	"testing"
)

func TestWriteCLIError(t *testing.T) {
	t.Parallel()

	t.Run("plain text で user-facing error を出力する", func(t *testing.T) {
		t.Parallel()

		buffer := &bytes.Buffer{}
		if err := writeCLIError(buffer, testError("boom")); err != nil {
			t.Fatalf("writeCLIError() error = %v", err)
		}
		if buffer.String() != "Error: boom\n" {
			t.Fatalf("buffer = %q, want %q", buffer.String(), "Error: boom\n")
		}
	})

	t.Run("nil error なら何も出力しない", func(t *testing.T) {
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

func TestResolveBuildMetadata(t *testing.T) {
	t.Parallel()

	t.Run("明示値がある場合は build info より優先する", func(t *testing.T) {
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

	t.Run("dev build は build info を使って埋める", func(t *testing.T) {
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

	t.Run("build info がない場合は既定値を維持する", func(t *testing.T) {
		t.Parallel()

		got := resolveBuildMetadata("dev", "none", "unknown", func() (*debug.BuildInfo, bool) {
			return nil, false
		})
		if got.version != "dev" || got.commit != "none" || got.date != "unknown" {
			t.Fatalf("resolveBuildMetadata() = %+v", got)
		}
	})
}
