# 決定: 暗黙の open は data-dependent migration を拒否する (#1852)

[English](./offline-migration-gate.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1852

## 決定

`migrate()` が `MigrationExecutionClass` を読む。ストアに source event があるとき、`data_dependent_offline` は暗黙の open では適用しない。エラーは保留中の version と `traceary store init` を出す。拒否した migration ではストアを変えない。

適用は `traceary store init` だけ。新しいコマンドは足さない。空の新規ストアは暗黙の open だけで現行 schema に届く。

041 / 042 は `constant_in_place`（projection の帳簿コピーであり `events` ではない）。035 / 045 は offline のまま（既存データへの `CREATE INDEX`）。

## 暗黙拒否の理由

#1851 は、殺された 1 transaction の migration が毎 open でゼロからやり直すことを示した。誰も読まない分類はコメントである。hook が 60 秒ブロックして永遠に再試行するより、すぐメンテエラーの方がよい。

## 空の読みを返さない理由

`list` / `search` は typed error を返す。0 件を返すと、ストアが遅れていることが隠れる。

## 結果

- v0.33.1 形 + events: 暗黙 open は失敗し、ledger は 34 のまま。
- 同じストア + `store init`: 現行 version に届く。
- 空ストア: 暗黙 open で現行 version に届く。
- 041/042 再分類後、暗黙 open はそれらを適用し 045 で止まる。
- 045 が中断: ledger は 44。次の暗黙 open はすぐ失敗する。
