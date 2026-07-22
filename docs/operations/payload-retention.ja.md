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

class ごとの plan 結果は `satisfied`、`unsatisfied`、`indeterminate` のいずれかです。byte-only ceiling に unknown measurement がある場合は `indeterminate` であり、`satisfied` にはしません。`unsatisfied` は hold/recovery floor により既知 ceiling を満たせない状態です。

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

canonical hash は RFC 8785 JSON canonicalization を使います。set semantics の array は class、stable DB identity、root ID、relative path、timestamp、reason で並べてから canonicalize します。時刻は UTC RFC3339Nano、duration は整数 nanoseconds、byte/count は base-10 JSON integer、relative path は `/` separator で `.`/`..` を禁止します。`plan_id` と presentation-only message は hash 対象外です。SHA-256 は誤変更検知であり真正性署名ではありません。CLI confirmation は plan ID と exact phase を表示します。

## Apply / interruption / retry

hash、version、DB identity、SQLite `user_version`、migration digest、root、recovery prerequisite が異なる plan は拒否します。body pruning の各候補は eligibility を確定する field の compare-and-set fingerprint も持ちます。新規 hold、変更済み、既に prune 済みの identity があれば stale-plan error とし、review 済み batch を部分適用せず fail-closed にします。hold row、recovery pin、candidate fingerprint、execution-ledger の unique transition は同じ SQLite write transaction で検査します。commit 前に hold が追加されたら body 削除は zero です。

Body pruning は plan batch ごとの bounded transaction です。execution ledger は plan/phase/batch outcome を同一 DB transaction に記録します。完了済み batch の再実行は冪等 no-op です。commit 前の中断は変更なし、commit 後の中断は ledger から検出します。

File cleanup は directory handle を使い、symlink を拒否し、path を再解決せず device/inode/size/mtime + digest を直前照合します。同じ directory 内の tombstone へ rename、directory fsync、`tombstoned` 記録、同じ handle から unlink、再 fsync、`committed` 記録の順です。durable state machine（`pending`, `running`, `tombstoned`, `committed`, `conflicted`, `failed`）により全 crash point を retry できます。matching tombstone は unlink を再開し、original replacement は conflict、committed は no-op です。

Compaction は暗黙実行しません。in-place 変更でなく新しい `VACUUM INTO` DB を作り、exclusive writer lease、WAL checkpoint/sidecar 処理、source + destination + safety margin の空き容量確認、destination `integrity_check`、file/directory fsync、restore protocol による swap/reopen を必須にします。中断時は original DB が authoritative のままです。

## Recovery / rollback

Raw-body apply には、対象 identity を含む verified archive/backup が必要です。body transaction 直前に再 verify し、その transaction 内で pin します。明示的な将来 policy `discard_without_recovery` は v0.31 の対象外です。restore は primary-key/idempotency を考慮し、restored、skipped-identical、conflicting content を区別します。

archive/backup class を横断する一つの `RecoveryCatalog` が pin と retention floor を所有します。recovery generation は source DB fingerprint + coverage manifest です。各 destructive execution は exact identity を復元できる recovery point を `rollback_until` 経過と明示 release まで pin します。rotation は catalog lease/CAS で直列化し、budget は pin を削除できません。現在 live generation の最新 verified point も保持します。verified point が zero なら v0.31 は候補を報告するだけで archive/backup を削除しません。unverified potential file は recovery floor ではありません。

`verified` は root confinement、regular-file identity、SHA-256、対応 format、coverage manifest、暗号化時の named env からの復号鍵利用、decode/SQLite `integrity_check`、同一 digest の copied-store restore rehearsal を要求します。digest だけでは不足です。pin/rotation は mutable path でなく digest と coverage generation を使います。

Rollback は既定 disabled の自動実行を停止し、verified recovery point/archive を copied DB に restore・検証した後、強化した restore contract で live DB を置換することです。現行 `VACUUM INTO` backup と staged-rename restore は出発点であり、v0.31 execution の十分な証拠ではありません。retention が使う前に、exclusive cross-process writer lease、WAL validate/checkpoint、same-directory temporary copy、file/directory fsync、`integrity_check`、DB/WAL/SHM の一括 stage、atomic rename、exact snapshot path reopen、失敗時 staged generation rollback を必須にします。replace 中に open writer を残しません。加算的 schema/ledger table は旧 binary が無視でき、down migration で削除 body を再構築しません。

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

## TDD / split PR plan

1. #1446: この contract だけを merge。schema/behavior は変更しない。
2. #1444 internal A: value object、additive schema、read-only planner、plan CLI、dry-run side-effect test。
3. #1444 internal B: recovery verification/restore test、recovery catalog/pin、execution ledger。
4. #1444 internal C: internal body executor。apply はまだ公開しない。
5. #1443: root confinement、catalog CAS、pin floor test を持つ archive/backup inventory/rotation executor。recovery drill 前に public apply を公開しない。
6. #1445: copied-store interruption/retry/restore/rollback drill、doctor/status、その後に opt-in public apply と default decision。

observable behavior の failing test から始め、最小の domain/application boundary、SQLite/filesystem adapter、CLI の順で実装します。handler call order と private helper は test contract にしません。

## Default decision checkpoint

v0.31 の retention execution は既定 disabled です。copied-store dogfood で recovery、bounded runtime、許容できる unknown-byte rate、安定した metadata/aggregate output、release-blocking finding なしを確認するまで、後続 release でも default 変更をしません。private body を external storage に upload しません。
