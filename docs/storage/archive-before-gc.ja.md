# Archive-before-GC 設計ノートとマニフェスト契約

[English](./archive-before-gc.md)

Epic #1309 · 設計スライス #1370 · 親カット #1360。

本ドキュメントは **Structure-Behavior Design Note** と **版付き archive manifest 契約**です。スライス 2 (#1371) が手動オペレータ経路を実装し、スライス 3 (#1372) が同じ application core を opt-in 自動スケジューラに再利用します。設計 PR 自体では破壊的 GC を有効化しません。

## 要求要約

| 項目 | 内容 |
|---|---|
| **目的** | マルチ GB の live SQLite を、履歴を黙って捨てずに縮小する。対象行を版付き・完全性検証済み（任意で暗号化）archive に export し、**検証後に限り** archive 済み identity だけを live DB から削除する。 |
| **現状** | `traceary store gc` は `--keep-days` に基づき hard delete。フルファイル backup と portable bundle はあるが、GC 対象 cold 行の stream archive + verify-before-delete ではない。 |
| **期待する振る舞い** | dry-run 優先の plan → stream export → manifest / digest 検証（密封時は decrypt 往復）→ 厳密 ID 削除 → 任意 VACUUM。restore は idempotent。失敗時 live DB は不変。 |
| **非対象** | Compressed VFS / ZIPVFS、ネットワーク退避、既定 retention の auto-archive 化、フル backup の置換、memory の auto-accept。 |

### v0.28.0 リリース姿勢

| スライス | Issue | 姿勢 |
|---|---|---|
| 1 設計 + manifest | #1370 | **リリースブロッキング**（本文書） |
| 2 手動 archive + restore | #1371 | **リリースブロッキング** |
| 3 opt-in 自動スケジューラ + doctor | #1372 | **v0.28.0 スコープ内**（スコープアウトしない）。**opt-in / fail-closed**、既定オンにしない |
| 4 dogfood + 決定ログ | #1373 | **リリースブロッキング**証拠 |

memory decay 系 GC (#1368/#1369) と削除経路の所有権を分離する: verify-after-archive 削除は #1371、candidate→expired は #1368。

## 概念モデル

| 概念 | 状態 | 振る舞い | 不変条件 |
|---|---|---|---|
| **Live store** | 通常 path の hot SQLite | hooks/CLI/MCP 書き込み | archive 途中の部分削除禁止。検証後のみ削除 |
| **Archive plan** | 計算済み・未書き込み | テーブルごとの主キー集合 | 同一 cutoff/target/clock で dry-run と apply が一致 |
| **Archive package** | 呼び出し側 path のファイル | envelope + payload + manifest | magic/version、digest 一致、ID 厳密 |
| **Manifest** | package 内 JSON v1 | schema・cutoff・件数・digest | seal 後不変 |
| **Cold record set** | 対象 target の GC 適格行 | export → delete | accepted memory を暗黙削除しない（既存 GC 規則に従う） |
| **Restore run** | package の import | PK 単位 insert-or-skip | 内容が異なる live 行は黙って上書きしない |
| **Retention mode** | config: 既定 `disabled` / `archive_then_gc` | 自動経路は opt-in のみ | パスフレーズは env **名**のみ。秘密は永続化しない |

## 責務表

| 責務 | 所有者 | 変更理由 | 非所有者 |
|---|---|---|---|
| 適格性（何が cold か） | 既存 GC target + keep-days | retention 方針 | Presentation |
| 行集合の stream export/import | application archive use case | 形式・バッチ | CLI フラグ構文 |
| Manifest 生成・検証 | application codec + 純粋 verifier | schema 版 | SQL 文字列のみ |
| 密封 seal/open | bundle 系 crypto 再利用 | パラメータ | doctor 文言 |
| 検証後の厳密 ID 削除 | StoreManagement / SQLite | ID リスト SQL | CLI |
| 自動 worker + lease | #1372 | スケジュール | 手動 CLI |
| オペレータ面 | presentation/cli | UX | domain 不変条件 |

## 境界 / IF

| 境界 | 消費者 | 隠蔽する詳細 | エラー契約 |
|---|---|---|---|
| `ArchivePlan` | CLI / worker | SQL 選択 | 不正 target → ユーザーエラー、空 plan → 成功 no-op |
| `ExportArchive` | CLI / worker | stream・一時ファイル rename | IO 失敗時 live DB 不変。最終 path に不完全 package を残さない |
| `VerifyArchive` | CLI、削除前、restore | decrypt + re-hash | fail-closed。検証失敗で削除しない |
| `DeleteArchived` | apply のみ | トランザクション複数表削除 | v1: 欠落 ID は hard fail（安全側） |
| `RestoreArchive` | CLI | idempotent insert | 内容衝突は skip+報告、1 件でも非ゼロ終了 (v1) |
| config `retention` (#1372) | doctor / worker | ファイル + env | 密封モードで env 欠落 → WARN、削除なし |

### 提案 CLI（#1371 規範。変更時は設計改訂）

```text
traceary store archive create --output PATH [--target …] [--keep-days N] [--dry-run] [--passphrase-env NAME]
traceary store archive verify --input PATH [--passphrase-env NAME]
traceary store archive restore --input PATH [--dry-run] [--passphrase-env NAME]
```

自動モード (#1372) は同じ use case を呼ぶ。SQL を再実装しない。

## Archive package 形式 (v1)

1. **任意の外枠 envelope**: magic `TRCARYAR` + version `1` +（密封時）salt/nonce/ciphertext。非密封時も magic+version+payload で manifest digest による完全性を持つ。
2. **内包 payload**: zstd または gzip 圧縮 tar（依存が無ければ gzip。#1371 でベンチ結果を PR に記録）。
3. **tar メンバ**: `manifest.json`、`tables/<table>.ndjson`、`tables/<table>.sha256`。

詳細フィールドと JSON Schema は英語版および [`archive-manifest.v1.schema.json`](./archive-manifest.v1.schema.json) を正とする。

**Identity 規則**

- export 順序: 表固定順、表内は PK 昇順。
- delete 順序: FK 安全な逆順。**export 時の ID 集合のみ**を使い、削除前に cutoff を取り直さない。
- `row_ids_sha256` はソート済み ID の改行連結の SHA-256。

## 削除前検証チェックリスト

1. ファイル存在・magic/version
2. 密封時: passphrase env で decrypt 成功
3. manifest が schema v1 に適合
4. 各 ndjson の digest 一致
5. 各行 parse・PK 一意・件数一致
6. `row_ids_sha256` 再計算一致
7. 任意: live 残存行との内容サンプル照合
8. 以上を通過後にのみ、厳密 ID 削除トランザクション

失敗時: abort、live DB 不変。

## クラッシュ窓

| 窓 | リスク | 緩和 |
|---|---|---|
| export 途中 | 不完全ファイル | `PATH.partial` + fsync + atomic rename |
| export 済・未 verify | 孤児 archive | 安全。verify 再実行可 |
| verify 後・delete 途中 | 部分削除 | 単一トランザクション |
| delete 後 VACUUM 失敗 | 領域未回収 | データ整合は維持、エラー報告 |
| 自動 worker 競合 | 二重 archive | DB 単位 lease + interval (#1372) |
| restore 衝突 | 黙上書き | v1: skip+報告 |

## レコード単位圧縮ベンチ計画

DB 全体の一括 zstd だけでは不十分。#1371 で次を記録する:

- 短文 event 1 万件
- 大きな command audit 1 千件
- dogfood 実機 multi-GB（#1373）
- 1 行 archive のフロアサイズ

## 振る舞いテスト / TDD

英語版の Behavior tests 表および TDD plan を正とする（#1371/#1372 実装時）。

## 設定スケッチ (#1372、既定 fail-closed)

```json
{
  "retention": {
    "mode": "disabled",
    "archive_then_gc": {
      "interval": "168h",
      "keep_days": 90,
      "target": "all",
      "output_dir": "~/.config/traceary/archives",
      "passphrase_env": "TRACEARY_ARCHIVE_PASSPHRASE"
    }
  }
}
```

## リスク / ロールバック

- 手続き的巨大 usecase → plan/export/verify/delete/restore 分離
- bundle との過剰共通化 → まず crypto helper のみ共有
- decay GC との二重書き込み → 所有権分離を ops に明記
- 自動削除の驚き → 既定 disabled、doctor 可視化

**ロールバック:** 自動は opt-in。手動コマンドはドキュメント付きで出荷可能。破壊的既定は現状の archive なし `store gc` のまま。

## PR 分割

| PR | Issue | 成果物 |
|---|---|---|
| 設計 | #1370 | 本文書 + schema |
| 手動 | #1371 | コマンド + usecase + テスト + ベンチ |
| 自動 | #1372 | config + worker + doctor |
| dogfood | #1373 | 証拠ログ |

## 関連

- Epic: #1309
- Storage: [README.ja.md](./README.ja.md)
- Backup: [../backup/README.ja.md](../backup/README.ja.md)
