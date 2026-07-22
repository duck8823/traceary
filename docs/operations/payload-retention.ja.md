# Payload retention / capacity 契約

[English](./payload-retention.md)

Status: Issue #1446 の v0.31.0 design checkpoint。この文書は #1444、#1443、#1445 の契約を定義するだけで、削除を有効にしません。

## 要求要約

Traceary は、resume・監査に必要な session metadata と短期 debug 用の raw payload を同じ保持 class として扱わず、local storage の増加を制限します。plan は read-only、実行は review 済みの同一 plan を使い、install / update だけで retention を実行しません。

最初の実装は opt-in のままです。dogfood は copied store または synthetic store で行い、raw-body pruning、archive cleanup、backup rotation、SQLite compaction を別 phase にします。

## 概念モデル

| Concept | State | Behavior | Invariant |
|---|---|---|---|
| `RetentionClass` | `raw_body`, `metadata`, `aggregate`, `archive`, `backup` | class ごとに独立した budget と action を選ぶ | 無関係な class 経由で row/file を削除しない |
| `RetentionBudget` | 任意の age、count、logical bytes、allocated bytes | 設定済み上限を満たすまで候補を作る | 未指定・unknown・zero は別の値 |
| `RetentionHold` | retained、debug、legal/operational hold、理由、任意 expiry | identity を plan から除外する | 並び替えや byte 制限より先に評価する |
| `CapacityExtent` | known logical/allocated bytes または unknown | 現在量と回収見込みを報告する | unknown を数値 zero として出力しない |
| `RetentionCandidate` | class、正確な identity/path、timestamp、extent、reason | action の理由を説明する | 全候補に機械可読な reason が一つ以上ある |
| `RetentionPlan` | version、plan ID、snapshot、source fingerprint、policy、candidate | review 用の immutable artifact | `plan_id` を除く canonical content の hash を ID とし、apply 時に再 plan しない |
| `RecoveryPoint` | archive/backup path、digest、verified-at、対象 identity | destructive action を許可する | 未検証・破損 material は削除を許可しない |
| `RetentionExecution` | phase、outcome、interruption marker | phase を冪等適用する | prerequisite 失敗後に後続 phase を暗黙実行しない |

### Retention class

- `raw_body`: event body と command input/output。prune 後も event/session ID、timestamp、kind、source metadata、body extent、aggregate を保持します。retention で削除済みの body は、元から空または取得不能だった body と区別します。
- `metadata`: audit、filter、lineage、aggregate に必要な session/event identity と provenance。v0.31 では budget を定義しますが物理削除しません。
- `aggregate`: 派生 summary / persisted aggregate state。aggregate cleanup だけで primary row 削除を許可しません。
- `archive`: 検証済み Traceary archive package。rotation は live row pruning と独立です。
- `backup`: SQLite の処理前 backup など local recovery copy。rotation は最新の検証済み recovery point を残します。

## Budget semantics

各 class は次の任意の組合せを設定できます。

```json
{
  "max_age": "336h",
  "max_count": 20,
  "max_logical_bytes": 1073741824,
  "max_allocated_bytes": 2147483648
}
```

未指定上限は unlimited です。数値 zero は明示的な zero budget で、class/command が全削除を許す場合だけ有効です。unknown measurement の表現には使いません。measurement は `{ "availability": "known", "bytes": 0 }` または `{ "availability": "unknown" }` を使います。

候補は class 固有の age、stable identity、path で決定的に並べます。候補は複数 reason（`age`, `count`, `logical_bytes`, `allocated_bytes`）を持てますが、一度だけ出現します。byte ceiling は既知 extent だけを使います。unknown extent は表示し、回収可能と仮定しません。count/age rule からは候補にできます。

Allocated bytes は選択 payload/file の占有量推定であり、filesystem が直ちに回収する保証ではありません。SQLite row pruning は logical reclaimable bytes を報告し、物理回収には独立した compaction phase が必要です。SQLite の candidate 単位 allocated bytes は加算できないため、v0.31 の `raw_body` 候補選択には使いません。DB 全体の allocated bytes は compaction 前後の観測専用です。

設定済み ceiling は projected post-plan state に対して個別に `satisfied`、`unsatisfied`、`indeterminate` を評価します。class result は AND reduction です。どれかが `unsatisfied` なら class も `unsatisfied`、それ以外で一つでも `indeterminate` なら `indeterminate`、全 ceiling が `satisfied` の場合だけ `satisfied` です。age/count と併用していても、設定 byte ceiling の current measurement または candidate extent が unknown なら `indeterminate` です。allocated-byte measurement 非対応も `indeterminate` です。`indeterminate` / `unsatisfied` plan は apply できません。

| Class | Age | Count | Logical bytes | Allocated bytes | v0.31 の zero budget |
|---|---|---|---|---|---|
| `raw_body` | candidate selector | candidate selector | candidate selector | observation only | reject |
| `metadata` | report only | report only | report only | observation only | reject、executor なし |
| `aggregate` | report only | report only | report only | observation only | reject、executor なし |
| `archive` | file selector | file selector | known file-size selector | 対応環境の known file-block selector | reject |
| `backup` | file selector | file selector | known file-size selector | 対応環境の known file-block selector | reject |

## Hold / exclusion

plan は次を除外します。

- 明示 identity hold（`retain`, `debug`, または legal-hold 相当の operational hold）
- 設定 age 境界より新しい body
- 将来 policy が明示的に許可しない限り active / non-terminal session
- destructive phase に必要な最新の verified recovery point
- symlink 解決後に設定 archive/backup root 外となる file
- 未検証・破損・部分書込み archive/backup file を根拠とする削除

plan は raw body を出力せず、除外件数と reason を報告します。hold expiry は apply 時の wall clock ではなく plan snapshot に対して評価します。

## 責務

| Responsibility | Owner | Not owner |
|---|---|---|
| Budget / plan invariant | domain value object | Cobra handler / SQL query |
| Plan orchestration / prerequisite | application usecase | domain / presentation |
| Body candidate projection、exact update、byte measurement | SQLite adapter | usecase |
| Archive/backup root 列挙と file identity | filesystem adapter | domain |
| CLI input/output / confirmation | presentation/CLI | application rule |
| Archive verify/restore | 既存 archive usecase / codec | retention planner |
| Compaction | 明示 store maintenance adapter | body-prune execution |

planner は body を含まない candidate projection と file metadata を使います。usecase に SQL row、table 名、open file handle、raw private body を渡しません。

## Boundary / interface

`dryRun bool` ではなく intent ごとの port に分けます。

```text
RetentionPlanner.Plan(ctx, RetentionPolicy) -> RetentionPlan
RetentionExecutor.ApplyBodyPlan(ctx, ReviewedRetentionPlan) -> PhaseResult
RetentionExecutor.ApplyArchivePlan(ctx, ReviewedRetentionPlan) -> PhaseResult
RetentionExecutor.ApplyBackupPlan(ctx, ReviewedRetentionPlan) -> PhaseResult
StoreCompactor.Compact(ctx, ReviewedCompactionPlan) -> CompactionResult
RecoveryVerifier.Verify(ctx, RecoveryPoint) -> VerifiedRecoveryPoint
```

`ReviewedRetentionPlan` は、対応 version、canonical hash、source DB/file root、一致する destructive prerequisite を検証してからだけ作れます。planning は writer dependency を持たず、caller が返却 JSON を明示的に file redirect しない限り、backup、marker、archive、temporary plan file を作りません。

## Immutable plan format

JSON plan は次を含みます。

- `schema_version` と `plan_id` 自身を除いて計算する canonical `plan_id`
- `created_at` と一つの UTC `snapshot_at`
- sanitize 済み source identity: DB file identity、SQLite `user_version`、migration-set digest、payload content を含まない file/root fingerprint
- effective policy と全 ceiling
- class ごとの current/candidate/excluded count・extent と byte availability
- 正確な DB identity または root ID + normalized root-relative path、timestamp、reason、必要 recovery point
- body pruning、archive cleanup、backup rotation、compaction の独立 phase
- unknown extent や recovery coverage 不足などの warning

plan JSON に event body、command input/output、archive passphrase、credential、absolute home path、file content を含めません。

canonical hash は normative `canonical_payload` に RFC 8785 JSON canonicalization を適用します。top-level plan は `plan_id`、`canonical_payload`、任意 `display` だけです。apply は `canonical_payload` だけを解釈し、未知 canonical field を拒否し、`display` を無視します。`canonical_payload` は `schema_version`, `created_at`, `snapshot_at`, `source`, `policy`, `class_results`, `candidates`, `exclusions`, `recovery_requirements`, `phases` を含み、ほかの field を hash から除外しません。

任意 canonical field は省略し、場合によって `null` にしません。byte と nanosecond duration は leading zero のない unsigned base-10 string（zero は `"0"`）です。count は 0〜9,007,199,254,740,991 の JSON integer、時刻は UTC RFC3339Nano、relative path は `/` separator で `.`/`..`/empty segment を禁止します。

machine-readable schema は [`schema/retention-plan.schema.json`](../../schema/retention-plan.schema.json) です。required/optional field、type、`additionalProperties=false`、enum order を固定します。enum order は retention class `raw_body < metadata < aggregate < archive < backup`、ceiling/reason `age < count < logical_bytes < allocated_bytes < retain < debug < legal_hold < active_session < recovery_floor < unknown_extent < path_escape < unverified < stale`、phase `body_prune < archive_cleanup < backup_rotation < compaction`、status `satisfied < unsatisfied < indeterminate` です。

DB/file candidate は全 comparison field を持ちます。非該当値は missing key でなく empty string です。DB candidate は `root_id`/`relative_path` を empty、file candidate は `database_identity` を empty にします。set-like array の total order は class result=class enum、ceiling=ceiling enum、candidate=class/database identity/root ID/relative path/timestamp/candidate identity、reason/exclusion=reason enum/stable identity、recovery point=generation/digest/root ID/relative path、phase/batch=phase enum/decimal-string batch ordinal です。全 key 同順位なら element の RFC 8785 bytes を final key にします。順序に意味がある array は `ordered_steps` と命名し sort しません。

checked-in golden vector は [`retention-plan-canonical.golden.json`](./testdata/retention-plan-canonical.golden.json) と [`retention-plan.golden.json`](./testdata/retention-plan.golden.json) です。canonical fixture は末尾改行を含まない RFC 8785 の完全な byte sequence です。その SHA-256 digest が complete plan fixture の `plan_id` であり、全実装が両方に一致する必要があります。

SHA-256 は誤変更検知で真正性署名ではありません。`display` は実行判定から参照しないため変更しても execution に影響しません。CLI confirmation は plan ID と exact phase を表示します。

## Apply / interruption / retry

hash、version、DB identity、SQLite `user_version`、migration digest、root、recovery prerequisite が異なる plan は拒否します。body pruning の各候補は eligibility を確定する field の compare-and-set fingerprint も持ちます。新規 hold、変更済み、既に prune 済みの identity があれば stale-plan error とし、review 済み batch を部分適用せず fail-closed にします。hold row、recovery pin、candidate fingerprint、execution-ledger の unique transition は同じ SQLite write transaction で検査します。commit 前に hold が追加されたら body 削除は zero です。

Body pruning は plan batch ごとの bounded transaction です。execution ledger は plan/phase/batch outcome を同一 DB transaction に記録します。完了済み batch の再実行は冪等 no-op です。commit 前の中断は変更なし、commit 後の中断は ledger から検出します。

File cleanup は directory handle を使い、symlink を拒否し、path を再解決せず device/inode/size/mtime + digest を直前照合します。同じ directory 内の tombstone へ rename、directory fsync、`tombstoned` 記録、同じ handle から unlink、再 fsync、`committed` 記録の順です。durable state machine（`pending`, `running`, `tombstoned`, `committed`, `conflicted`, `failed`）により全 crash point を retry できます。matching tombstone は unlink を再開し、original replacement は conflict、committed は no-op です。

Recovery point は `active`, `deleting`, `deleted` state も持ちます。pin acquire と `active -> deleting` は同じ RecoveryCatalog CAS を使います。pin は `active` にだけ成功し、deletion reserve は pin count zero かつ protected floor reference なしの場合だけ成功します。rotation lease は CAS から tombstone rename、二回の directory fsync、unlink、catalog `deleted`、ledger commit まで保持します。`deleting` point を使おうとする body apply は body transaction 前に失敗して re-plan します。pin と競合した rotation は CAS 失敗です。crash recovery は lease/fencing token と exact tombstone identity を照合し、削除完了または verified original file を戻せた場合だけ `active` へ復帰します。

Compaction は暗黙実行しません。in-place 変更でなく新しい `VACUUM INTO` DB を作り、exclusive writer lease、source + destination + safety margin の空き容量確認、destination `integrity_check`、下記の recoverable single-DB replacement state machine を必須にします。中断・invalid な出力は破棄し authoritative にしません。

## Recovery / rollback

Raw-body apply には、対象 identity を含む verified archive/backup が必要です。body transaction 直前に再 verify し、その transaction 内で pin します。明示的な将来 policy `discard_without_recovery` は v0.31 の対象外です。restore は primary-key/idempotency を考慮し、restored、skipped-identical、conflicting content を区別します。

archive/backup class を横断する一つの `RecoveryCatalog` が pin と retention floor を所有します。recovery generation は source DB fingerprint + coverage manifest です。各 destructive execution は exact identity を復元できる recovery point を `rollback_until` 経過と明示 release まで pin します。rotation は catalog lease/CAS で直列化し、budget は pin を削除できません。現在 live generation の最新 verified point も保持します。verified point が zero なら v0.31 は候補を報告するだけで archive/backup を削除しません。unverified potential file は recovery floor ではありません。

`verified` は root confinement、regular-file identity、SHA-256、対応 format、coverage manifest、暗号化時の named env からの復号鍵利用、decode/SQLite `integrity_check`、同一 digest の copied-store restore rehearsal を要求します。digest だけでは不足です。pin/rotation は mutable path でなく digest と coverage generation を使います。

Rollback は既定 disabled の自動実行を停止し、verified recovery point/archive を copied DB に restore・検証した後、強化した restore contract で live DB を置換することです。現行 `VACUUM INTO` backup と staged-rename restore は出発点であり、v0.31 execution の十分な証拠ではありません。

replacement は exclusive cross-process writer lease/fencing token を取得し、新規 open を拒否し、WAL `TRUNCATE` checkpoint、全 Traceary connection close、writer zero と WAL/SHM sidecar 不存在を証明します。その後は single main DB file だけを扱います。restart 時は新しい lease/fencing token を取得し、古い token の operation は拒否します。

journal update は一つの persistence primitive を使います。monotonic sequence、fencing token、state、path、digest を含む完全 record を同じ directory の temporary file に書き、file fsync、journal への rename、directory fsync を行います。intent state の永続化後だけ namespace operation を実行できます。file rename 後は directory fsync、その後に completed state を永続化します。

1. `prepared`: candidate を sync、digest 一致、`integrity_check` 成功。canonical path は old live のまま。
2. `old_move_intent`: live DB を journal 指定 old generation へ rename する durable intent。
3. live を old へ rename、directory fsync、`old_staged` 永続化。
4. `new_move_intent`: candidate を canonical path へ rename する durable intent。
5. candidate を canonical へ rename、directory fsync、`new_placed` 永続化。
6. fencing token 下で exact canonical path を reopen、migration/read check と `integrity_check`、`verified` 永続化。
7. catalog/ledger commit + fsync、`committed` 永続化。rollback pin release 後だけ old generation を削除。

Recovery は roll-forward と rollback を暗黙に選びません。`prepared` で candidate の検証が済んでいるため、`old_staged` は常に roll-forward します。plan abort は `old_move_intent` より前に明示的な durable abort request を必要とします。配置後に canonical-new verification が失敗した場合は、`rollback_new_quarantine_intent`, `rollback_new_quarantined`, `rollback_old_restore_intent`, terminal `rolled_back` を使います。各 rename は同じ intent、rename、directory-fsync、completed-state の順です。rollback 済み execution は `prepared` へ戻りません。再試行には新しい candidate、plan、journal が必要です。

二つの rename を一つの atomic operation とは扱いません。recovery は次の完全な state/name matrix を使い、未掲載 combination は `conflicted` として削除しません。

| Journal state | Canonical | Old | Candidate | Recovery |
|---|---|---|---|---|
| `prepared` | old digest | absent | new digest | `old_move_intent` を永続化して続行 |
| `old_move_intent` | old digest | absent | new digest | old rename retry |
| `old_move_intent` | absent | old digest | new digest | namespace operation 完了、`old_staged` 永続化 |
| `old_staged` | absent | old digest | new digest | `new_move_intent` を永続化して続行 |
| `new_move_intent` | absent | old digest | new digest | new rename retry |
| `new_move_intent` | new digest | old digest | absent | namespace operation 完了、`new_placed` 永続化 |
| `new_placed` / `verified` | valid new digest | old digest | absent | verify/new 継続 |
| `new_placed` / `verified` | invalid new | old digest | absent | `rollback_new_quarantine_intent` を永続化 |
| `rollback_new_quarantine_intent` | new digest | old digest | absent | canonical new を journal 指定 quarantine へ rename、fsync、`rollback_new_quarantined` を永続化 |
| `rollback_new_quarantine_intent` | absent | old digest | absent、quarantine は new digest | 先行 rename 完了、`rollback_new_quarantined` を永続化 |
| `rollback_new_quarantined` | absent | old digest | absent、quarantine は new digest | `rollback_old_restore_intent` を永続化 |
| `rollback_old_restore_intent` | absent | old digest | absent、quarantine は new digest | old を canonical へ rename、fsync、`rolled_back` を永続化 |
| `rollback_old_restore_intent` | old digest | absent | absent、quarantine は new digest | 先行 rename 完了、`rolled_back` を永続化 |
| `rolled_back` | old digest | absent | absent、quarantine は new digest | terminal no-op、再試行には新 plan が必要 |
| `committed` | valid new digest | old/absent | absent | new 継続、old は pin または release 後 retire |

intent 後に canonical/old の両方が old digest、または unknown digest の path があれば推測せず conflict にします。WAL/SHM を multi-file unit として copy/rename しません。replace 中に open writer を残しません。加算的 schema/ledger は旧 binary が無視でき、down migration で削除 body を再構築しません。

candidate/byte drift、integrity failure、metadata projection 変化、aggregate mismatch、recovery verify 失敗、path escape、hold bypass、nominal plan 中の write を rollback trigger とします。

## Behavior test

| Given | When | Then | Level |
|---|---|---|---|
| unknown / zero extent | plan serialize | state を区別する | domain/application |
| hold 対象と適格 row | raw-body plan | hold row を reason 付き除外 | application/integration |
| metadata 同一の large body | review 済み body prune | metadata-only 出力は byte-equivalent、full-body は retention unavailable | CLI/MCP integration |
| plan 後に candidate 変更 | apply | batch 全体を stale として追加 prune なし | SQLite integration |
| commit 前後の process 中断 | retry | effective apply は zero または one | SQLite integration |
| corrupt recovery package | destructive phase | 削除拒否 | usecase/integration |
| archive file と symlink escape | rotation plan/apply | root 外を拒否 | filesystem integration |
| 全 backup を消す budget | rotate | 最新 verified recovery point を保持 | application/integration |
| compaction なしの pruning | size inspect | logical bytes は減り、allocated bytes は残り得る | dogfood |
| verify 後の explicit compaction | compact | integrity 成功と allocated delta を報告 | dogfood |
| plan command 前後の DB/WAL/root | compare | DB、sidecar、root、marker、temporary file は不変 | integration |
| unknown extent の byte-only budget | plan | `indeterminate` で apply 不可 | application/integration |
| plan 後/apply 中に hold 追加 | apply | transaction の body 削除は zero | SQLite integration |
| 二つの同時 rotation | apply | catalog CAS で直列化し verified floor が一つ以上残る | integration |
| body prune 後の rotation | rotate | pin 済み recovery point を explicit release 前に削除しない | integration |
| 全 selected identity | plan render | identity と全 reason を出力 | application/CLI |
| canonical golden plan | Go/fixture verifier で hash | canonical bytes と plan ID が完全一致 | domain/tooling |
| pin と `active -> deleting` 競合 | CAS を interleave | 一方だけ成功し pinned file は unlink されない | integration |
| file delete 全 state で crash | restart | wrong-path deletion なしで file/catalog/ledger が収束 | integration |
| DB swap 全 state で crash | restart | old または verified new の一方だけが canonical | SQLite integration |

## TDD / split PR plan

1. #1446: この contract だけを merge。schema/behavior は変更しない。
2. #1444 internal A: value object、additive schema、read-only planner、plan CLI、canonical golden vector、dry-run side-effect test。
3. #1444 internal B: recovery verification/restore test、recovery catalog/pin、execution ledger、DB swap 全 crash state。
4. #1444 internal C: internal body executor。apply はまだ公開しない。repository の 1 issue/1 PR rule により A/B/C は単一 #1444 PR 内の別 commit/review checkpoint とし、各 focused test 成功後だけ次へ進みます。
5. #1443: root confinement、catalog CAS、pin floor test を持つ archive/backup inventory/rotation executor。recovery drill 前に public apply を公開しない。
6. #1445: copied-store interruption/retry/restore/rollback drill、doctor/status、その後に opt-in public apply と default decision。

observable behavior の failing test から始め、最小の domain/application boundary、SQLite/filesystem adapter、CLI の順で実装します。handler call order と private helper は test contract にしません。

## Default decision checkpoint

v0.31 の retention execution は既定 disabled です。copied-store dogfood で recovery、bounded runtime、許容できる unknown-byte rate、安定した metadata/aggregate output、release-blocking finding なしを確認するまで、後続 release でも default 変更をしません。private body を external storage に upload しません。
