# Native 観測配送の設計チェックポイント

[English](native-observation-delivery.md)

状態は **提案であり、未採用、未実装** です。
[観測配送の leaf](https://github.com/duck8823/traceary/issues/2466) の実装前設計資料であり、新しい CLI command の利用案内や native host の認定ではありません。
[採用済み headless R0](../adr/2026-11-03-headless-runtime-r0.ja.md) を正本とします。
production 実装前に、独立した High/security review と主 session の採否判断で未確定の contract を解決する必要があります。

## 要求と既存の境界

既存 native hook の capture は daemon がなくても動作する必要があります。
任意の live 配送は導出、redaction 済み metadata のみを運び、prompt、code、audit、memory の本文、credential、terminal input、承認判断、停止要求、再開指示を運んではいけません。
queue 受付の成功は、native 出所の認証や入力可能状態の証明ではありません。

現在の `application/runtime/daemon.go` にある `ObservationMetadata` と `Daemon.Observe` は未信頼 metadata の transport です。
既存の owner-private Unix transport と launch identity は再利用できる境界ですが、native hook と run の対応を証明しません。
`infrastructure/runtime/pty_unix.go` の `launchEnvironment` は外部から継承した runtime 変数を除去して run/generation/epoch の hint を設定しますが、それらの一致、cwd や native session ID だけで観測を bound にしてはいけません。

[従来の観測 ADR](../adr/2026-10-18-desktop-runtime-observations.ja.md) は、descriptor path を native hook の invocation ごとの引数で渡す案を示しています。
すべての host での対応や、host 設定方式の採用を証明したものではありません。
host/version ごとに一意な伝播と非破壊な hook 合成を実証する必要があります。
descriptor path は対応付けの情報であり、入力や process 制御の権限を与えません。
伝播を検証できなければ観測は unbound、native interaction は unknown のままとし、wrapper や cwd/PID による代替は認めません。

## 概念モデルと責務

| 概念 | 所有者 | 不変条件 |
| --- | --- | --- |
| 導出した観測 | domain value | 閉じた有界 metadata とし、本文や効果を持つ arm を含めず、native 出所の主張は未信頼とする |
| managed binding descriptor | runtime metadata adapter と不変の domain binding | store/worktree/profile host/version/run/generation/epoch と物理 witness を照合し、保存 PID に権限を与えない |
| native hook context | 既存 host CLI adapter | native payload は一度だけ読み、元の capture、receipt/spool、stdout/exit 判断を保持する |
| 任意配送 | application port | 全体を一つの単調時計 deadline で制限し、処理中の数を制限し、自動 replay や主結果の変更をしない |
| local transport | 既存 daemon adapter | 観測 endpoint のみを使い、daemon 自動起動や管理操作をしない |
| native の因果証跡 | version ごとの adapter と reducer | descriptor の対応付けだけでは native 状態や承認表示を確定しない |

identity と上限は domain が所有し、JSON、file、socket の詳細を持ち込みません。
application は任意配送の調停を担当し、host 判断の整形や SQL への直接 access を行いません。
infrastructure は descriptor の no-follow read、root lock、atomic publication と wire deadline を担当します。
主 capture と host の stdout/exit 整形は既存 CLI hook の責務です。
既存の対象単位 service に capability を加える設計とし、hook ごとの新しい class や二つ目の target Registry は作りません。

## 配送順序の提案

既存 hook handler は payload を一度だけ読み、主 capture と判断結果を保持します。
任意の経路が allowlist 済み metadata を導出し、明示された、対応を検証済みの native invocation context を解決します。
その後、将来の明示的な metadata intake と同じ有界 application port を使います。
生の native payload は転送せず、stdin の二重 reader も作りません。

提案する **全体 250 ms の単調時計 deadline** は、任意処理の descriptor 検証前に開始します。
root/file 検証、connect、handshake、frame write と acknowledgement のすべてを含めます。
phase ごとに 250 ms を与える実装は全体上限を超えるため採用しません。
上限内に終えられなければ unavailable/unknown の telemetry とし、無制限の background worker を残したり、元の capture 結果を変えたりしません。
cancellation と実際に残る処理中 worker も上限設計に含め、cancel を無視する callback によって代替 worker を無制限に増やしてはいけません。

daemon 不在、危険な socket、過大 frame、queue 圧迫、切断、timeout でも、元の event ID、inserted/redelivery、hook stdout、exit code と既存 receipt/spool の所有権を保持します。
live 配送のための独自 spool や drain worker は追加しません。
観測 ACK は受付だけを示し、event の commit、native 配送、承認、process 状態や権限の証明ではありません。

## 未確定の contract

1. **public intake 名と schema**：閉じた version 付き metadata-only schema を、flag の案内や public command の実装より前に決めます。この文書には実行可能な CLI 保証となる例を載せません。
2. **descriptor 表現**：run ごとの不変 private file と、完全な launch selection を key とする有界 envelope を比較します。所有者による publication、no-follow race 検証、最大サイズ、合計 budget と generation 廃止を実装前に決めます。環境変数を暗黙の同等品として扱いません。
3. **native の一意な対応**：独立に検証した invocation ごとの伝播と、既存 launch への正確な binding を要求します。native session ID は version ごとの一意な対応が必要で、最初に見つかった候補や同じ cwd では不十分です。曖昧、他対象、未対応、古い context を bound/current にしてはいけません。
4. **metadata の損失と保持**：新しい descriptor format は、実際の型付き audit/export/forget と合計 budget の contract に統合します。どの復旧用 link を消し、どの監査、policy を保持するかを決めます。各部分 write の確定済み prefix を実際の launch/lifecycle reader が読める必要があり、通常の自製 root を恒久的に unsupported にしてはいけません。未知の version は引き続き拒否します。
5. **hook 合成**：無関係な global/per-run handler、host の判断優先順位と一回の論理 capture を保持します。global 設定の install/上書き、auth/trust/model/PATH 変更や wrapper 導入は行いません。合成 fixture は、実 host/version での動作を証明しません。

これらの決定は関連します。
descriptor file だけを選び、寿命、監査、削除 contract を後回しにすることは、必須の安全性を解決したことになりません。
この文書では表現を採用しません。

## 振る舞いテストと検証計画

| 条件と操作 | 必須の観測結果 |
| --- | --- |
| 対応済みで一意な invocation descriptor と生成した private Unix server | 一つの有界な導出 frame のみ。元の capture ID/件数/stdout/exit は不変で、ACK は受付だけを示す |
| 各 phase で daemon 不在、低速、危険、backpressure | 一つの全体 deadline。主処理の失敗、二つ目の drainer、自動 replay、無制限の worker 増加を起こさない |
| env/native-ID/cwd だけの主張、race/future/他所有者の descriptor、誤った store/host/version/generation/epoch、曖昧な対応 | 任意 telemetry を unbound/拒否とし、native 権限や主処理結果を変えない |
| 重複、順序逆転、旧 generation から遅延した callback | 現 generation に昇格せず、因果検証を version ごとの独立した境界に残す |
| global/per-run hook 合成 fixture | 既存 handler と判断優先順位を保持し、論理 capture を二重化しない。未検証の native 合成は unknown のまま |
| descriptor publication/廃止/forget の各 write 境界で失敗 | 実 metadata reader が確定済みの各 prefix を読め、損失/保持と不確定性を明示し、自動 retry しない |

RED/GREEN の観測可能な test と CLI 互換性 golden から始めます。
経過時間、deadline、queue は実際に生成した private socket で検証し、fixture を通すために production deadline を延ばしません。
影響する staged/reverse test、並行処理の race test、build/vet/lint、英日文書検証、新しい同一 head の CI/security を実施します。
最終独立実装レビューと daemon 統合 wave も必要です。
合成 fixture で実 native hook、実機 sleep/wake や同一会話の再開を認定してはいけません。

## rollback と scope

任意の live 配送だけを止めます。
native hook の capture、判断、既存 spool replay、core 履歴、Registry と native 会話は保持します。
暗黙の input、承認、replay、resume や新しい会話への fallback は導入しません。
認証と課金は実 host CLI が担当します。
UI/framework/distribution はこの leaf の対象外とし、Muse native 作業は保留します。
この提案は leaf の完了や、設計 checkpoint 前の production 変更を許可するものではありません。

## 実装済み履歴との整合

[履歴 leaf](history-reconciliation.ja.md) は、有界な metadata audit/preview/forget を含めて `main` にマージ済みです。この提案では実際の実装を拡張し、inventory 互換性を先送りしたり、自ら生成した descriptor を非対応 metadata と扱ったりしてはいけません。履歴 leaf の完了は、lifecycle の実機・native hook・session 復旧の認定ではありません。

受入済み R0 は追加 command 名を `traceary runtime observe` と定めています。未決定なのは flags と closed な versioned metadata-only schema であり、command 名を再選定するものではありません。実装前 design review が完了するまで、この文書は実行可能な flags や配布済み command を保証しません。

## Descriptor 表現のレビュー候補

既存の owner-private な `execution.json` の完全な immutable launch snapshot を descriptor 本体として再利用し、明示的な full launch selection から解決する案も比較します。これは未採用の候補です。新しい per-run file や envelope を増やさずに済む可能性がありますが、既存 snapshot が store/worktree/profile host/version と run/generation/epoch の全 witness を保持していること、同じ有界 deadline 内で安全に読み取れることを実 source とテストで確認する必要があります。descriptor の参照や一致だけで native binding を認定することはありません。native hook の per-invocation propagation・因果性・version ごとの証拠は別に必要です。既存 schema の意味を変更する場合は、同じ typed metadata audit/forget/budget と有効な書込 prefix の契約も更新します。

## 独立実装前レビューの結果

固定 tree `7a94503a` の独立 High/security レビューは **CHANGES_REQUIRED** です。実装や native 認定の承認ではありません。descriptor の比較対象となる実ファイル名は `execution.json`（`executionEnvelope.launches`）です。新しい per-run file を追加する案は採用せず、既存の完全な launch snapshot と明示的な full selection の再利用を具体化します。新ファイル・新 schema を追加しない限り inventory の拡張は不要ですが、既存 metadata audit/forget と有効な書込 prefix が変化しないことは実テストで確認します。

実装前の残件は、wire 上の worktree/profile/full selection の一意な表現、production の有界な observation receiver と後続 reducer の責務境界、closed な event kind、primary hook budget 内の配送位置の確定です。現在の `ObservationMetadata.TargetID` には domain 上の対応を推測せず、旧 daemon が新 schema を明示拒否する互換性も定めます。descriptor と一致しても native binding・入力許可・承認済み状態には昇格させません。レビュー推奨の新規 ring buffer は自動採用せず、後続 projection と重複する状態所有者を作らない設計を確認します。

optional 配送は primary capture の完了後に限定し、既存 packaged hook の残り budget と spool drain の予約時間を侵食する場合は省略します。stdout/exit と receipt/spool の意味は変更しません。正確な command flags・wire schema と receiver 境界の修正版は再度 checkpoint で検証し、それまでは production code を追加しません。host ごとの native hook 引数伝播・合成の認定は各 adapter leaf に残し、ここで universal な対応を宣言しません。
