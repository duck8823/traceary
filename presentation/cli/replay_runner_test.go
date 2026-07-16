package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/presentation/cli"

	apptypes "github.com/duck8823/traceary/application/types"
)

type replayUsecaseStub struct {
	bundle apptypes.ReplayBundle
	err    error
}

func (s *replayUsecaseStub) Bundle(context.Context, apptypes.ReplayCriteria) (apptypes.ReplayBundle, error) {
	return s.bundle, s.err
}

// TestRootCLI_Replay_RequiresReplayUsecase asserts that invoking
// `traceary replay` without WithReplay produces a configuration error
// instead of silently running on a zero-value usecase. Regression
// guard for the architect feedback on #658 that removed the
// presentation-layer fallback.
func TestRootCLI_Replay_RequiresReplayUsecase(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
	).Command()
	stdout := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{
		"replay",
		"--out", filepath.Join(t.TempDir(), "replay.html"),
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want configuration error")
	}
	if !strings.Contains(err.Error(), "replay") {
		t.Errorf("err = %v, want mention of replay usecase", err)
	}
}

// TestRootCLI_Replay_SurfacesBundleError asserts that an error from
// ReplayUsecase.Bundle bubbles up through runReplay with the localized
// wrapper so operators see a clear failure mode.
func TestRootCLI_Replay_SurfacesBundleError(t *testing.T) {
	t.Parallel()

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithReplay(&replayUsecaseStub{err: errors.New("boom")}),
	).Command()
	stdout := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{
		"replay",
		"--out", filepath.Join(t.TempDir(), "replay.html"),
	})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want wrapped bundle error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want underlying 'boom' to appear in wrapper", err)
	}
}

// TestRootCLI_Replay_WritesHTMLOnSuccess asserts that the end-to-end
// CLI path — ReplayUsecase.Bundle → replayDataFromBundle →
// writeReplayHTML — produces a browser-readable file on disk when
// everything wires correctly.
func TestRootCLI_Replay_WritesHTMLOnSuccess(t *testing.T) {
	t.Parallel()

	outPath := filepath.Join(t.TempDir(), "replay.html")
	bundle := apptypes.ReplayBundleOf(time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC), nil, nil, nil, nil)

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithReplay(&replayUsecaseStub{bundle: bundle}),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"replay",
		"--out", outPath,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("replay HTML should be written on success; stat err = %v", err)
	}
	if !strings.Contains(stdout.String(), "Wrote replay HTML") && !strings.Contains(stdout.String(), "書き出しました") {
		t.Errorf("stdout = %q, want success summary line", stdout.String())
	}
}

func TestRootCLI_Replay_WritesMarkdownOnSuccess(t *testing.T) {
	t.Parallel()

	outPath := filepath.Join(t.TempDir(), "replay.md")
	bundle := apptypes.ReplayBundleOf(time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC), nil, nil, nil, nil)

	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithReplay(&replayUsecaseStub{bundle: bundle}),
	).Command()
	stdout := &bytes.Buffer{}
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{
		"replay",
		"--format", "markdown",
		"--out", outPath,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(string(body), "# Traceary replay") {
		t.Fatalf("markdown missing title: %s", body)
	}
	if !strings.Contains(stdout.String(), "Markdown") && !strings.Contains(stdout.String(), "markdown") {
		t.Errorf("stdout = %q, want markdown success line", stdout.String())
	}
}
