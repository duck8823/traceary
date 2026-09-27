# ライフサイクル互換性監査 — 2026-09-27

[English](lifecycle-audit.md)

これは公式文書に基づく監査であり、実機取得の証明ではありません。過去の observed flags と live fixtures は変更しません。Stop は turn 終了であり session 終了ではありません。Interrupt / StopFailure は固定 note のみを記録し、session を閉じません。raw error と途中の assistant message は spool 保存前に破棄します。permission / telemetry / task / heartbeat / model-switch の購読は追加しません。

| Host | 最新の参照 | 採否 |
|---|---|---|
| Codex | CLI 0.157.1、2026-09-26。[hooks](https://developers.openai.com/codex/hooks) | SessionEnd / Interrupt を採用。host timeout 3秒、context deadline 2.5秒。session_end 固定 note のみを記録し、論理 session を閉じない。cleanup・refinement・extraction・decay・GC・spool backlog drain を行わない。normal close / idle shutdown 後も native thread は resume できるため、明示 CLI end / GC が終端を所有する。identity 欠落は無処理。one-shot wrapper の終了所有権を維持。Stop は turn 境界。 |
| Claude | CLI 2.1.283。[hooks](https://code.claude.com/docs/en/hooks)、[changelog](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md) | StopFailure（2.1.78 導入）を受動 note として採用。error_details / last_assistant_message は保存しない。PostModelSwitch（2.1.251）は model telemetry のため保留。既存の子 session 処理を維持。 |
| Kimi | CLI 2.1.0、2026-09-23。[hooks](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/hooks.html) | 正規化した Interrupt を採用。base session fields を使用し、reason は破棄、turn_id は必須にしない。旧 host 用 Stop を維持。互換性の例外として既存の host timeout 10秒を維持し、internal context は2.5秒とする。厳密な host limit 3秒は新 Codex / Claude passive 購読に適用する。context 非対応 I/O が host budget を消費する可能性があり、wall-clock の保証はない。合成 SessionEnd、StopFailure、heartbeat、task、queued-prompt は追加しない。 |
| Antigravity | CLI 1.2.11、2026-09-25。[hooks](https://antigravity.google/docs/hooks)、[changelog](https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md) | fullyIdle=false guard を採用。prompt / transcript の best effort capture は維持し、continuation・consolidation・完了 turn extraction・usage boundary は抑止。true / 欠落は互換動作。存在する null / 文字列 / 数値は不明値として idle action を抑止。false→true が同じ executionNum を共有しうるため idempotency は保留。SDK compaction を JSON hook として追加しない。 |
| Gemini | Stable 0.61.0、2026-09-23。[changelogs](https://geminicli.com/docs/changelogs/)、[reference](https://geminicli.com/docs/hooks/reference/) | イベント拡張なし。SessionEnd は host が待たない best effort。PreCompress は advisory marker のみ、post-compress summary ではない。AfterAgent は turn 境界。 |
| Grok | Released 1.0.40、2026-09-20（[changelog](https://x.ai/build/changelog)）。main source 1.0.41 の release 日は未確認。[hooks](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/10-hooks.md)、[DTO](https://raw.githubusercontent.com/xai-org/grok-build/main/crates/codegen/xai-hooks-plugins-types/src/lib.rs) | 購読追加なし。1.0.18 から start hook は background 実行されるため、prompt-before-start の identity 保持が必要。main source の StopCancelled / Failure は release / live dispatch が未確認のため保留。turn / process 終了から session 終了を推測しない。 |
| Muse | Published manual 1.2.1、next SDK 1.3.0。[changelog](https://dev.meta.ai/docs/muse-code/changelog)、[next SDK hooks](https://meta-models.github.io/muse-code-sdk/next/guides/extend/hooks/) | イベント拡張なし。SDK の hook / last_assistant_message / turn_id は released CLI の transcript support の証明ではない。1.0.3 の live evidence と未対応・未観測を維持。 |

## 再配送、cleanup、rollback

Codex SessionEnd は物理 runtime の閉鎖観測であり、再開可能な論理 thread の不可逆な終了境界ではありません。session aggregate と global fallback state は変更しません。native payload identity を優先し、Interrupt は active child ではなく main session に帰属します。文書化済みの native delivery ID がある場合は保持しますが、body hash の identity は作りません。論理 session の閉鎖・再開は別 design checkpoint の [follow-up #2394](https://github.com/duck8823/traceary/issues/2394) で扱います。既存の明示 terminal cleanup / maintenance は変更せず、atomic cleanup の保証を追加しません。新 passive adapter は spool 永続化前の入力取得時に note 帰属を確定します。native session ID が存在する場合のみ明示 one-shot wrapper identity を使用し、replay 時の wrapper 環境に関係なく取得時の identity を保持します。native identity 欠落・空白は無処理です。この保証は新 passive adapter のみで、既存の全 spool command に適用される保証ではありません。

前提修正: [#2395](https://github.com/duck8823/traceary/issues/2395) は [PR #2396](https://github.com/duck8823/traceary/pull/2396) で統合済みです。wall-clock budget 計測を parallel test load から分離し、attestation deadline を fixture 構築後に開始します。production retry 動作と時間閾値は変更していません。別 Issue / PR のため本変更には同梱せず、実時間の安全性は fresh CI で引き続き検証します。

rollback は単一 Issue PR の revert。DB migration と installed host configuration の変更はありません。既に記録した固定 note は履歴として残ります。

## Host と Traceary のバージョン要件

新購読には現在の公式 lifecycle contract に対応する host が必要です。hook refresh 前に host の対応を確認し、必要なら更新してください。過去の Codex 0.145.0 live fixture は SessionEnd / Interrupt 対応の保証ではありません。本監査は導入最低バージョンや実機検証済みを保証しません。新 passive CLI adapter は現 HEAD build / 次の Traceary release の機能であり、既存 installed 0.52.0 binary の機能ではありません。この変更は installed binary と host settings を更新しません。
