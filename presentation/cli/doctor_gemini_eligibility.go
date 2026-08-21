package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// geminiEligibilityProbeTimeout bounds the headless `gemini -p` eligibility
// probe. The ineligible-tier rejection fails fast; the budget exists for slow
// but eligible accounts and matches the install script's default
// TRACEARY_GEMINI_TIMEOUT (60s).
const geminiEligibilityProbeTimeout = 60 * time.Second

// geminiEligibilityProbePrompt is a deliberately tiny prompt so the probe
// costs almost nothing on eligible accounts. `--approval-mode plan` keeps the
// run free of tool execution, matching the dogfood observation flags.
const geminiEligibilityProbePrompt = "Reply with the single word ok."

// geminiEligibilityStderrCap bounds how much of the probe's stderr is kept
// for classification. The eligibility signature appears at the start of the
// error dump; the rest is never surfaced in doctor output.
const geminiEligibilityStderrCap = 64 * 1024

type geminiEligibilityStatus string

const (
	// geminiEligibilityEligible means the bounded `gemini -p` probe exited
	// successfully, so this account can serve the run and the wired
	// BeforeAgent hook can record prompt events.
	geminiEligibilityEligible geminiEligibilityStatus = "eligible"
	// geminiEligibilityIneligible means the probe was rejected with the
	// IneligibleTierError class (Gemini Code Assist for individuals is no
	// longer served). The host aborts the run after SessionStart, so
	// prompt/transcript events are absent even though hooks are wired.
	geminiEligibilityIneligible geminiEligibilityStatus = "ineligible"
	// geminiEligibilityUndetermined covers every other outcome (no binary,
	// timeout, unrelated exit error): doctor reports skip, never pass.
	geminiEligibilityUndetermined geminiEligibilityStatus = "undetermined"
)

type geminiEligibilityResult struct {
	Status geminiEligibilityStatus
	// Reason is a short operator-safe detail for undetermined outcomes. It
	// never echoes host stderr, which may carry account-specific text.
	Reason string
}

// geminiEligibilityProbeFunc is stubbed in tests with fixture results so CI
// never needs live Google auth.
var geminiEligibilityProbeFunc = probeGeminiHeadlessEligibility

var geminiEligibilityLookPath = exec.LookPath

// probeGeminiHeadlessEligibility re-runs the dogfood observation in a bounded
// form: `gemini -p <trivial> --approval-mode plan` from the project directory
// with a throwaway TRACEARY_DB_PATH / TRACEARY_HOOK_STATE_DIR, so installed
// Traceary hooks record into a temp store instead of the operator's live one.
func probeGeminiHeadlessEligibility(ctx context.Context, projectDir string) geminiEligibilityResult {
	if _, err := geminiEligibilityLookPath("gemini"); err != nil {
		return geminiEligibilityResult{
			Status: geminiEligibilityUndetermined,
			Reason: "gemini CLI is not on PATH",
		}
	}
	probeDir, err := os.MkdirTemp("", "traceary-gemini-eligibility-")
	if err != nil {
		return geminiEligibilityResult{
			Status: geminiEligibilityUndetermined,
			Reason: "could not allocate a throwaway probe store directory",
		}
	}
	defer func() { _ = os.RemoveAll(probeDir) }()

	probeCtx, cancel := context.WithTimeout(ctx, geminiEligibilityProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "gemini", "-p", geminiEligibilityProbePrompt, "--approval-mode", "plan") // #nosec G204 -- fixed host CLI and probe arguments
	cmd.Dir = projectDir
	cmd.Stdout = io.Discard
	stderr := &cappedBuffer{max: geminiEligibilityStderrCap}
	cmd.Stderr = stderr
	cmd.Env = geminiEligibilityProbeEnv(probeDir)

	runErr := cmd.Run()
	return classifyGeminiEligibility(probeCtx, stderr.String(), runErr)
}

// geminiEligibilityProbeEnv pins the probe's Traceary side effects to the
// throwaway directory, replacing any operator-level overrides so hook writes
// can never land in the live store.
func geminiEligibilityProbeEnv(probeDir string) []string {
	const hookStateDirEnvKey = "TRACEARY_HOOK_STATE_DIR"
	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, dbPathEnvKey+"=") || strings.HasPrefix(entry, hookStateDirEnvKey+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		dbPathEnvKey+"="+filepath.Join(probeDir, "probe.db"),
		hookStateDirEnvKey+"="+filepath.Join(probeDir, "hook-state"),
	)
}

// classifyGeminiEligibility maps the probe outcome onto the three-state
// classification. The IneligibleTierError signature is matched on stderr only;
// a successful exit is the single eligible signal, and everything else stays
// undetermined so doctor never upgrades an unknown host to pass.
func classifyGeminiEligibility(probeCtx context.Context, stderr string, runErr error) geminiEligibilityResult {
	lowered := strings.ToLower(stderr)
	if strings.Contains(lowered, "ineligibletiererror") ||
		strings.Contains(lowered, "no longer supported for gemini code assist for individuals") {
		return geminiEligibilityResult{Status: geminiEligibilityIneligible}
	}
	if probeCtx.Err() == context.DeadlineExceeded {
		return geminiEligibilityResult{
			Status: geminiEligibilityUndetermined,
			Reason: "probe timed out",
		}
	}
	if runErr == nil {
		return geminiEligibilityResult{Status: geminiEligibilityEligible}
	}
	return geminiEligibilityResult{
		Status: geminiEligibilityUndetermined,
		Reason: "probe exited non-zero without the ineligible-tier signature",
	}
}

// cappedBuffer keeps at most max bytes of a child's output; excess bytes are
// discarded but reported as written so the child is never blocked.
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := b.max - b.buf.Len(); remaining > 0 {
		chunk := p
		if len(chunk) > remaining {
			chunk = chunk[:remaining]
		}
		_, _ = b.buf.Write(chunk)
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

// inspectGeminiHostEligibility runs the bounded eligibility probe only when
// the project config check passed — i.e. Traceary-managed hooks are actually
// wired and the matrix `prompt` cell could be read as a live-capture promise
// on this host. Without wired hooks the gemini-config check already reports
// the gap, and an account probe would add nothing (and would still cost one
// headless run on eligible accounts).
func (c *RootCLI) inspectGeminiHostEligibility(ctx context.Context, hooksWired bool, projectDir string) doctorCheck {
	const checkName = "gemini-host-eligibility"
	if !hooksWired {
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusSkip,
			Message: localizef(
				"gemini host eligibility probe skipped: the project config did not pass gemini-config inspection, so prompt capture is already reported there",
				"gemini host の eligibility probe をスキップしました。project config が gemini-config 検査を通過していないため、prompt capture の欠如はそちらで報告済みです",
			),
		}
	}
	return buildGeminiEligibilityCheck(geminiEligibilityProbeFunc(ctx, projectDir))
}

func buildGeminiEligibilityCheck(result geminiEligibilityResult) doctorCheck {
	const checkName = "gemini-host-eligibility"
	switch result.Status {
	case geminiEligibilityEligible:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusPass,
			Message: localizef(
				"bounded `gemini -p` probe succeeded on this host: the account is eligible, so the wired `BeforeAgent` hook can record prompt events",
				"bounded な `gemini -p` probe がこのホストで成功しました。アカウントは eligible なため、配線済みの `BeforeAgent` hook が prompt event を記録できます",
			),
		}
	case geminiEligibilityIneligible:
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusWarn,
			Hint: Localize(
				"migrate to Antigravity (`traceary hooks install --client antigravity`) or switch to an eligible Gemini Code Assist / paid API plan, then re-run the isolated probe recipe in docs/integrations/gemini-extension.md; Traceary cannot change Google account eligibility",
				"Antigravity へ移行するか (`traceary hooks install --client antigravity`)、eligible な Gemini Code Assist / 有償 API プランへ切り替えた上で docs/integrations/gemini-extension.md の隔離 probe 手順を再実行してください。Google アカウントの eligibility は Traceary では変更できません",
			),
			Message: localizef(
				"gemini `-p` probe was rejected with IneligibleTierError: Gemini Code Assist for individuals is no longer served, so the host aborts the run after SessionStart and prompt/transcript events are absent even though the Traceary hooks are wired — the matrix `prompt` cell describes Traceary's wiring, not this account's eligibility",
				"gemini `-p` probe が IneligibleTierError で拒否されました。Gemini Code Assist for individuals は提供終了のため、host は SessionStart 直後に run を中断し、Traceary hook は配線済みでも prompt/transcript event は記録されません。matrix の `prompt` セルは Traceary 側の配線状態を表し、このアカウントの eligibility を保証するものではありません",
			),
		}
	default:
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "probe outcome was inconclusive"
		}
		return doctorCheck{
			Name:   checkName,
			Status: doctorStatusSkip,
			Message: localizef(
				"gemini host eligibility probe was inconclusive (skipped): %s; the matrix `prompt` cell describes Traceary's wiring and is not a per-account capture guarantee",
				"gemini host の eligibility probe は不明でした (skip): %s。matrix の `prompt` セルは Traceary 側の配線状態を表し、アカウント単位の capture を保証するものではありません",
				reason,
			),
		}
	}
}
