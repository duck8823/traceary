package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// geminiIneligibleTierStderrFixture mirrors the 2026-08-21 dogfood
// observation: `gemini -p` on a Gemini Code Assist individual (free-tier)
// account fails with this error class on stderr and records only
// session_started/session_ended into a throwaway store.
const geminiIneligibleTierStderrFixture = `Error: IneligibleTierError
This account is no longer supported for Gemini Code Assist for individuals.
Please migrate to Antigravity or use an eligible plan.`

func TestClassifyGeminiEligibility_IneligibleTierFixture(t *testing.T) {
	t.Parallel()

	result := classifyGeminiEligibility(context.Background(), geminiIneligibleTierStderrFixture, errors.New("exit status 1"))
	if result.Status != geminiEligibilityIneligible {
		t.Fatalf("Status = %q, want %q", result.Status, geminiEligibilityIneligible)
	}
}

func TestClassifyGeminiEligibility_SuccessIsEligible(t *testing.T) {
	t.Parallel()

	result := classifyGeminiEligibility(context.Background(), "", nil)
	if result.Status != geminiEligibilityEligible {
		t.Fatalf("Status = %q, want %q", result.Status, geminiEligibilityEligible)
	}
}

func TestClassifyGeminiEligibility_TimeoutIsUndetermined(t *testing.T) {
	t.Parallel()

	probeCtx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-probeCtx.Done()

	result := classifyGeminiEligibility(probeCtx, "", errors.New("signal: killed"))
	if result.Status != geminiEligibilityUndetermined {
		t.Fatalf("Status = %q, want %q", result.Status, geminiEligibilityUndetermined)
	}
	if !strings.Contains(result.Reason, "timed out") {
		t.Fatalf("Reason = %q, want timeout detail", result.Reason)
	}
}

func TestClassifyGeminiEligibility_OtherExitErrorIsUndetermined(t *testing.T) {
	t.Parallel()

	result := classifyGeminiEligibility(context.Background(), "some unrelated host error", errors.New("exit status 2"))
	if result.Status != geminiEligibilityUndetermined {
		t.Fatalf("Status = %q, want %q", result.Status, geminiEligibilityUndetermined)
	}
	if result.Reason == "" {
		t.Fatal("Reason is empty for undetermined exit error")
	}
}

func TestBuildGeminiEligibilityCheck_IneligibleWarnsWithoutFixCommand(t *testing.T) {
	t.Parallel()

	check := buildGeminiEligibilityCheck(geminiEligibilityResult{Status: geminiEligibilityIneligible})
	if check.Name != "gemini-host-eligibility" {
		t.Fatalf("Name = %q, want gemini-host-eligibility", check.Name)
	}
	if check.Status != doctorStatusWarn {
		t.Fatalf("Status = %q, want warn", check.Status)
	}
	if !strings.Contains(check.Message, "IneligibleTierError") {
		t.Fatalf("Message = %q, want the IneligibleTierError class named", check.Message)
	}
	if check.FixCommand != "" || check.AutoFixAvailable {
		t.Fatalf("FixCommand = %q, AutoFixAvailable = %t; Traceary cannot fix Google eligibility", check.FixCommand, check.AutoFixAvailable)
	}
}

func TestBuildGeminiEligibilityCheck_EligiblePasses(t *testing.T) {
	t.Parallel()

	check := buildGeminiEligibilityCheck(geminiEligibilityResult{Status: geminiEligibilityEligible})
	if check.Status != doctorStatusPass {
		t.Fatalf("Status = %q, want pass", check.Status)
	}
}

func TestBuildGeminiEligibilityCheck_UndeterminedSkips(t *testing.T) {
	t.Parallel()

	check := buildGeminiEligibilityCheck(geminiEligibilityResult{
		Status: geminiEligibilityUndetermined,
		Reason: "gemini CLI is not on PATH",
	})
	if check.Status != doctorStatusSkip {
		t.Fatalf("Status = %q, want skip", check.Status)
	}
	if !strings.Contains(check.Message, "gemini CLI is not on PATH") {
		t.Fatalf("Message = %q, want the undetermined reason", check.Message)
	}
}

func TestInspectGeminiHostEligibility_SkipsProbeWithoutWiredHooks(t *testing.T) {
	original := geminiEligibilityProbeFunc
	t.Cleanup(func() { geminiEligibilityProbeFunc = original })
	geminiEligibilityProbeFunc = func(context.Context, string) geminiEligibilityResult {
		t.Fatal("probe must not run when hooks are not wired")
		return geminiEligibilityResult{}
	}

	check := (&RootCLI{}).inspectGeminiHostEligibility(context.Background(), false, t.TempDir())
	if check.Name != "gemini-host-eligibility" {
		t.Fatalf("Name = %q, want gemini-host-eligibility", check.Name)
	}
	if check.Status != doctorStatusSkip {
		t.Fatalf("Status = %q, want skip", check.Status)
	}
}

func TestInspectGeminiHostEligibility_WarnsOnIneligibleFixture(t *testing.T) {
	original := geminiEligibilityProbeFunc
	t.Cleanup(func() { geminiEligibilityProbeFunc = original })
	geminiEligibilityProbeFunc = func(context.Context, string) geminiEligibilityResult {
		return classifyGeminiEligibility(context.Background(), geminiIneligibleTierStderrFixture, errors.New("exit status 1"))
	}

	check := (&RootCLI{}).inspectGeminiHostEligibility(context.Background(), true, t.TempDir())
	if check.Status != doctorStatusWarn {
		t.Fatalf("Status = %q, want warn for the ineligible fixture", check.Status)
	}
	if !strings.Contains(check.Message, "IneligibleTierError") {
		t.Fatalf("Message = %q, want the IneligibleTierError class named", check.Message)
	}
}

func TestGeminiEligibilityProbeEnv_PinsThrowawayStore(t *testing.T) {
	t.Setenv(dbPathEnvKey, "/operator/live.db")
	t.Setenv("TRACEARY_HOOK_STATE_DIR", "/operator/hook-state")

	env := geminiEligibilityProbeEnv("/tmp/probe-dir")
	var dbPath, hookStateDir string
	dbPathCount := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, dbPathEnvKey+"=") {
			dbPathCount++
			dbPath = strings.TrimPrefix(entry, dbPathEnvKey+"=")
		}
		if strings.HasPrefix(entry, "TRACEARY_HOOK_STATE_DIR=") {
			hookStateDir = strings.TrimPrefix(entry, "TRACEARY_HOOK_STATE_DIR=")
		}
	}
	if dbPathCount != 1 || dbPath != "/tmp/probe-dir/probe.db" {
		t.Fatalf("%s entries = %d value %q, want exactly one throwaway store path", dbPathEnvKey, dbPathCount, dbPath)
	}
	if hookStateDir != "/tmp/probe-dir/hook-state" {
		t.Fatalf("TRACEARY_HOOK_STATE_DIR = %q, want throwaway hook state dir", hookStateDir)
	}
}

func TestCappedBuffer_DiscardsExcess(t *testing.T) {
	t.Parallel()

	buf := &cappedBuffer{max: 4}
	if n, err := buf.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write() = %d, %v; want 6, nil", n, err)
	}
	if got := buf.String(); got != "abcd" {
		t.Fatalf("String() = %q, want %q", got, "abcd")
	}
}
