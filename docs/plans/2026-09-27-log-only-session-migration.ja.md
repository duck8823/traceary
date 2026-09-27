# Log-only Session 移行の実装計画

[English](./2026-09-27-log-only-session-migration.md)

- Status: Proposed work packages。日程化と受け入れ条件の準備済みであり、実装開始可能ではない。
- Date: 2026-09-27
- Design: [Session identity ADR](../adr/2026-09-27-resumable-session-boundaries.ja.md)
- Design issue: [#2394](https://github.com/duck8823/traceary/issues/2394)
- Design PR: [#2398](https://github.com/duck8823/traceary/pull/2398)。`Refs #2394` を使い、closing reference ではない。
- Baseline: ADR commit `3df81e1881fcdfb77c3417e0d95f0a73a0a065a1`。

## 権限と実装 checkpoint

承認済みの方向性は、Session を lifecycle 管理ではなく log grouping identity とすることである。
以下の P1–P4 は作業単位の仮 ID であり、作成済み GitHub issue ではない。
前提 checkpoint 後に、各作業単位へ一つの ticket、専用 branch、PR を対応させる。
将来の issue body には title だけでなく、具体的な肯定/否定の受け入れ条件、ownership、依存、検証 gate を含める。
architecture の Ready/merge 判断は人間が GitHub UI で行う。
この計画成果物自体は runtime、schema、database、runtime ticket/PR の操作を許可せず、設計文書の公開は別途 main が所有する。

実装前にメンテナーが次を承認する。

- public handoff/JSON downstream への影響と、実在する flag、version 境界を含む flag 移行 policy。
- supervisor provenance の厳密な認識方法と atomic writer guard 契約。保存済み success reason だけでは実際の outcome を証明しない。
- closure 依存 consumer、local end marker、対応する host callback 契約と parent inference 制約。
- 明示 start/end marker、提供 summary/coverage、warning/return、internal API、legacy CLI alias の契約。
- isolated dogfood/recovery evidence の scope と、利用不能 host の縮退判断。

この計画は順序と受け入れ条件を用意するが、上の詳細 audit は実装開始をまだ阻む。
条件付き自律進行には検証済み review finding の解消と gate fact の確認が必要で、人間の UI design checkpoint を解除しない。gate が満たされるまで計画は Proposed、design PR は draft とし、main は issue body を準備できるが gated source work は開始しない。
最後に提供された観測では `3df81e18` の design CI は in progress であり、この文書は CI 成功を主張しない。

## 範囲と順序

```text
checkpoint -> P1 (read consumer 移行)
           -> P2 (execution guard と legacy result adapter)
           -> P3 (通常 producer/consumer の atomic 切替)
           -> P4 (統合検証、dogfood、recovery)
```

session、hook、CLI、SQLite source が重なるため、P1 → P2 → P3 → P4 を直列化する。
readonly fixture inventory と独立レビューは実装と並行可能だが、writer の scope を重複させない。
S/M/L は相対的な工数であり、日付の約束ではない。

| Package | Size | Risk | merge の前提 |
| --- | --- | --- | --- |
| P1 | M | HIGH: public context/handoff output | Public-contract checkpoint |
| P2 | L | HIGH: execution outcome と repository guard | P1 と provenance/writer-path audit |
| P3 | L | HIGH: 複数 host と public lifecycle 切替 | P2 guard の検証、consumer/host checkpoint |
| P4 | M | evidence/docs は MEDIUM。cross-version behavior 変更が必要なら HIGH | 統合済み P1–P3 head |

対象外は runtime monitoring、shared-conversation Invocation ID/table、lifecycle column の物理削除、retention purge、汎用 context assembly/token-budget framework、install/login、本番 config 変更、release publication とする。
取得済み SID/store routing を維持し、解決済み Workspace 全体の replay 固定は別の既知 gap とする。

## P1: Context と handoff の lifecycle gate を除去する

**Ownership:** `presentation/cli/` の context/handoff consumer、関連する `application/queryservice/` と `application/usecase/` の query/build 経路、既存 test、利用者向け文書。
実在する入口には `presentation/cli/handoff.go` と `presentation/cli/context_command.go` があり、`presentation/cli/output.go` は DTO 定義を含む。
この作業単位では parent selection が共有する `Active` の意味を変更しない。

**作業:**

- stale-start 拒否、handoff stale re-query、生成 `STATUS` と close 案内を調べ、lifecycle eligibility と scaffold を除去する。text の `STATUS` 行は空欄で残さず削除する。
- downstream/JSON consumer の実際の公開範囲または不在を調べ、存在しない現在の context JSON 契約を捏造しない。
- latest selection の scope/order/tie fallback、既存 field、compact/refinement、human prose、対応する明示 filter を維持する。
- `MemoryAsOf` は memory のみに適用し、recent-command/memory-count limit、記録済み coverage の意味を維持する。event-as-of や汎用未 coverage event stream は追加しない。
- 未使用 `sessionSummaryOutput` と `SessionStatus` consumer を調べてから除去し、content aggregate/coverage の summary type は維持する。
- 実在する handoff/compact-only の `--stale-after`/`--allow-stale` は、文書化した一移行期間 deprecated no-op として保持し、flags-require-handoff/compact validation は維持する。version/除去境界は checkpoint で承認する。
- deprecated flag が明示された場合のみ stderr に warning を出し、stdout JSON/ID を汚さない。

**肯定の受け入れ条件:** 古い unended/legacy ended の関連 content を明示 lookup できる。old-unended/new-ended が混在する暗黙 latest で、既存の決定的な順序を維持する。文書化した output 移行として生成 lifecycle scaffold が消える。
**否定の受け入れ条件:** 代わりの running/availability status、user summary の書き換え、登録のみの record より新 content を優先する ranking 変更、parent inference 拡大を行わない。

**検証:** 影響する `presentation/cli`、`application/queryservice`、`application/usecase` test。handoff golden/JSON consumer、stale-start fixture、compact/refinement、MemoryAsOf regression。
**rollback 条件:** 選択 content の欠落、未承認の downstream output 破壊。
**recovery:** 互換 storage と fixture export を保持する。read を戻すと stale 制限も戻るため、透明な rollback とせず明示的な運用判断を必要とする。

## P2: Supervisor outcome を保護し、legacy execution result を保持する

**Ownership:** `presentation/cli/`、`application/usecase/`、`domain/model/` の session execution/finalization、repository contract、`infrastructure/sqlite/`。guard に必要な範囲のみ hook/doctor caller も対象。
通常 Session の closure はまだ除去しない。

**作業:**

- confirmed-supervisor projection を定義する前に、すべての result writer と根拠の取得元を調べる。通常の manual End も CLI/empty-source 属性を持ち得るため、それだけを信頼できる証明にしない。
- transactional guard site として通常 End repository write、stale GC/doctor SQL、cascade FindOpenChild/domain guard、BundleConflictReplace を含む bundle ImportSession を調べる。env-only の nested one-shot skip は replay 後に不十分であり、架空の actor SQL field を指定しない。
- GC/doctor、parent cascade、host/直接 end、outcome を変える import/replay から one-shot row を atomic に保護する。現在 result を書けるのは supervisor `FinalizeOneShot` のみとする。
- one-shot への直接 `session end` は対処方法のある non-success を返し、log/outcome を変更しない。
- run ごとの専用新 SID、不変 wrapper binding、既存 finalization/first-result reconciliation、exit code、cancellation、timeout、signal、usage、固定 routing を維持する。
- 同じ result の retry は冪等とし、矛盾する result で最初の record を上書きしない。
- 過去の raw mode/end/reason を保持する。one-shot mode は run を示すが、result を書いた actor は示さない。
- 信頼できる provenance のみで実際の supervisor outcome を確認する。早期 host/cascade success 後に failure を試みた履歴は、根拠不足なら actual outcome unknown の記録 metadata とする。
- scope を限定した legacy import は過去記録を復元できるが、現在 result を上書きせず provenance を捏造しない。backfill や時刻 heuristic は使わない。
- 新しい `cli:session-finalize` outcome-text content refinement を止め、過去の生成 refinement と user summary を維持する。

**肯定の受け入れ条件:** 専用 run の retry と後の同一 SID log が動く。過去 raw value は export 可能。並行 race でも supervisor process の振る舞いと記録 outcome を維持する。
**否定の受け入れ条件:** interactive success/曖昧な one-shot reason から成功 run を推測しない。新 Invocation table/ID、履歴書き換え、cleanup による result 欠落を行わない。

**検証:** use-case/domain/SQLite と CLI supervisor test。並行 finalization、child-parent end、直接 end、GC/doctor、import/replay、早期 legacy success と矛盾 failure の fixture。
**rollback 条件:** process exit/outcome の変更、guard の迂回、過去 value の欠落、result/log conflict。
**recovery:** 現在の typed compatibility column と専用 wrapper binding を保持する。isolated copy、または承認済み手順で危険な旧 writer を freeze した状態でのみ復元する。
guard 移行の atomic 性が証明されるまで P3 に進めない。

## P3: 通常 Session closure を log-only boundary capture に置換する

**Ownership:** 通常 session domain/use case、SQLite query/update consumer、public session CLI、doctor、`presentation/cli/` と integration package の実際に対応する全 host adapter/asset、関連 docs/test。
P2 の execution protection/finalization を維持する。

**atomic な前提と作業:**

- parent inference、`Active`、`List ActiveOnly`、`FindEndedSessionIDs`、doctor stale diagnostic/fix、local end marker、closure writer を切替前または同時に移行する。不要になった doctor stale lifecycle WARN/fix は除去し、不要な monitoring を追加しない。
- `presentation/cli/session_resolution.go` の log/audit implicit destination も P1 ではなく atomic consumer cutover に含める。明示 SID は保持し、暗黙 lookup は lifetime に依存せず既存 workspace/scope/order で関連する最新の記録 grouping identity を選ぶ。該当がなければ既存 default fallback を維持する。metadata/attribution を暗黙変更せず、docs/help は active/stale から recorded lookup の説明に更新する。
- `presentation/cli/hook_subagent.go` の SubagentStop と Kimi spool replay も同時に切り替える。現在の lazy StartChild と End/extract は、child を terminalize しない marker-only stop にする。明示 lineage、根拠のある lazy registration、completion fact、extract、cleanup、scoped receipt を維持する。
- unended record の増加で推定 parent を広げない。根拠のある spawn/lineage のみ受理し、不十分なら unknown として近い active/unended Session を選ばない。
- doctor fix を synthetic stale-close writer として残さず、GC は `endedAt` を生成しない。
- 同じ ID/metadata の `session start` は exit 0/既存 ID を返し、start record を増やさない案とする。client/agent/workspace/parent が衝突する場合は actionable error で fail closed とし、上書きや warning-only success にしない。internal API/alias の詳細は checkpoint で承認する。
- 通常 `session end` の目標は明示 marker と提供 summary/refinement coverage。terminal gate/cascade は行わない。別の明示 invocation ごとに新 marker を記録し、実際の同じ delivery receipt retry のみ冪等とする案を推奨する。SessionID だけで全 boundary occurrence をまとめない。可能な範囲で output ID/flag を維持し、return/warning/dedup 移行を先に承認する。
- marker を消さない既存 start と Stop stale-marker cleanup を含む local semantic end gate を除去し、scoped receipt dedup は維持する。
- Claude、Codex、Gemini、Grok、Kimi、Muse、Antigravity を実際に対応する callback で個別検証し、不在の hook を捏造しない。
- 有用 usage/transcript flush、redaction、取得 SID/固定 store routing、receipt、cleanup を維持する。
- 空の推測 close content note の既定生成を止め、正しい scope の有用 Interrupt/StopFailure outcome と過去 boundary event を維持する。

**肯定の受け入れ条件:** 旧 explicit/GC marker 後の同一 ID resume が append/retrieve できる。resume → end/Stop が安全に flush/cleanup する。summary 付き end は coverage を保つ。sibling や新しい無関係 record を parent に推測しない。
destination fixture は old ended/unended/stale、明示 SID、candidate 不在を扱い、attribution/fallback を維持する。subagent-stop 後の late log を query でき、重複 stop と Kimi replay の extract は provenance と冪等性を守り、terminalization/result override を行わない。start fixture は同じ metadata の成功と衝突拒否を扱い、explicit-end fixture は新 invocation と receipt retry を区別する。
**否定の受け入れ条件:** 通常の再帰 terminalization、stale closure、非空 log purge、schema DROP、dedup 全無効化を行わない。supervisor の subprocess cancellation を除去しない。

**検証:** 対応する各 host contract の fixture、CLI start/end 契約、query/domain/SQLite、parent lineage、hook-local state、spool/replay、transcript/usage、redaction test。
**rollback 条件:** log/flush の欠落、wrong parent/store、guard regression、consumer/writer の意味の混在。
**recovery:** legacy wire/storage と boundary history を保持し、isolated copy または明示承認した freeze 手順を使う。旧 GC/doctor は新 unended record を大量 close し、旧 reader は stale content を拒否し得る。
producer のみの部分 rollback/cutover は許容しない。

## P4: 移行と isolated recovery を検証する

**Ownership:** migration/recovery docs、controlled fixture/evidence 定義、既存 test/wave tooling。発見した runtime fix は分類して担当 package scope に戻し、未レビューの付随変更にしない。

evidence/fixture の準備は readonly 作業として先に開始できるが、P4 完了と dogfood は merge 済み統合 runtime head に結び付ける。

**作業:**

- legacy column、boundary event、raw execution metadata、refinement coverage を bundle export/import で確認する。古い build は新 schema manifest を拒否する。
- 普遍的な roundtrip/downgrade を保証せず、旧 reader 制限と GC/doctor mutation risk を文書化する。
- bounded dry-run と isolated recovery copy の証跡を用意する。status 変更で retention 削除を有効化しない。
- この文書 head ではなく merge 済み統合 runtime head で、release 前に controlled dogfood を行う。
- DB/store、`TRACEARY_HOOK_STATE_DIR`、spool/queue/receipt、GC marker、lease、diagnostic、usage offset を該当する範囲で隔離し、実在する override が有効なことを先に確認する。
- 認証と本番 host config は触らず、sanitized controlled session を使い、raw private log を使わない。
- SHA、UTC 時刻、provider CLI version、controlled scenario、検証 command、sanitized result を記録する。

七つの host すべてについて capability/evidence matrix を記録し、fixture/live を分けて `PASS`、`FAIL`、`NOT_RUN`、実際の hook support、理由を示す。
未対応 hook は架空 callback の実装失敗ではなく、pass にもならない。
利用不能 host の縮退はメンテナーが明示的に判断する。
必須 acceptance が未検証なら release を停止する。

**肯定の受け入れ条件:** 統合 runtime test、fresh CI、wave evidence、必須 controlled scenario、isolated recovery が対応 SHA で成功する。
**否定の受け入れ条件:** install/auth/本番 mutation、release publish、未確認 override による実 store 変更を行わず、docs-only check を runtime validation と数えない。
**recovery:** 隔離失敗で dogfood を停止し、sanitized evidence を保存して isolated copy から復元する。本番 mutation で復旧しない。

## 検証 gate と完了証跡

実装 commit では `scripts/test-select-staged.sh` で影響 test を選び、適用する lint/typecheck/test と、文書変更時の docs check を実行する。
各 integrated runtime wave では次を実行する。

```sh
scripts/run-wave-e2e.sh --wave ID --ref SHA --project-dir WORKTREE --evidence-dir DIR
```

wave log を統合 SHA に結び付け、未実行/失敗 evidence を成功と扱わない。
必須 fresh CI と独立レビューは維持し、同一 input を証明できる場合のみ検証証跡を再利用する。
review finding は main が triage し、一律自動修正しない。
P4 の完了条件は必須 scenario の実証跡と縮退 host 判断であり、経過時間目標ではない。
この計画変更は文書検証のみを実行し、source/runtime/host test や release gate の成功を主張しない。

## 参照

- [Session identity ADR](../adr/2026-09-27-resumable-session-boundaries.ja.md)
- [段階的な検証戦略](../../CONTRIBUTING.md#staged-test-strategy)
- [Storage model](../storage/README.ja.md)
