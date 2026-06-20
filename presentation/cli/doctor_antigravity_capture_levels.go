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
//   - final_turn_supported   — Stop (transcript + turn boundary) fires only on
//     interactive runs.
//   - final_turn_unavailable — headless `agy --print` emits no Stop/finalization
//     hook, so no transcript event or turn boundary is recorded for that run.
const (
	antigravityCaptureLevelsCheck = "antigravity-capture-levels"

	// Capture-level tokens surfaced verbatim in the doctor message and the docs
	// capture matrix so status output and documentation stay in lock-step.
	antigravityCaptureStartSupported       = "start_supported"
	antigravityCaptureToolAuditSupported   = "tool_audit_supported"
	antigravityCaptureFinalTurnSupported   = "final_turn_supported"
	antigravityCaptureFinalTurnUnavailable = "final_turn_unavailable"
)

// buildAntigravityCaptureLevelsCheck returns the additive antigravity-capture-levels
// doctor check. It is always PASS: a working install legitimately captures the
// start and tool-audit levels in every mode, and the absence of a Stop hook in
// headless `agy --print` is a host-mode trait, not a Traceary install failure, so
// it must not warn. The message reports the capture-level vocabulary and makes the
// print-mode `final_turn_unavailable` reality explicit.
func buildAntigravityCaptureLevelsCheck() doctorCheck {
	return doctorCheck{
		Name:   antigravityCaptureLevelsCheck,
		Status: doctorStatusPass,
		Message: localizef(
			"Antigravity capture levels are a host-mode trait, separate from hook install health (route installation is checked by `%s`). "+
				"Every mode captures `%s` (PreInvocation → session start/refresh) and `%s` (PreToolUse+PostToolUse run_command → command audit). "+
				"`%s` (Stop → transcript event + turn boundary) is captured only on interactive runs; headless `agy --print` is `%s` because the host emits no Stop or other finalization hook in print mode, so a print run records no transcript event and no turn boundary. "+
				"A healthy hook install therefore does NOT imply full session transcript capture in print mode — this is the expected print-mode capture level, not an install failure.",
			"Antigravity の記録レベルは実行モードの性質であり、hook 導入経路の健全性とは別です（導入経路は `%s` が検査します）。"+
				"すべてのモードで `%s`（PreInvocation → セッション開始/更新）と `%s`（PreToolUse+PostToolUse run_command → コマンド監査）を記録します。"+
				"`%s`（Stop → transcript event + turn 境界）は対話実行でのみ記録され、headless `agy --print` ではホストが print mode で Stop その他の finalization hook を発行しないため `%s` となり、その実行では transcript event も turn 境界も記録されません。"+
				"したがって hook 導入が健全でも、print mode でセッション transcript を完全に記録することは保証されません。これは print mode における想定どおりの記録レベルであり、導入失敗ではありません。",
			antigravityRouteSummaryCheck,
			antigravityCaptureStartSupported,
			antigravityCaptureToolAuditSupported,
			antigravityCaptureFinalTurnSupported,
			antigravityCaptureFinalTurnUnavailable,
		),
	}
}
