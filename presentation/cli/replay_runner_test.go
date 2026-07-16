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

func TestRootCLI_Replay_MarkdownTruncatesTimelineActivity(t *testing.T) {
	t.Parallel()

	// Multi-kilobyte prompt-style summary must not be embedded raw in replay
	// Markdown (same bound as timeline: timelineSummaryMaxRune = 72).
	huge := strings.Repeat("PROMPT-BODY-", 5000) // ~60KB, well over 72 runes
	marker := "UNIQUE_REPLAY_MARKER_SHOULD_NOT_APPEAR_IN_FULL"
	huge = marker + huge
	block := apptypes.TimelineBlockOf(
		time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 21, 12, 30, 0, 0, time.UTC),
		3,
		[]string{"codex"},
		[]apptypes.TimelineWorkspaceBreakdown{
			apptypes.TimelineWorkspaceBreakdownOf(
				"duck8823/traceary",
				3,
				[]string{"prompt"},
				[]string{"codex"},
				huge,
				apptypes.TimelineSummarySourcePrompt,
			),
		},
	)
	bundle := apptypes.ReplayBundleOf(
		time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC),
		nil,
		nil,
		[]apptypes.TimelineBlock{block},
		nil,
	)
	outPath := filepath.Join(t.TempDir(), "replay.md")
	rootCmd := newTestRootCLI(
		cli.WithStoreManagement(&storeManagementUsecaseStub{}),
		cli.WithReplay(&replayUsecaseStub{bundle: bundle}),
	).Command()
	rootCmd.SetOut(&bytes.Buffer{})
	rootCmd.SetErr(&bytes.Buffer{})
	rootCmd.SetArgs([]string{"replay", "--format", "markdown", "--out", outPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "# Traceary replay") {
		t.Fatalf("missing title")
	}
	if strings.Contains(text, huge) {
		t.Fatalf("markdown embedded full timeline activity (%d bytes)", len(huge))
	}
	// Truncated activity may keep a short prefix of the marker, but not the full multi-KB body.
	if len(text) > 200_000 {
		t.Fatalf("markdown too large (%d bytes); expected bounded digest", len(text))
	}
	// The unbounded PROMPT-BODY repetition must not appear hundreds of times.
	if strings.Count(text, "PROMPT-BODY-") > 5 {
		t.Fatalf("timeline activity not truncated: PROMPT-BODY- count=%d", strings.Count(text, "PROMPT-BODY-"))
	}
}
