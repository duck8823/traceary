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
| `RetentionPlan` | version、plan ID、snapshot、source fingerprint、policy、candidate | review 用の immutable artifact | canonical plan content の hash を ID とし、apply 時に再 plan しない |
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

Allocated bytes は選択 payload/file の占有量推定であり、filesystem が直ちに回収する保証ではありません。SQLite row pruning は logical reclaimable bytes を報告し、物理回収には独立した compaction phase が必要です。

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

- `schema_version` と canonical `plan_id`
- `created_at` と一つの UTC `snapshot_at`
- sanitize 済み source identity: DB file identity、SQLite `user_version`、migration-set digest、payload content を含まない file/root fingerprint
- effective policy と全 ceiling
- class ごとの current/candidate/excluded count・extent と byte availability
- 正確な DB identity または canonical local path、timestamp、reason、必要 recovery point
- body pruning、archive cleanup、backup rotation、compaction の独立 phase
- unknown extent や recovery coverage 不足などの warning

plan JSON に event body、command input/output、archive passphrase、credential、file content を含めません。

## Apply / interruption / retry

hash、version、DB identity、SQLite `user_version`、migration digest、root、recovery prerequisite が異なる plan は拒否します。body pruning の各候補は eligibility を確定する field の compare-and-set fingerprint も持ちます。新規 hold、変更済み、既に prune 済みの identity があれば stale-plan error とし、review 済み batch を部分適用せず fail-closed にします。

Body pruning は plan batch ごとの bounded transaction です。execution ledger は plan/phase/batch outcome を同一 DB transaction に記録します。完了済み batch の再実行は冪等 no-op です。commit 前の中断は変更なし、commit 後の中断は ledger から検出します。

File cleanup は same-directory atomic marker と exact file identity を使います。削除直前に retained recovery point を再検証します。候補が既にない場合、recorded identity が review 済み file だったことを証明できるときだけ already absent とします。同じ path の置換は conflict です。

Compaction は暗黙実行しません。DB integrity と代表 metadata/full-body query の成功後に実行し、専用 recovery point を作成/検証し、logical pruning と分けて allocated-size delta を報告します。

## Recovery / rollback

Raw-body apply には、対象 identity を含む verified archive/backup が必要です。明示的な将来 policy `discard_without_recovery` は v0.31 の対象外です。restore は primary-key/idempotency を考慮し、restored、skipped-identical、conflicting content を区別します。

Archive/backup rotation は live DB generation に対する最新の verified recovery point を最低一つ残します。age/count/byte budget はこの floor を上書きできません。verified recovery point がなければ候補を報告できますが、最後の potential recovery file は削除できません。

Rollback は既定 disabled の自動実行を停止し、verified recovery point/archive を copied DB に restore・検証した後、既存 backup/restore 契約だけで live DB を atomic に置換することです。加算的 schema と execution ledger table は旧 binary へ戻しても残せます。旧 binary は無視します。down migration で削除 body を再構築しません。

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

## TDD / split PR plan

1. #1446: この contract だけを merge。schema/behavior は変更しない。
2. #1444: retention value object、加算的な body extent/availability と execution ledger migration、body planner/apply port、SQLite implementation、CLI plan/apply、behavior test。default は disabled のまま。
3. #1443: root confinement と recovery-point floor を持つ archive/backup inventory・rotation plan。
4. #1445: doctor/status、copied-store の interruption/retry/restore/rollback 証跡、最終 opt-in/default decision。

observable behavior の failing test から始め、最小の domain/application boundary、SQLite/filesystem adapter、CLI の順で実装します。handler call order と private helper は test contract にしません。

## Default decision checkpoint

v0.31 の retention execution は既定 disabled です。copied-store dogfood で recovery、bounded runtime、許容できる unknown-byte rate、安定した metadata/aggregate output、release-blocking finding なしを確認するまで、後続 release でも default 変更をしません。private body を external storage に upload しません。
