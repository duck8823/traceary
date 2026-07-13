package cli

// Antigravity capture level is a host-mode trait, not an install-route fact. A
// correctly wired hook install captures different lifecycle levels depending on
// how the host runs the agent. Route installation health is reported separately
// by the antigravity-hooks summary (and its per-route checks); this check never
// inspects install files. It exists so `doctor --client antigravity` does not
// imply full session transcript capture just because the hooks are installed.
//
// The capture-level vocabulary is fixed by the documented Antigravity hook
// contract, so this check is pure and deterministic (no filesystem probe):
//
//   - start_supported       — PreInvocation fires in every mode (session start/refresh).
//   - tool_audit_supported   — PreToolUse+PostToolUse (run_command) fire in every mode.
//   - final_turn_supported   — Stop supplies transcriptPath in interactive and
//     current headless CLI runs; actual persistence is checked from DB evidence.
const (
	antigravityCaptureLevelsCheck = "antigravity-capture-levels"

	// Capture-level tokens surfaced verbatim in the doctor message and the docs
	// capture matrix so status output and documentation stay in lock-step.
	antigravityCaptureStartSupported     = "start_supported"
	antigravityCaptureToolAuditSupported = "tool_audit_supported"
	antigravityCaptureFinalTurnSupported = "final_turn_supported"
)

// buildAntigravityCaptureLevelsCheck returns the additive antigravity-capture-levels
// doctor check. It is always PASS: a working install legitimately captures the
// configured hook surface. Actual transcript delivery is judged separately by
// antigravity-event-coverage so a healthy config cannot hide a runtime gap.
func buildAntigravityCaptureLevelsCheck() doctorCheck {
	return doctorCheck{
		Name:   antigravityCaptureLevelsCheck,
		Status: doctorStatusPass,
		Message: localizef(
			"Antigravity capture levels are a host-mode trait, separate from hook install health (route installation is checked by `%s`). "+
				"Every mode captures `%s` (PreInvocation → session start/refresh) and `%s` (PreToolUse+PostToolUse run_command → command audit). "+
				"Current Antigravity hooks expose `%s` in interactive and headless `agy --print` runs (Stop → transcript-derived prompt + transcript event + turn boundary). "+
				"Runtime delivery is verified separately by `antigravity-event-coverage`; a healthy hook install alone does not prove transcript persistence.",
			"Antigravity の記録レベルは実行モードの性質であり、hook 導入経路の健全性とは別です（導入経路は `%s` が検査します）。"+
				"すべてのモードで `%s`（PreInvocation → セッション開始/更新）と `%s`（PreToolUse+PostToolUse run_command → コマンド監査）を記録します。"+
				"現在の Antigravity hooks は interactive と headless `agy --print` の両方で `%s` を提供します（Stop → transcript 由来 prompt + transcript event + turn 境界）。"+
				"実行時の配送は `antigravity-event-coverage` が別途検証します。hook 導入が健全なだけでは transcript 永続化の証明にはなりません。",
			antigravityRouteSummaryCheck,
			antigravityCaptureStartSupported,
			antigravityCaptureToolAuditSupported,
			antigravityCaptureFinalTurnSupported,
		),
	}
}
