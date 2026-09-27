# ADR: 再開可能な論理セッションとホスト終了の観測

[English](./2026-09-27-resumable-session-boundaries.md)

- Status: Proposed
- Date: 2026-09-27
- Decision owner: Traceary メンテナー（人間による承認が必要）
- Reviewers: アーキテクチャ、ライフサイクル、hook 配信、ストレージと bundle、独立検証の担当
- Related issue: [#2394](https://github.com/duck8823/traceary/issues/2394)
- Checkpoint: 設計議論のみ。この ADR は実装も設計の採用も許可しない。

## 要求の要約

ホストが会話を表示しなくなっても、Traceary が記録する論理的な作業は終了したとは限らない。
論理的な終了とホストの終了を区別し、終端状態、所有権、再試行、委譲の保証を維持する必要がある。
現在の推奨は C とする。
単調な論理 `Session` を維持し、ホスト終了を受動的な観測として記録し、ランタイムの利用可能性は不明と表示する。
設計上は論理会話とランタイムを分離し、終端した集約を再オープンしない。
B は、この分離を将来実現する条件付きの案であり、実装承認でも、この設計を将来承認するための必須条件でもない。
C は #2393 の受動観測 baseline と将来の read 契約案を組み合わせるものであり、全ホストの SessionEnd を受動化する案ではない。
現在の interactive Claude、Gemini、Kimi の `SessionEnd` は集約の `End` を呼び、この mapping は変更しない。
特定済みの受動 mapping は Codex `SessionEnd`/`Interrupt`、Claude `StopFailure`、Kimi `Interrupt` であり、全ホスト共通の受動 close mapping はない。
この区別は再開可能な host-close callback として特定した対象に適用し、将来の再分類には根拠とレビューが必要である。
共通の単調な不変条件は、同一の hook mapping を意味しない。
この設計 PR は runtime code、schema、履歴データ、既存の `active` 契約を変更しない。
#2394 は open のまま維持し、draft design PR は closing reference ではなく `Refs #2394` を使う。

## 背景と根拠

2026-09-27 に確認した [Codex の公式 hook 契約](https://developers.openai.com/codex/hooks)では、main thread の `SessionEnd` は通常終了、開いている会話の archive/delete、接続クライアントがない状態での 30 分の idle に伴って発生し、subagent には発生しない。
現在の reason は `other` である。
`SessionStart` の source には `startup`、`resume`、`clear`、`compact` があり、compaction 後も同じ turn が継続し得る。
これらは別の runtime instance の存在を証明しない。
これは文書の根拠であり、認証済みホストの実動作を検証した結果ではない。

base `a61b0a18b57345f90009ac449a2be3210c8e0910` のリポジトリ上の根拠は次のとおり。

- `domain/model/session_lifecycle.go` は最初の終端遷移を維持する。
- `application/usecase/session_usecase_impl.go` の `Active` は start Event を返し、`End` は子孫を終了させる。
- `infrastructure/sqlite/sql/find_active_session.sql` の従来の activity 選択には、終了後に event がある session も含まれる。
- hook 配信の semantic fingerprint は runtime generation の識別根拠にはならない。
- bundle import は store より新しい `manifest.BundleSchemaVersion` を未知 table の検査前に拒否するため、table の追加だけでは後方互換にならない。

[#2393](https://github.com/duck8823/traceary/issues/2393) の受動 adapter は取得済み SID、絶対 database route、raw cwd を固定するが、解決済み Workspace 全体は固定しない。
`TRACEARY_WORKSPACE` と repository detection は replay 時に Workspace を再解決し得る。
完全に不変な Workspace binding は目標であり既知の gap であって、達成済み baseline ではない。
すべての legacy routing が修正済みとも判断できない。

interactive hook の論理 ID は native `session_id` と一致するが、one-shot wrapper 内は例外である。
既存 ID への `SessionStart` は冪等に成功して hook state を書き、後の同一 ID の event は終端済みレコードへの late event として付く。
新しい continuation や再オープンにはならない。
C は当面この動作を維持し、後続 event を抑制または破棄せず、従来の `active` は `ended_with_late_events` を含み得る。
終端後に記録された受動 close note 自体も、この activity query の条件を満たし得る。

`session_start`/`session_end` の配信 fallback は `session_id` を使うため、繰り返し start は同一 retry にまとめられ得る。
start source は instance identity ではない。
native event ID のない受動 close receipt は異なる方法で保持され、この非対称性から runtime 順序は分からない。
host の再配信の曖昧さと、commit 後の spool clear 失敗による Traceary local replay は区別する。
将来の local receipt ID と取得時の `received_at` は local replay の重複を除去できるが、host episode や因果順序を証明しない。
現在の spool `CreatedAt` は保存 event の `recorded_at` と同じではなく、どちらも runtime duration を確定しない。
既存 one-shot wrapper は local process lifetime を観測できるが、host 全体の episode identity は証明しない。

## 各ホストへの適用範囲

論理的な終端の不変条件は全ホストで共通とし、callback の完全性と相関の根拠は個別に扱う。
native/runtime ID の文字列は相関の識別情報であり、認証や認可の scope ではない。

| Host | C での能力の境界 |
| --- | --- |
| Claude | interactive `SessionEnd` は `End` を呼び、`StopFailure` は受動。両方の mapping を維持 |
| Codex | `SessionEnd`/`Interrupt` は受動。文書化された callback でも instance/order の識別根拠は不十分 |
| Gemini | interactive `SessionEnd` は `End` を呼ぶ。runtime instance の根拠が欠ける場合は不明とし、終了を捏造しない |
| Grok | end の根拠が欠ける場合は不明とし、終了を捏造しない |
| Kimi | interactive `SessionEnd` は `End` を呼び、`Interrupt` は受動。強い相関を推測しない |
| Muse | end の根拠が欠ける場合は不明とし、終了を捏造しない |
| Antigravity | end の根拠が欠ける場合は不明とし、終了を捏造しない |

この表は設計上の制約であり、全クライアントの hook coverage を検証したという主張ではない。

## 比較した選択肢

| 案 | 利点 | コストまたは成立しない仮定 | 推奨 |
| --- | --- | --- | --- |
| A: `endedAt` を消して Session を再オープン | 既存 entity を再利用できる | 最初の終端、one-shot 所有権、再試行 ledger と衝突し、古い End の遅延 replay が再開した generation を終端し得る | 不採用 |
| B: 論理 Session と別の `RuntimeEpisode` | 論理終了を変えず、証明されたホスト instance を表現できる | 安定した instance/correlation、replay ID、因果順序、並行クライアント規則、schema と bundle の設計が必要 | 将来の条件付き案のみ |
| C: 論理 Session と受動的なホスト観測 | 既存 lifecycle を維持し、既知の事実だけ記録する | runtime の利用可能性、継続時間、episode 数は分からない | 現在の推奨、人間の承認待ち |

episode を child Session として表現しない。
child は委譲作業を意味し、再帰的な終了と GC の対象になるため、runtime 再起動とは意味が異なる。

## 概念モデルと不変条件

| 概念 | 状態と振る舞い | 制約 |
| --- | --- | --- |
| 論理 `Session` | 既存の集約 lifecycle。明示終了は終端 | 最初の終端時刻と理由を維持し、再オープンしない |
| ホスト終了の観測 | 取得済みの論理 identity に関連付けたホストの報告 | 集約、子孫、one-shot owner を終了させない |
| 従来の activity 選択 | 既存の `active`、`ended_with_late_events`、stale/activity 規則 | activity は集約の終端状態や runtime availability とは別 |
| runtime availability | 現在の根拠では `UNKNOWN` | close の受信は現在のホスト停止を証明しない |
| 将来の `RuntimeEpisode` | 条件付きの host instance projection | 独立に証明された相関と replay/order の識別情報が必要 |
| 将来の continuation 関係 | 論理作業レコード間の明示的な関係 | 別の承認済み設計が必要。child や自動 resume binding ではない |

1. 集約の論理終端状態、従来の activity query、runtime availability の三つを分離する。
2. close の要約の意味は「最後に記録されたホスト終了の観測」とするが、最終的な CLI/API 文言は暫定である。記録時刻は発生時刻、因果順序、継続時間、episode 数ではない。
3. host event ID がなければ、再受信と別の close 発生を区別できない場合がある。semantic fingerprint ではこの曖昧さを解消できない。
4. replay 順序と recorded-at は runtime の因果順序を証明しない。
5. 目標は取得時に、spool 保存前に native identity、解決済み Workspace/local root、固定 database route を結び付けること。#2393 は SID、絶対 database route、raw cwd のみ固定済みで、Workspace 全体の binding は gap として残る。将来の episode identity も replay 時に再探索せず、spool 前に固定する。
6. 遅れて届いた古い close は新しい generation を終了させない。generation identity が証明できなければ、現在の episode を推測して選ばない。
7. 明示終了、one-shot completion、子孫の終了、既存 GC の `legacy_unknown` 終了を維持する。GC は論理 root を終端でき、この制約を暗黙に変更しない。SQL は `runtime_mode` を制限せず、保護されない長期 idle の one-shot を終端して `FinalizeOneShot` と衝突し得る。owner のみの完了は意図する境界であり、GC は既知の例外。
8. 論理終端後の resume で再オープンや新しい continuation の自動 binding を行わない。archive、delete、通常 close の reason は区別に不十分であり、restore から continuation を推測できない。
9. 同じ native thread を複数のクライアントが使っても、単一の現在 runtime episode を意味しない。

## 責務と interface 案

以下は意味上の契約であり、承認済み API 名や DTO schema ではない。
host payload と SQLite の詳細は domain の外に置く。

| Layer / owner | 責務と境界 | 失敗と非責務の契約 |
| --- | --- | --- |
| Domain | 論理終端の不変条件と観測の意味を所有。episode の不変条件は将来承認された場合のみ | host DTO や保存順序から lifecycle を推測しない |
| Application write | 取得済みの論理 identity を持つ正規化済み観測を受け、既存 event 記録を `End` なしで調整 | identity が欠落または曖昧なら推測した Session を変更しない。自動 continuation は行わない |
| Application read/query | 終端、従来の activity、観測の要約を別の read concept として提示 | availability は unknown。`active` を暗黙に再定義しない |
| Presentation / adapter | source/reason を解釈し、enqueue 前に native ID と local root を論理 identity に結び付ける | 不完全な根拠を明示し、推測で補わない |
| Presentation / CLI | 稼働時間を断定せず、記録された観測と不明な availability を表示 | 別途承認されない限り既存 flag/output 契約を維持 |
| Infrastructure | 取得済み SID/database/raw-cwd context を保存して replay。完全不変の Workspace binding を目標とし、repository と将来の projection storage を実装 | 取得 SID/database を再 binding しない。Workspace の現在の再解決は既知の gap。episode を推測しない |

既存の session use case は集約 write（`End`、`FinalizeOneShot`）を所有する。
`Active` は start Event を返す read であり、集約操作ではない。
観測の記録は別の意味を持つ操作であり、集約終了を再利用すると受動的な報告に再帰的な副作用が混入する。
適切な場合は event 記録を再利用し、汎用 host lifecycle engine、Strategy 階層、hook 名ごとの use-case class は作らない。
観測に別の public method が必要かは、consumer と transaction の境界を根拠に checkpoint で判断する。

## 振る舞いの受け入れ仕様

以下は承認後の実装に対するテスト案であり、この文書変更で実行したテストではない。

| 前提と操作 | 観測可能な結果 | Level |
| --- | --- | --- |
| 開いた論理作業で同じ Codex thread を受動 close/resume/close/resume | 論理レコードは非終端のまま。episode 数や availability を確定しない | Hook integration + read |
| Codex の `compact` または `clear` の SessionStart | 新しい instance を推測せず、論理再オープンもしない | Adapter |
| 開いた Codex thread を archive/delete 後に restore | close は報告のまま。`other` では原因を区別できず、continuation を推測しない | Integration |
| 全クライアントが離脱して idle、close が後から届く | 記録時刻を停止時刻と扱わず、現在も利用不能とは断定しない | Read |
| 同じ event ID を replay | 既存の文書化された配信冪等性の範囲で重複効果を防ぎ、論理状態は不変 | Delivery integration |
| event ID がなく、同じ payload を二度受信 | 一回の発生か二つの episode かを断定せず、既存 receipt/dedup の意味を維持 | Delivery + read |
| 新しい resume 後に古い close を replay | 固定した古い routing を維持し、終了や現在 episode の推測更新をしない | Spool integration |
| Codex/Kimi の受動 Interrupt 後に resume | interrupt は論理終了でも証明された runtime 境界でもない | Adapter + domain |
| 複数クライアントが同じ thread を使う | 単一の episode に推測でまとめず、availability は unknown | Concurrency integration |
| 既知の GC 例外を除き、one-shot command の入れ子受動 callback が close を受信 | owner completion の境界を維持し、close は本人や子孫を完了させない | Use case |
| 終端 Session が同一 ID の start/resume 後に event を受信 | 論理 ID と最初の end は不変。event は late event として保持され、従来 active の対象になり得る。continuation ではない | Hook + read |
| 終端 Session が受動 close note を受信 | end は不変。note は保持され、既存 activity query の late event に数えられ得る | Hook + read |
| commit 後に spool clear が失敗して local replay | host 再配信と local 重複を区別。将来の receipt ID は episode/order の断定なしで replay を dedup できる | Delivery integration |
| 終端した parent が close/resume を受信 | 最初の終端を維持。再オープン、子孫再作成、自動 continuation はしない | Domain + use case |
| 明示的な end が子孫を再帰終了 | 既存動作を維持し、観測では取り消さない | Use case |
| 保護されない長期 idle の one-shot が finalization 前に GC 対象になる | 現在の GC 終端と FinalizeOneShot の衝突を明示。owner のみの完了は意図する境界で、現在の GC 保証ではない | Storage + use case |
| GC が stale root を `legacy_unknown` で終了 | 後の resume でも終端を維持し、この制約を明示する | Storage integration |
| C で既存 bundle を export/import | schema と論理/event レコードの互換性を維持し、episode を捏造しない | Bundle integration |
| 将来の B bundle を古い importer が開く | rollout 前に format gate/rejection を検証し、未知 table を黙って失わない | Compatibility |
| 将来の episode identity が欠落または衝突 | 推測 episode に投影せず、観測を残し、不確実性や error policy を提示 | Future B integration |

## TDD plan

| Step | Red の仕様 | 最小の Green | Refactor の境界 |
| --- | --- | --- | --- |
| 1 | 特定済み受動 callback が集約や子孫の終端状態を変更 | 集約の `End` を呼ばず受動報告を記録 | Domain invariant と application capture |
| 2 | replay が別 session/store に解決される | 取得時の routing context を固定して replay | Presentation acquisition と infrastructure transport |
| 3 | output が稼働/停止、duration、episode 数を断定 | 終端、activity、記録観測を分離して unknown を表示 | Query DTO と CLI rendering |
| 4 | 明示終了、one-shot、GC、bundle が退行 | 既存の観測可能な振る舞いを維持 | 汎用 event 記録に lifecycle flag を追加しない |
| 将来の B のみ | identity/order または format 互換性が不成立 | 承認後に限定した identity/projection 契約を追加 | Episode invariant owner と persistence |

実装 commit 前に、影響する unit、SQLite/spool/bundle integration、host fixture、CLI contract test を実行する。
fresh CI と独立レビューは実装の gate であり、文書検証の成功は lifecycle 検証ではない。

## 人間の checkpoint と実装段階

最初に人間のメンテナーが C の許容性、以下の product 判断、実装 scope を決定する。
独立した architecture/lifecycle、delivery、bundle reviewer は、その前に不変条件と根拠を確認する。
明示的な判断が記録されるまでは Proposed を維持する。
この high-risk ADR の draft を自動で Ready に変更したり merge したりせず、人間のメンテナーが GitHub UI でその遷移を行う。

承認後、独立した ticket を作り、各 ticket に一つの PR を対応させる。

1. 完了した #2393 の受動取得と routing を baseline とし、新しい実装 ticket にしない。レビューで残存 gap が判明した場合だけ、その実証された gap に限定した別 ticket を作り、全 routing 修正とは主張しない。
2. 既存 `active` を再定義せず、観測/read 契約と利用者向けの不確実性の表示を追加または明確化する。
3. 明示終了、終端後 resume、GC、bundle の regression fixture と運用文書を追加する。
4. host evidence spike が安定した instance/correlation と replay/order を証明した場合に限り B を検討し、別の人間承認付き設計と versioned migration/bundle plan を作る。

これらは提案であり、外部 issue 作成や実装を事前許可するものではない。

## 影響、移行、rollback

C は現在の schema と論理 lifecycle を変更しない。
runtime が現在接続されているか、何度再起動したかには答えられず、観測の表示はこの制限を伝える必要がある。
既存の明示終了と GC の結果は、後の再開可能性を制約する場合も終端として維持する。

close/start 時刻、fingerprint、archive/restore の推測から履歴 episode を backfill しない。
C の read 契約は既存 event を使い migration を要しない。
承認時に schema 変更が必要と判明した場合は、B と同じ checkpoint と versioned bundle gate を適用する。
将来の B は bundle format compatibility gate、明示的な unknown historical state、upgrade/old-import 検証の後にのみ additive storage を使う。
additive SQL migration だけでは importer の拒否契約を解決できない。

C の rollback は新しい観測 projection または adapter mapping を無効化し、論理レコードと記録済みの事実を残す。
将来の B は projection を無効化し、論理レコードと回復可能な episode data を残して、承認済みの bundle recovery/export 手順を使う。
どちらも `endedAt` の消去、child Session への変換、continuation link の捏造は行わない。
wrong-route replay、終端状態の変更、誤解を招く availability、bundle data loss を rollback の条件とし、release 前に復旧経路をテストする。
credential や raw transcript は収集せず、routing failure、replay conflict、unknown identity の件数を限定して監視する。

## 未決定の product 判断

- unknown runtime availability を許容するか、host の強い根拠を提供前に要求するか。
- close の要約を既定で表示するか。安定した CLI/API label と時刻の出所をどう定義するか。
- host event ID がない場合、どの receipt/dedup 契約を利用者に提示し、local receipt ID を追加するか。
- 受動観測を activity から除外するか。C の隠れた filter ではなく、別の public-contract change が必要。
- GC で one-shot を除外するか、dormancy や別の reason を使うか。別の人間判断であり、この ADR は変更を許可しない。
- GC または明示終了後に利用者がどう明示的に続行するか。continuation 関係は別の承認済み設計が必要。
- B が実現可能になった場合、episode は client 単位か host-defined runtime instance 単位か。並行 client の何を終了条件にするか。
- どの host instance、event identity、ordering 契約が安定していると実証できるか。現在の thread ID と start/end reason では不十分。
- B に必要な versioned bundle format、downgrade/recovery policy は何か。

## セルフレビューと検証範囲

この推奨は domain が所有する終端不変条件を維持し、transport の報告を集約 lifecycle から分離する。
テスト案は private な呼び出し順ではなく、観測可能な状態と output を守る。
identity/order の不明点と legacy GC の制約を、新しい抽象化で隠さず明示する。
この PR は対訳の設計文書だけを変更し、文書 pairing、removed-alias check、`git diff --check` は成果物を検証するものであり、実ホストや runtime 実装の検証ではない。

## 参照

- [Issue #2394](https://github.com/duck8823/traceary/issues/2394)
- [受動取得と routing の作業 #2393](https://github.com/duck8823/traceary/issues/2393)
- [Codex hook 契約](https://developers.openai.com/codex/hooks)
- [アーキテクチャ原則](../architecture/README.ja.md)
- [Event lifecycle](../lifecycle.ja.md)
- [Storage model](../storage/README.ja.md)
