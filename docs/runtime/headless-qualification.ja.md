# 統合 headless daemon 認定

[English](headless-qualification.md)

**実装 checkpoint と native 認定を分離しています。** [実装 QA](https://github.com/duck8823/traceary/issues/2470) と [未完了の installed-AI 認定](https://github.com/duck8823/traceary/issues/2498) は別です。release や native 認定ではありません。
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

## 別途追跡する native/全体認定の残り gate

- 実 native の起動・出力・手動入力・detach/reconnect、認定済み bound observation と失効、実終了。未対応 host は unsupported/unknown を明示します。
- foreign/stale run、PID 再利用、daemon 不在/crash、partial input、slow consumer、overflow/cursor gap、maintenance、shutdown cancel、update defer の fault matrix。
- 実機 sleep/wake はユーザーが後回しにした NOT_RUN。timer から成功を推定しません。
- native workload・機材・process tree と metadata/output の有界性の証拠。CPU/RSS は明示延期で、実測合格ではありません。再開時に workload・機材・閾値を事前固定します。
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

## lifecycle 依存を含む統合 source

元の lifecycle slice98536b18c53130fd80604b04cc99c50fe9dd2747に、レビュー済みの Codex/AGY 実装と unsupported-platform guard を統合しています。QA 自体は runtime 権限を追加しません。host dispatch と actual Wait 後 cleanup の union は別途レビュー・検証しました。
単一の process fixture で、output・controls・observe・integrated・replay-oracle・shutdown・crash・update・autostart の9 scenario を保持します。
integrated の live run では、reconnect 後・手動入力前に、update attempt 1回の延期と notice、正確な単一 run・pending0の shutdown preview 1回の cancel を確認します。
shutdown の confirm は行わず、続く正確な入力・再送防止・resize・Stop まで同じ child/generation が live であることを確認します。
metadata の延期・preview 取消であり、更新の install、不可逆 shutdown の巻戻し、native 状態の権限ではありません。

当初は review を隔離するため QA PR を lifecycle feature branch に向けていましたが、lifecycle 実装は main に merge 済みで、QA の base も明示的に main へ戻しました。
**QA を feature 親へ merge してはいけません。**
2026-11-05、保守者は安全な実装と未完了の実機/native認定の分離を明示承認しました。
実装完了にも source 同一性、独立 High/security review、fresh full CI/lint、固定した統合入力での実装限定 wave が必要です。
物理/native 条件は後続認定で未完了を保持し、failed/unknown/NOT_RUN を合格にせず、未認定権限を有効化しません。
元の Daemon 全体 goal は後続認定を満たすまで未完了です。

## 同一targetで同時稼働する合成child

追加した `concurrent` scenario では、同じ canonical worktree・core-store target・profile で、2件の合成childを同時にliveにします。
run と generation のidentityは別でなければなりません。
一方の実runと他方の実generationを組み合わせた入力は、zero-writtenで拒否し、両childの入力artifactを作らず、両方の実journalでtyped not-foundになることを確認します。

各childには、**同じ**request IDで、それぞれ異なる5byteを送ります。
完全一致の重複は各自のreceiptを読むだけで再送せず、literal markerと異なるresize geometryによって入力の混線・重複を検出します。
run別artifactで元のcanonical cwd/storeとrun identityも照合します。
一方へのACK付きStop後、実終了をreconcileする間、他方は同じidentityでliveを維持し、別の明示Stopでのみ終了します。
両方の終了はsignalによるもので、有限fixtureの期限切れではないことを確認します。
両終了のreconcile後にdaemonの実Waitを取得し、core bytes不変とbootstrapしない条件も維持します。

後述のretained-cursor scenarioを含め、合成fixtureは計11modeです。
これは実AIの同時稼働、native会話IDの再利用、native generation binding、中断復旧、resource閾値の認定ではありません。
既存の最終gateとparent先行のdelivery順序は維持します。

## Daemonの実終了後の拒否

integrated childと元Daemonの実終了後、有効な旧selectionでattach・input・resize・Stopを各1回要求します。
合成入力をechoせず、bounded runtime metadataを変更せず、元childを再起動せず、core storeを初期化せずに失敗することを確認します。
Daemonの実Wait後にはowner socketが既に存在せず、file modeと内容hashが変わらないことでも暗黙owner作成を検出します。

任意のobservationは既存のbest-effort CLI契約を維持し、exit0で正確に`unavailable`・`unbound`・`admission_only`を返します。
queuedや現在状態の証明ではありません。
この場合もruntime/core/native artifactを変更しません。
最初のテストはobservationにもnonzeroを要求して失敗したため、そのoracle失敗を保持し、既存の文書化された実装契約に合わせて修正しました。
製品の修正とは扱わず、native・global排他・障害途中の認定にも読み替えません。

## Public CLIでのretained historyの正確なeviction

`cursor-gap`だけが固定2MiB+32KiBの`x`を出力し、既存modeのworkloadは維持します。
既定の保持2MiB・chunk32KiBは起動前に照合し、黙って変更しません。
childの実exit7後、cursor0のattachでcapture終了・base32768/head2129920を確認し、delivery_unknown=falseの既知`evicted` gap[0,32768)が正確に1件続くことを要求します。
各frameのselectionを維持し、保持された全byteをcanonicalかつ連続したcursorで正確なheadまで読み、end marker・helper trailer・読み取り専用clientの実Wait0を確認します。

32768からのattachはgapなしで保持2MiBを再読し、2129920からの再接続はdataを再送しません。
元core/target不変・bootstrapしない条件・Daemonの実Waitも維持します。
最初は既存2MiB producerでeviction oracleが失敗し、新しい合成workloadだけを増やして必要な圧力を加えました。
このREDはfixtureの不足として保持し、runtimeの欠陥とは扱いません。
history evictionをslow-live-consumer、unknown tail、実AI、resource性能の認定には読み替えません。

## 保守者決定: CPU/RSS認定を延期（2026-11-05）

保守者はCPU/RSS計測を明示的に延期し、応答性は後で体感確認する方針とした。このためCPU/RSS計測は今回のheadless deliveryの停止条件にしない。これは延期であり、リソース予算の実測合格ではない。Stage Aは合成校正のみで、native process familyのCPU/RSS網羅性も未証明のまま。sampling方式や数値予算を新たに採用したものではない。正確なbyte/ack、metadata/outputの上限、process所有と実終了の証拠は維持する。他のnative、sleep/wake、security、review、wave gateは変更しない。

## 実装 fault 証拠の layer

| Fault | 証拠。native 認定ではない |
| --- | --- |
| foreign/stale selector、同時 run、重複 input receipt | 直接所有する合成 child と公開 CLI。厳密な zero-effect/no-replay |
| daemon 不在/crash、cancel/update defer、cursor eviction | 実 daemon/CLI、正確な byte/cursor/receipt/actual Wait。有限の合成 producer |
| partial input、closed-handle 再利用 | 注入 syscall/write 境界と無関係の実 pipe。OS PID 再利用や native input 中断ではない |
| slow subscriber、output overflow、PID 再利用 | application/fake-child model。明示 unknown gap、独立正常 reader、epoch 拒否 |
| queue pressure、causal loss | 実 metadata lock/socket queue と注入 worker の失効。stale stamp を付け直さない |
| maintenance、committed history | 隔離した実 SQLite transaction/lease。排他中 Busy、解放後読取 |
| 実機 sleep、native callback/currentness/recovery | NOT_RUN/未認定、または保持済みの実測範囲のみ。上の行から推定しない |

実装限定 wave は実際の合成 process test と、隔離した合成 daemon-free record/search/refine/memory を使います。
host-record SKIP は明示したままです。
plugin install、trust 回答、導入済み AI 起動や native 互換性/wave 認定の代替は行いません。
CPU/RSS は延期のままで、UI・配布・host 設定・auth/model/provider・自動再送を許可しません。
