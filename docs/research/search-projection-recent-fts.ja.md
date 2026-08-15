# write-only の recent FTS（#1842）

[English](./search-projection-recent-fts.md)

どこからも読まれていない recent trigram FTS が元を取れるかの scratch 測定です。live の約 36 GiB store は対象外です。

## 決定

**write-only ティアを削除します。** `traceary search` から読ませません。

`8cae0ab0` が最後の reader を外しています。検索は fingerprint + decode walk です。session ティアは exact-keyword + LIKE です（#1756）。この FTS を空にしても結果は変わりません。

## 再配線（B）を採らない理由

再配線は snapshot / tail の merge（#1718）を戻します。v0.37 は「速く感じる」ゲートではありません。#1620 の store は `search_projection_recent_fts_data` に 9.0 GB を払い、そのあと `database or disk is full` になりました。

## scratch（`TestRecentFTSDoesNotEarnDecodeWalk`）

note 120 件、generation complete。出荷 FTS の MATCH は 0（writer を drop 済み）。比較用 trigram FTS は `search_projection_recent_documents` からだけ作り、捨てます。

| クエリ | walk hits | walk p50 | walk p95 | 比較 FTS hits | FTS p50 | FTS p95 |
|---|---|---|---|---|---|---|
| unique-recent-marker | 1 | 3.69 ms | 3.83 ms | 1 | 33 µs | 45 µs |
| shared-token | 12 | 4.36 ms | 4.57 ms | 12 | 26 µs | 31 µs |

FTS のほうが速いです。読まれない 9 GB の posting の元は取れません。fingerprint は同じ event を返します。

## やり方

- migration 066（`constant_in_place`）は writer trigger 2 本だけ落とします。implicit open では virtual table を DROP しません（052 と同じ規則）。
- `store compact` が work copy で `search_projection_recent_fts` を落とします。
- `--index-family-bytes` の既定は 1464 MiB のままです。対象は残ったもの（documents / session / fingerprints / shared）です。残った FTS ページは compact まで数えます。

## 約束

`literal_search_fingerprints` と session ティアは動き続けます。検索は recent FTS を読みません。
