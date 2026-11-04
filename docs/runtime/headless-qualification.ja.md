# 統合 headless daemon 認定

[English](headless-qualification.md)

**未完了です。** [headless 認定 Issue](https://github.com/duck8823/traceary/issues/2470) を追跡します。release や導入済み AI の認定ではありません。
UI/framework の採否は別です。
実機 sleep/wake はユーザーが後回しにした未実施項目であり、成功ではありません。
Muse native 作業も保留のままです。

## 統合した合成 process fixture

`TestRuntimeCLIEndToEndRegistrationLaunchAndListWithoutHomeOrCoreBootstrap` は、実際の公開 CLI subprocess、隔離した実 daemon、その daemon が直接所有する合成 PTY 実行ファイルを再利用します。
HOME、credential、導入済み model は使いません。
既存 core-store の不透明なファイルは byte 単位で不変とし、poison core path は初期化してはいけません。

`integrated` scenario は observation admission を **unbound** と確認し、生存中の出力を読み取ります。
read-only attach client だけを detach して実際に Wait し、同じ管理対象 generation が生存していること、foreign generation の attach が拒否されることを確認します。
続いて、**手動入力の前に** 正確な byte cursor から再接続します。
明示した byte の入力、重複 request の receipt 確認、resize、child 内の marker と geometry の照合、work loss を明示確認した停止を行います。
daemon による実 child 終了の反映と、再接続 stream の終了・既読 byte の再送防止を検証します。
停止 signal の delivered receipt だけで終了と判定しません。

正シナリオの workload は事前に固定します。
child 1件、実際の decode 後に fixture private file の consumed cursor で確認する64KiB burstの `x` 出力が正確に2MiB、raw input が5byte、`r` marker が1byte、同じ request の重複 receipt 確認が1回、resize は31x101です。
既存の30秒 context は失敗の上限であり、AI latency や性能 SLA ではありません。
各 live frame の selection と連続した正規 decimal cursor を照合します。
raw 出力を状態・承認として解釈しません。

別の `replay-oracle` scenario では、同じ5byteを **新しい** 明示的な合成 request ID で意図して送ります。
child は次の byte が literal `r` であることを必須にします。
予期しない再送は0xe6で始まるため、geometry を記録する前に97で終了します。
これにより、重複 request が入力を再送した場合に正シナリオが検出できることを確認します。
異なる手動 request の重複排除を製品要件にはしません。
既存 output/control/observe scenario の速度制御しない2MiB burstも回帰検証に残します。
当初の速度制御しない integrated race では実際に slow-consumer gap が発生し、この失敗証拠を保持しています。
private file の同期は正の合成 workload だけを決定化し、native backpressure や resource 性能の証明にはしません。
gap を成功として許容しません。

これは合成実行ファイルを使う実 process/CLI 統合検証です。
導入済み AI の hook、bound native observation、native 現在状態、最終 model/provider、中断後の復旧、sleep/wake、system resource 閾値の認定ではありません。

## 最終統合 head で残る gate

- 実 native の起動・出力・手動入力・detach/reconnect、認定済み bound observation と失効、実終了。未対応 host は unsupported/unknown を明示します。
- foreign/stale run、PID 再利用、daemon 不在/crash、partial input、slow consumer、overflow/cursor gap、maintenance、shutdown cancel、update defer の fault matrix。
- 実機 sleep/wake はユーザーが後回しにした NOT_RUN。timer から成功を推定しません。
- native workload・機材・閾値の事前固定と、総 CPU/RSS/process tree、metadata/output の有界性の実測。
- daemon なしでの既存 CLI/hooks/redaction/spool/history/memory 互換性。
- 元の target、新 generation、未確認 input/tool/approval の再送なしを満たす実 native 中断・再開。旧 owner の生存/不明、同時再開、target 変更、履歴消失、復旧中 crash の拒否。
- 独立 High review と、同じ統合 SHA に対する final-head build/full CI/lint/security、承認された wave E2E。

合成 test の成功をこれらの代わりにはしません。
この fixture は runtime/auth/default/config/schema の変更、wrapper、自動 adoption/kill/resume、UI 採用、配布を許可しません。
rollback は新しい test scenario/helper と本文書だけを削除します。
親 Issue は実際の tagged release まで開いたままです。

## selection 不一致の入力拒否

各 control scenario で、構文上は有効ですが epoch・run・generation・worktree・profile のいずれか1つが一致しない target に、明示的な5byteの合成入力を送ります。
公開 CLI は失敗し、payload を表示せず、rejected・written0 の安全な receipt を返す必要があります。
正しい selection で同じ request ID を照会しても not-found を維持し、元 child の input artifact が存在しないことを確認します。
続く正確な正入力と literal marker の oracle によって、遅延した意図しない書込みも検出します。
拒否された request は再送しません。
unknown・不一致 selector の検証であり、既存 run の同時実行や native 所有権の残りの matrix の代替にはしません。
runtime の振る舞い・上限は変更しません。
