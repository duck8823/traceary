# Archive-before-GC dogfood 決定ログ (v0.28.0)

[English](./archive-dogfood-v0.28.md)

#1373 · epic #1309 · 親 #1360。

## 環境

| 項目 | 値 |
|---|---|
| 日付 (UTC) | 2026-07-17 |
| ホスト | local multi-agent dogfood store |
| live store | `~/.config/traceary/traceary.db`（演習前 ~2.5 GiB） |
| 演習 | **copy のみ**（live への archive delete は行わない） |
| バイナリ | #1371 archive CLI + #1386 IN チャンク修正 |

## 演習結果

### 1. dry-run

| keep-days | 結果 |
|---|---|
| 90 | 候補 0 |
| 30 | **13 667** |
| 14 | **131 418**（#1386 前は SQL too many variables） |

### 2. verify-before-delete（copy, keep-days=30）

- 行数 13667 / 削除 13667 / Archive OK / ~24.5s
- package ~57 MiB、DB ~2.5 → ~2.2 GiB

### 3. stale-active-sessions

- dry-run 623 → apply で 623 終了（#1363 経路）

### 4. 自動モード (#1372)

**dogfood / 既定 install は `retention.mode=disabled` を維持。**

手動 `store archive create --delete-after-verify` を multi-GB 級で実証済み。auto は opt-in のまま。

## フォローアップ

| Issue | 内容 | 扱い |
|---|---|---|
| #1386 | archive IN の SQL 変数上限 | 実装 PR |

## 決定（正本）

1. 手動 archive-before-GC は release-blocking で出荷済み
2. automatic archive-then-gc は **opt-in のみ**で出荷（#1372）。dogfood では disabled
3. archive なし `store gc` は従来どおり利用可
4. #1372 の v0.28.0 スコープアウトは行わない
