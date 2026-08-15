# 決定: cleanup のアドレスをテーブル形にする (#1825)

[English](./search-projection-cleanup-address.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1825

## 決定

cleanup は `rowid` ではなく、各 projection テーブル自身の primary key で行を指します。`ProjectionCleanupCandidate` が `Address1/2/3` と `AddressBlob` を運び、DELETE は class ごとに組みます。`RowsAffected() != 1` はこれまでどおり `SearchProjectionDriftError` です。

#1808 の 2 つの `WITHOUT ROWID` 変換は **今回やりません**。

## いま変換しない理由

`search_projection_session_keywords` の implicit PK autoindex は、テーブル本体と同程度の大きさのままです（scratch の 400 行 `dbstat`。#1808 が 408k event の rehearsal で見た比率と同じ）。比率は構造的です。

変換するなら次のどちらかが必要です。

- corpus 比例テーブルの `INSERT…SELECT`（store-sized、disk peak が増える）
- `DROP` + 再作成 + live generation の破棄（upgrade した全 store が search を rebuild）

どちらも、この issue が防ぎたい stall より大きい運用コストです。addressing を直せば、あとから変換しても `SELECT rowid` でコンパイル失敗しません。

## 結果

- recent / eviction はこれまでどおり `document_id`（INTEGER PRIMARY KEY）
- keyword / fingerprint / summary / aggregate / exclusion は PK 列
- rowid テーブルはそのまま。autoindex の重複は disk-peak 付きの変換まで残る
