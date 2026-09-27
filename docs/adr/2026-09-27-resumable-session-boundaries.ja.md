# ADR: lifecycle 管理を持たない Session identity

[English](./2026-09-27-resumable-session-boundaries.md)

- Status: Proposed
- Date: 2026-09-27
- Decision owner: Traceary メンテナー（人間の checkpoint）
- Reviewers: architecture、context/handoff、hook 配信、execution、storage/bundle、独立検証の担当
- Related issue: [#2394](https://github.com/duck8823/traceary/issues/2394)
- Supersedes: 同じ設計 PR の commit `f502b939` までに記録された、C の受動 close/runtime lifecycle 案。

ユーザーは会話で新しい方針を承認した。
Traceary は AI の audit log、event、refinement を記録し、Session は lifecycle を管理するオブジェクトではなく、記録をまとめる identity とする。
この文書は以前の推奨を置き換えるものであり、runtime monitoring を追加する案ではない。
具体的な実装と public contract の移行は Proposed のままとする。
Ready と merge は人間のメンテナーが GitHub UI で行い、自動遷移は許可しない。
この PR は対訳の設計文書のみを変更し、`Refs #2394` を使い、issue を open のまま維持する。

## 要求の要約

active、非終端、直近に開始した Session を条件にせず、identity に関連する記録を追加して取得する。
context/handoff の選択と表示、通常の host closure、合成した stale closure から lifecycle 依存を除去する。
identity、source AI、Workspace、parent の委譲 lineage、label、model metadata、記録 event、refinement coverage は維持する。
execution の success/failure/timeout/signal は意味を持つが、Session status ではなく invocation outcome に属する。
再開した log を受けるためだけの reopen、generation、runtime availability、`RuntimeEpisode`、continuation entity は不要である。
この意味を実現するために legacy column を物理削除する必要はない。

対象外は runtime monitoring、uptime 計測、過去履歴の書き換え、非空 log の自動削除、本番操作、この設計作業での install/login/release 実行とする。

## 現在の振る舞いと根拠

現在の public behavior には lifecycle 由来の status が存在する。
この設計は「status は存在しない」と主張するのではなく、その依存を変更する。
レビューしたリポジトリの baseline は次のとおり。

- `handoff.go:126` は `STATUS` を出力する。
- `context_pack_builder.go:74-75,335-342` は start 時刻の stale window による選択制限を適用する。
- `output.go:127-144` は現在未使用の `sessionSummaryOutput` DTO を定義する。
- Session の `Active`/`Latest` は Event を返し、context summary の consumer 契約ではない。
- legacy active query は `ended_with_late_events` を含み得る。
- `update_stale_sessions` は `runtime_mode` を制限せず、長期 idle の one-shot が finalization 前に終端し得る。
- bundle import は store より新しい manifest schema version を拒否し、その後に未知 table を拒否する。

現在の interactive Claude、Gemini、Kimi の `SessionEnd` は集約の `End` を呼ぶ。
現在の受動 mapping には Codex `SessionEnd`/`Interrupt`、Claude `StopFailure`、Kimi `Interrupt` がある。
interactive の論理 ID は通常 native `session_id` を使うが、one-shot wrapper 内は例外である。
繰り返し start は冪等に成功し、後の同一 ID event は終端済みレコードに付く場合がある。
これらは異なる現在の mapping であり、全ホストの hook coverage が同じという主張ではない。
新しい案は Session の終端不変条件を保護するのではなく、通常の追跡 Session の terminalization を除去する。

[#2393](https://github.com/duck8823/traceary/issues/2393) は対象の受動経路で取得済み SID、絶対 database route、raw cwd を固定する。
解決済み Workspace 全体は固定せず、`TRACEARY_WORKSPACE` と repository detection は replay 時に再解決し得る。
この routing gap は lifecycle 除去とは独立であり、どちらも全 legacy routing の修正を証明しない。
native ID は相関のデータであって、認証や認可の scope ではない。
host identity の曖昧さには検証した namespace/store routing を使い、identity の捏造や自動 cross-store merge を行わない。

## 比較した選択肢

| 案 | 利点 | コスト | 推奨 |
| --- | --- | --- | --- |
| lifecycle を authoritative に維持 | 意味の変更を抑えられる | stale/terminal による read 除外と host-driven state が log 記録の目的と衝突 | 目標として不採用。legacy wire 互換のためのみ保持 |
| 別の optional runtime episode | 強い host 根拠があれば instance を表現可能 | log 受け入れと無関係な monitoring、identity/order、schema 作業を増やす | この要求のためには導入しない |
| Session を log grouping identity のみにする | append/retrieve/refine の目的に合い、execution outcome は別に残る | caller audit、public-contract 移行、execution の分離が必要 | 推奨、実装 checkpoint 待ち |

## 概念モデル

| 概念 | 状態と振る舞い | 不変条件 |
| --- | --- | --- |
| Session identity | 記録と metadata をまとめる | append/retrieve は `endedAt`、active/stale、start age に依存しない |
| Event | identity と provenance を持つ audit/content の記録 | 有用な履歴、redaction、信頼できる配信を維持し、現在の状態を捏造しない |
| Refinement | summary と明示的な coverage | coverage は単調。新 event-stream assembler を要求せず、既存 consumer を維持 |
| Context selection | 明示 identity/selector または関連する最新の記録 Session | terminal state ではなく relevance と明示 filter で選択 |
| Invocation / ExecutionResult | success/failure/timeout/signal と usage を持つ one-shot process completion | execution owner が completion/idempotency を所有し、後続 log で outcome を上書きしない |
| Parent lineage | 委譲関係 | parent の完了で child の append を拒否しない。lineage は process cancellation ではない |
| Legacy lifecycle field | 履歴互換のデータ | 当初保持し、通常の選択 authority にしない。typed one-shot outcome は分離 adapter で読む |

Invocation は概念上の責務であり、承認済み table や API schema ではない。
互換実装は現在の one-shot mode/end/reason storage と専用 wrapper SID の binding を維持する。
任意の additive invocation storage は別の承認済み migration plan が必要である。
過去の `runtime_mode=one_shot` は run row を示すが、terminal reason を書いた actor は示さない。
正確な保存済み typed reason を、backfill や書き換えなしで、分離した read-time compatibility adapter を通じた記録上の過去 result/legacy metadata として保持する。
確認済み supervisor outcome として投影するのは、信頼できる supervisor-finalization/provenance の根拠がある場合のみとする。
古い host/直接 end/parent cascade が finalization より先に close した可能性があれば、実際の outcome は unknown とし、記録 reason と provenance の限界を併記する。
reason だけから実際の success を断定しない。
古い first-reason reconciliation は最初の reason を保持して矛盾を拒否するが、writer を証明しない。
根拠を認識する厳密な契約は実装 checkpoint で定め、新 table や一律の heuristic はここで追加しない。
interactive End も success を書くため、one-shot provenance のない success reason は execution outcome ではなく、unknown は unknown のままとする。
後続 log で記録上の過去 result や確認済み outcome を消したり上書きしたりしない。
現在の `session run` が invocation ごとに新しい専用 SID を作る動作を維持する。
取得した wrapper SID は明示的で不変の 1:1 compatibility binding により execution を識別し、delivery receipt は execution identity と区別する。
現在は新 Invocation ID/table を必要としない。
同じ会話への複数 invocation の関連付けは要求されておらず、別の将来 identity/migration 設計が必要である。
意味のある completion の最初の終端不変条件は、Session から execution owner に移す。
後続 Session event で execution result を消したり再計算したりしない。

## 責務と consumer 向け interface

| Owner | 責務と consumer 境界 | 行わないこと |
| --- | --- | --- |
| Domain Session/identity | identity、source AI、Workspace、委譲 metadata | lifecycle で log/read を許可または拒否すること |
| Domain execution owner | completion と不変の invocation outcome | outcome を Session closure と扱い、後続 log を拒否すること |
| Application event/refinement write | 有用な記録を追加し、coverage と配信冪等性を維持 | 過去の end/GC marker で再開 log を拒否すること |
| Application context query | 現在の filter、MemoryAsOf、bounds で既存の関連 context/refinement を返す | lifecycle snapshot の取得だけのために `Active`/`Latest` 経由にすること |
| Presentation context/handoff | 選択した content と記録 provenance を表示 | `STATUS`、lifecycle duration、runtime availability の追加や human summary の書き換え |
| Presentation host adapter | callback を個別解釈し、enqueue 前に固定 SID/DB を取得。Workspace 固定は目標/gap | inactivity から closure を推測したり cleanup で log を捨てたりすること |
| Application invocation / supervisor | process completion、cancellation、usage、wrapper routing | 再帰 Session End の除去と subprocess cancellation の除去を混同すること |
| Infrastructure | 互換 legacy data を保存し、bounded retrieval と replay を実装 | 古い end/import で read 除外を再有効化したり schema を暗黙移行したりすること |

取得済み identity への append、identity/relevance と現在の coverage consumer/filter/bounds による既存 context selection、outcome による invocation finalization という小さな consumer 契約を使う。
method の追加や置換前に、既存の Event を返す `Active`/`Latest` の caller を調べる。
`List ActiveOnly`、`FindEndedSessionIDs`、parent inference、doctor stale diagnostic/fix、hook-local end marker も lifecycle-gate audit に含める。
active/stale eligibility の除去で parent 推定を広げず、根拠のある spawn lineage を維持し、不十分なら unknown とする。
広い lifecycle facade や汎用 monitoring framework は導入しない。
未使用 `sessionSummaryOutput` と派生 `SessionStatus` の伝播は、caller audit で影響範囲と public output 互換義務を確認してから削除する。
SessionSummary は content aggregate と coverage を持ち続けてよく、未使用 DTO の削除は全 summary type の削除ではない。
public list/status output が存在しないとは仮定せず、影響する各 public consumer の移行を明示的に決定する。

## Context と handoff の契約

明示 identity selection は暗黙の最新選択より優先する。
暗黙選択は指定した source/workspace scope の関連する最新の記録 Session を選ぶ。
lifecycle に依存しない既存 latest-selection scope、順序、同順位の決め方は維持し、active/stale eligibility のみ除去する。
登録のみの record より有用 content を優先する ranking 変更は別の follow-up checkpoint とし、この変更に暗黙に含めない。
phase 1 は除去のみとし、実際の既存 selection/order、field、refinement/compact behavior、recent-command limit、memory count budget を維持する。
新しい汎用 context assembly engine は要求しない。
現在の handoff as-of は `MemoryAsOf` であり event cutoff ではなく、その実際の範囲を維持する。
summary coverage は既に記録するが、一般的な未 coverage event の追加や event-as-of/token/time budget がすべて現在の handoff 機能とは限らない。
coverage-bounded event stream、event cutoff、新 token/time framework は必要な場合のみ別の将来機能とする。
24 時間以上前に開始した Session も明示 lookup の対象であり、`startedAt` の古さだけで拒否しない。
stale による handoff 再 query と `session end` で閉じる案内も、stale 拒否とともに除去する。
対応する明示 user date/content filter と既存の memory as-of cutoff を維持する。
時刻は記録データであり lifecycle 境界ではない。
生成された `STATUS`、lifecycle duration、稼働/停止の断定を、versioned release document、golden output test、JSON/downstream field audit とともに意図的に除去する。
`--allow-stale` と stale-after consumer を調査し、lifecycle eligibility 除去後は文書化した deprecated no-op compatibility flag にする移行を推奨するが、public version policy に従う。
その語を含む human-authored summary は編集しない。
既存 refinement coverage と履歴 note を維持し、この変更で新しい未 coverage event の handoff stream を導入しない。

## Host callback と配信の契約

通常の追跡 Session の `SessionEnd` は本人と子孫を terminalize しなくなる。
取得した SID と固定 DB routing を使って usage/transcript の flush、限定した diagnostics、hook state cleanup を行うことはできる。
cleanup や遅延 callback によって flush/replay の log を黙って破棄しない。
hook-local end marker を調べる。既存 start は marker を消さず成功し得て、Stop の stale-marker 処理は state を消したり flush/extract/routing fallback を抑制したりし得る。
意味上の end gate を除去する一方、scope を限定した receipt で callback/delivery dedup を維持し、dedup 全体を無効化しない。
end-only の空 payload に対する推測 host-close content note は既定で書かない案とする。
これは noise を減らすものであり、event 全体の除去ではない。
必要な Interrupt/StopFailure の有用な audit outcome は、正しい provenance で残し、Session state を装う label を付けない。
既存の start/end/close event は履歴として残し、過去の purge や body 書き換えは行わない。

運用 receipt の最小の代替は、既存 delivery ledger/spool metadata と限定した sanitized diagnostics であり、新しい event framework ではない。
commit-before-spool-clear の dedup に durable local receipt ID や取得時の `received_at` が必要なら、その storage を別途承認する。
host event ID がない再配信から、別の host occurrence や runtime 順序を推測できない。
local receipt は local replay を dedup できるが host episode を確定しない。
spool `CreatedAt` は保存 event の `recorded_at` ではなく、どちらも runtime duration を証明しない。
redaction、取得時 routing、retry reliability、有用な failure log を維持する。

| Host | 個別の根拠が必要な契約レビュー |
| --- | --- |
| Claude | 通常の interactive SessionEnd terminalization を置換し、必要な flush/cleanup と有用な StopFailure outcome を維持 |
| Codex | 推測 SessionEnd note の既定書き込み除去を確認し、有用な Interrupt outcome と flush/cleanup を維持 |
| Gemini | 通常の interactive SessionEnd terminalization を置換し、start/clear と flush/cleanup を個別確認 |
| Grok | 実際に対応する hook と取得を確認し、callback 不在から end を合成しない |
| Kimi | 通常の interactive SessionEnd terminalization を置換し、有用な Interrupt outcome を維持 |
| Muse | 実際の capture/receipt を確認し、lifecycle callback を捏造しない |
| Antigravity | 実際の capture/flush/cleanup を確認し、lifecycle callback を捏造しない |

この表は将来の契約検証を定めるものであり、host coverage の認証や実動作テストの結果ではない。

## One-shot、明示 end、housekeeping

one-shot CLI exit code、process cancellation、signal、timeout、usage capture、固定 wrapper SID/DB routing、active-execution protection を維持する。
process 完了後も Session は append 可能とする。
再帰 Session End の無効化で、supervisor が子 process を停止する責務は消えない。
execution-result 分離は、legacy end import/replay で execution outcome が上書きされないようにする。
phase 2 の前または同時に、GC/doctor fix、parent cascade（現在の `FindOpenChildSessionIDs` は mode filter を持たない）、host end、直接の `session end`、outcome を変える import/replay から one-shot row を atomic に保護する。
scope を限定した compatibility import は古い記録 result を復元できるが、現在 result を上書きしたり、曖昧な provenance を確認済みに昇格したりしない。
execution result を書けるのは supervisor の `FinalizeOneShot` のみとし、実証された atomic replacement までは現在の finalization と first-result reconciliation を維持する。
one-shot に対する明示 `session end` は log/outcome を変えず、対処方法のある拒否を返し、成功を装わない。
現在の `cli:session-finalize` による “one-shot process finished: reason” を content refinement に自動で上書きすることを止め、outcome は execution record/projection に属させる。
過去の生成 refinement は変更せず、表示 policy は別の判断とする。

現在の public `session end` command を暗黙に再解釈しない。
目標は `session start` が identity と start marker を冪等に登録し、`session end` が明示 end marker のみを記録し、terminalization や retrieval gate にしないこととする。
versioned release document と限定した compatibility adapter で lifecycle 解釈を deprecate し、既存の ID output と flag は可能な範囲で保持して contract test で検証する。
段階が承認されるまでは bridge は古い振る舞いを維持し、即時の冪等性や互換性は保証しない。
新しい marker semantics の実装前に public-contract migration checkpoint を置く。
既存 End use case は summary/refinement と cascade もまとめて処理する。
marker-only 置換では提供された summary と coverage を維持し、終端の副作用だけを除去する。
移行中の古い end marker は legacy field/event に残せるが、後続 content を除外せず、分離した invocation outcome を変更しない。
warning、exit code、structured output、marker の詳細は、この選択した移行内で決定する。

synthetic stale close を無効化し、GC は housekeeping として `endedAt` を捏造しない。
opt-in の bounded retention/empty-orphan housekeeping は log identity と分離する。
委譲 lineage、refinement reference/coverage、pending spool/receipt、active one-shot execution を保護し、削除案の前に安全な orphan 条件と bounded dry-run を定める。
非 ended status であるだけでは housekeeping 対象にしない。
古い、または ended と記録されたという理由だけで非空 log を削除しない。
より広い retention 削除は別の明示承認/設計が必要であり、この案には含めない。

## 振る舞い仕様と TDD plan

以下は受け入れテスト案であり、この文書変更で実行したテストではない。

| Scenario | 観測可能な目標 | Test level |
| --- | --- | --- |
| 同じ native identity が明示/GC end marker 後に再開して append | 同じ grouping identity、古い marker 維持、新 event は query 可能。reopen/generation 不要 | Hook + query integration |
| start/end を重複配信 | 有用 log を失わず、completion を重複せず、文書化した receipt scope を維持 | Delivery integration |
| host event ID がない | occurrence/order を捏造せず、local replay reliability と redaction を維持 | Spool integration |
| 24 時間以上前に開始した idle Session を明示選択 | stale-start 拒否なく関連 event/refinement を返す | Context integration |
| 過去 refinement coverage と close note | 既存 coverage/content の振る舞いを維持し、purge や新 event stream を要求しない | Context query |
| 現在の別 run が専用 SID を作り独立 retry | 不変 wrapper binding と run ごとの first result/reconciliation を維持し、後の同一 SID log は query 可能 | Invocation integration |
| 過去 one-shot typed outcome と interactive success/legacy_unknown | 記録 typed reason は保持し、確認済み outcome には supervisor 根拠が必要。unknown は不明。backfill や後続 log の上書きなし | Compatibility query |
| 過去 one-shot が host/cascade で早期 success を記録し、finalization が failure を試行 | raw 保存 success は export 可能。矛盾から確認済み success とはせず、根拠がなければ actual outcome は unknown | Compatibility + finalization |
| host/直接 end、GC/doctor、parent cascade、import/replay が one-shot row に到達 | atomic guard で outcome 保護。直接 end は対処方法のある拒否。supervisor finalization 維持 | Use case + SQLite |
| local end marker 後の resume と end/Stop | flush/extract、固定 routing、cleanup が欠落なく動き、receipt dedup も維持 | Hook fixtures |
| host-closed sibling と新しい無関係 Session がある時の parent inference | 根拠のある spawn/lineage のみ受理。不十分なら unknown とし、近い active/unended Session を選ばない | Hook parent fixture |
| 古い unended のみ、または古い unended/新しい ended 混在で暗黙 latest | 全対象が選択可能で、既存の決定的順序と根拠のある parent inference を維持 | Query integration |
| 明示 end に summary/refinement を提供 | marker-only でも summary と coverage を維持し、再帰終端しない | Use case + CLI |
| one-shot success/failure/timeout/signal 後に Session event | CLI/process outcome を維持して append し、completion conflict なし | Supervisor + use case |
| parent execution 完了後に child が log 出力 | child lineage/content を保持し、process supervision は独立 | Domain + integration |
| host end callback に有用 usage/transcript | 取得 SID/DB で flush、限定 diagnostics と cleanup。terminalization や黙った破棄なし | Host fixtures |
| end-only の空 callback | 推測 close content note は既定で書かず、operational receipt reliability を維持 | Host + delivery |
| schema 変更なしで legacy end/runtime field の bundle を import | 履歴保持。read 除外の再有効化や新 execution outcome の上書きなし | Bundle integration |
| 既存 command/memory limit、compact behavior、MemoryAsOf | 実際の既存 bounds/field を維持し、生成 STATUS/availability/duration は除去。event cutoff を捏造しない | Query + CLI |
| human summary に status prose | summary は不変で、生成した lifecycle scaffold のみ除去 | Rendering |
| GC が idle 非空 record/active execution を検出 | synthetic close/非空 purge なし。execution と reference を保護 | Storage integration |
| 七つの host integration の契約変更 | 個別 fixture が flush、routing、cleanup、有用 failure、receipt を証明 | Adapter integration |

| TDD step | Red | 最小 Green | Refactor の境界 |
| --- | --- | --- | --- |
| read 依存を先に除去 | end/stale-start が関連 context を拒否、または STATUS を表示 | 既存 scope/order query の lifecycle gate と生成 lifecycle 表示除去 | Consumer query と古い Event helper |
| 通常 closure writer | host callback/GC が authoritative terminal state を生成 | closure なしで flush/diagnostics/cleanup。空 close note の既定生成なし | Host 解釈と有用 event capture |
| execution 分離 | Session append/import/replay が one-shot result と衝突 | execution owner に completion を置き、process outcome と active protection を維持 | Invocation outcome と Session identity |
| 互換性 | 古い bundle/end marker が除外を戻す、または履歴を失う | legacy wire 維持。Session eligibility read は非 authoritative、記録 result は保持し、確認済み outcome の provenance を明示。public adapter をテスト | Storage 互換と product semantics |

実装 checkpoint で影響する unit、CLI output、context、SQLite、hook/spool、supervisor test を実行する。
schema の段階では migration、index、bundle 互換、recovery test も必要である。
delivery 前に独立レビューと fresh CI が必要であり、文書 check のみでこれらの振る舞いを検証したとは扱わない。

## 人間の checkpoint と delivery 段階案

会話は方向性を承認したが、すべての public transition や migration 詳細を承認したわけではない。
実装前に既存 context selection 維持/output 移行、explicit-end deprecation、host contract 変更、execution protection、互換境界を承認する。
checkpoint 後の限定した各実装段階を、一つの ticket/branch/PR に対応させる。
これは提案であり、今 ticket を作る許可ではない。

1. caller を調べ、context/read lifecycle 依存と未使用 status DTO の伝播を除去し、legacy wire/storage は維持する。
2. closure writer 停止前または同時に caller audit を完了し、parent inference、Active/List ActiveOnly/FindEndedSessionIDs、doctor stale diagnostic/fix、hook-local end gate を移行する。parent 推定は根拠のある spawn/lineage のみとし、不十分なら unknown として近い active/unended candidate を選ばない。doctor --fix を synthetic closure writer として残さない。GC/doctor、parent cascade、host/直接 end、import/replay の atomic one-shot guard を設け、FinalizeOneShot reconciliation を維持する。その後で通常 closure writer と synthetic GC close を除去し、各 host の flush/cleanup、有用 outcome、空 note policy をレビューする。housekeeping は opt-in の安全な範囲のみ設計する。
3. 現在の専用 wrapper SID と typed compatibility storage で one-shot outcome を分離し、outcome refinement の自動生成を止める。shared-session invocation や新 identity/table は不要であり、任意の additive storage は別の承認済み設計が必要。
4. lifecycle column の物理削除は別の任意判断とし、この機能の必須条件にしない。

release 前に isolated DB/store fixture と sanitized controlled real session で resume、flush、refinement、replay、one-shot outcome を dogfood する。
DB/store、`TRACEARY_HOOK_STATE_DIR`、spool/queue root、receipt、GC marker、lease、diagnostic、usage offset を該当する範囲で隔離し、dogfood 前に override が実際に有効なことを確認する。
fixture routing と redaction を先に定義し、認証は触らず、private log を既定の test input にせず、本番 host config を変更しない。
この設計作業では install、login、本番操作、release を行わない。

## 移行と rollback の安全性

当初は `endedAt`、`runtimeMode`、`terminalReason` と既存 bundle wire 表現を、DROP、backfill、履歴変更なしで維持する。
legacy field は通常 Session status の authority にしないが、過去 typed reason は記録 metadata として保持し、確認済み supervisor outcome は分離 execution compatibility adapter を通じた信頼できる provenance を必要とする。
既存 wire field と記録時刻は当初維持し、date diagnostics、明示 scope の retention input、content filter は lifecycle 由来の context scaffold とともに一律除去しない。
read 変更を通常 closure writer/GC 除去より先に行い、execution-result projection は分離した段階で行う。
新実装は、旧版の end data の import/replay で除外を暗黙に戻したり新 outcome を置換したりしない。
legacy boundary event/column の保持は古い bundle 表現を支えるが、全 cross-version roundtrip semantics の保証ではない。
意図的な新契約の非互換と version gate を文書化する。
旧 binary は新しい semantics を実装せず、mixed-version 動作に同じ意味を保証しない。

schema migration がなければ app rollback は技術的に可能だが、自動的に安全とはならない。
旧 binary の最初の GC/doctor が新しい unended record に close を大量に合成し、stale read が拒否し得る。
旧 closure writer の freeze または isolated recovery copy と明示的な運用判断を必要とし、現在 freeze flag があるとは主張しない。
全記録データを維持する。
feature/config rollback switch を選ぶ場合は read/writer の対象範囲と互換動作を checkpoint で定義する。
現在その flag があるとは主張しない。
将来の Invocation schema/index 変更は versioned migration と bundle gate が必要で、古い build は新 bundle version を拒否する。
additive SQL でも普遍的な downgrade は保証しない。
検証した export/recovery path と論理 record を残し、rollback 成功を装う履歴 marker の書き換えはしない。

rollback 条件は再開 event の欠落、refinement coverage の破損、process outcome の変更、wrong-store replay、移行なしの非互換 public output とする。
runtime uptime や raw transcript ではなく、限定した sanitized delivery/query failure を監視する。
Workspace replay 固定の gap は全段階を通じて別 scope に残る。

## 未決定の実装判断とセルフレビュー

- latest-relevant ranking、source namespace/collision、context output 移行、public list/status consumer の詳細。
- explicit-end deprecation の日程と marker/output 互換の詳細。
- 各 host の flush/cleanup と有用 Interrupt/StopFailure の保持根拠。既存 ledger が不十分な場合の最小 receipt storage。
- 専用 run SID binding、atomic active-execution protection、typed compatibility adapter の限界。shared-session invocation identity/storage は別の将来判断。
- opt-in orphan 条件、reference/lineage/spool 保護、別途承認する retention scope。
- rollback switch の要否と範囲。Invocation storage 導入時の versioned recovery。

モデルは Session lifecycle ownership を runtime episode に移すのではなく除去する。
execution の責務、有用な audit fact、identity routing、refinement coverage は維持する。
テスト案は内部の呼び出し順ではなく content、outcome、互換性を観測する。
この文書 PR は schema 変更、source test、live-host 検証を行ったとは主張しない。

## 参照

- [実装計画（作業単位の提案）](../plans/2026-09-27-log-only-session-migration.ja.md)

- [Issue #2394](https://github.com/duck8823/traceary/issues/2394)
- [限定した受動取得 #2393](https://github.com/duck8823/traceary/issues/2393)
- [アーキテクチャ原則](../architecture/README.ja.md)
- [Event lifecycle（現在の動作）](../lifecycle.ja.md)
- [Storage model（現在の動作）](../storage/README.ja.md)
