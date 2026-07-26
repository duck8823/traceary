# list/context クエリ用の永続メタデータ投影

[English](index-backed-metadata-list.md)

## Structure-Behavior 設計メモ

### 要求

- 一般的なメタデータ list/context 読取は、RFC3339Nano の境界を正しく
  並べつつ、本文を持つ `events` レコードを開かない。
- workspace/session/source-hook 絞り込み、offset ページ、keyset ページの
  対象件数と順序を維持する。
- 過去互換の2種類の source-hook 本文prefix判定は維持するが、読取時には
  本文を評価しない。
- スキーマ更新は追加だけで行い、アプリケーションを戻した後も更新済み
  ストアを読み書きできる。

### 概念と責務

| 概念 | 責務 | 不変条件 |
| --- | --- | --- |
| 正規化時刻 | migration 000031 と event trigger | `created_at_norm, id` を event 順序の唯一の契約にする |
| 永続メタデータ投影 | migration 000034 | eventごとにmetadata、過去互換hook分類、任意のcommand-audit属性を1行保持する。本文やcommand payloadは保持しない |
| 絞り込み済みmetadata page | metadata datasource | list/context SQLは投影だけを開き、既存の`EventMetadata`契約を返す |
| transaction内の同期 | event/command-audit trigger | authoritative rowのcommit結果と投影を乖離させない |

### 対応するクエリ計画

| クエリ形状 | 投影の順序インデックス |
| --- | --- |
| 一般metadata list（limit/offsetを含む） | `idx_event_metadata_created_at_norm_id_desc` |
| workspace list/context | `idx_event_metadata_workspace_created_at_norm_id_desc` |
| session list/context | `idx_event_metadata_session_created_at_norm_id_desc` |
| workspace + session list/context | `idx_event_metadata_workspace_session_created_at_norm_id_desc` |
| 明示source-hook list | `idx_event_metadata_source_hook_created_at_norm_id_desc` |

時刻境界が指定された場合は直接predicateのSQLを選び、対象の順序index内を
seekする。scope以外のfilterはseek後に適用し、公開済みの結果を変えない。
過去互換hookはmigration時またはevent書込み時に固定分類へ変換する。
metadata読取はその分類だけを比較し、event本文を評価しない。

### 振る舞いテスト

1. 000034適用前ストアのprivate copyだけを更新し、source digest不変、
   event/投影件数一致、`integrity_check=ok`、外部キー違反0を確認する。
2. 既存rowのbackfillと、event/auditのinsert/update/deleteが同じtransaction
   で投影へ反映されることを確認する。
3. 代表的なlist/context SQLとplanが投影だけを使い、`events`を開かず、
   ORDER BY用の一時treeを作らないことを確認する。
4. 時刻順、filter、失敗のみの絞り込み、過去互換hook、offset、
   composite keysetの意味を維持する。
5. 10k eventの直接range queryをCI smokeとしてp95 50 ms未満に保つ。
6. 外部write lock中はbusy/lockedとなり、投影objectが部分作成されない。
   lock解除後のretryは成功する。
7. 更新済みストアを投影導入前のmigration setで開き、その後のevent書込みも
   永続triggerで投影へ反映されることを確認する。
8. release前に2つのopt-in運用benchmarkを実行する。残す要約は数値と固定
   booleanだけに限定する。

### 性能とmigration証跡

2026-07-26にGo 1.26.3、macOS 26.5（darwin/arm64、Apple M4）、
modernc SQLite driverでCI smokeを測定した。投影済みevent 10,000件に対し、
workspaceを絞った2秒間の直接range、`limit=50`、25回反復のp95は
**412.25 µs**で、50 msの目標を満たした。

copied-store migration benchmarkは、privateな256 MiBのsynthetic body領域を
持つ000034適用前sourceを複製し、copyだけを更新する。

```sh
TRACEARY_RUN_METADATA_PROJECTION_MIGRATION_BENCHMARK=1 \
  go test -v ./infrastructure/sqlite -run '^$' \
  -bench BenchmarkEventMetadataProjectionCopiedStoreMigration -benchtime=1x
```

同じhostでは8 eventのmigration 34が**309.6 ms**で完了した。
source、更新前copy、更新後copyはすべて302,714,880 bytesで、既存の空きpageに
細い投影が収まったためmain fileの増加量は0 bytesだった。scratchのpeakは
605,528,480 bytes、checkpoint後は605,429,760 bytesだった。integrity成功、
外部キー違反0、sourceはbyte単位で不変だった。外部write lock中は
**1,012.467 ms**後に想定どおりbusyとなり、部分作成objectは0件、
lock解除後のretryは成功した。

Phase-A benchmarkはSQLiteが生成する256 MiB領域を8件作り、managed/stored
body bytesがともに2 GiB以上であること、event/投影件数一致、投影だけを使う
順序planを確認してから、直接metadata rangeを25回測定する。

```sh
TRACEARY_RUN_MULTI_GIB_BENCHMARK=1 \
  go test -v ./infrastructure/sqlite -run '^$' \
  -bench BenchmarkMetadataDirectRangeMultiGiB -benchtime=1x
```

出力はmanaged/stored bytes、event/投影件数、body metadata欠落数、
返却body bytes、plan分類、反復回数、p95だけである。p95基準は250 ms未満。
fixtureはGo testの一時directoryだけに置き、終了後に削除する。CIでは実行
しない。

同じhostでのPhase Aはmanaged 2,418,753,536 bytes、stored body
2,147,483,648 bytes、event/投影各8件だった。25回すべてが投影だけを使う
planで、body metadata欠落と返却body bytesはいずれも0、p95は
**0.2921 ms**で250 ms基準を満たした。

### ロールバックと残リスク

000034は追加migrationである。旧binaryは追加table/indexを無視し、永続
triggerが旧binaryの書込みも投影へ反映する。release後はschema objectを
削除せず、applicationだけをauthoritative-table読取へ戻せる。投影を削除
する場合は、利用中のreaderがないことを確認した後、別のforward migration
で行う。

backfillとindex作成は1つのmigration transactionで実行する。巨大storeでは
一時容量が必要で、競合writerはbusy timeoutまで待つ可能性がある。失敗時は
table、index、trigger、migration recordをまとめてrollbackする。eventごとに
細い1 rowと5本の順序indexを追加するため、展開後もwrite latencyと
WAL/checkpoint増加を監視する。
