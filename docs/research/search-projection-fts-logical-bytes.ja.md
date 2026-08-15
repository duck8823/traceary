# search-projection の `fts_logical_bytes` 整数幅 (#1787)

[English](./search-projection-fts-logical-bytes.md)

FTS shadow table の logical-byte クエリの scratch サイズ測定です。live の約 36 GiB ストアではありません。

## 決定

フィールド名は残します。整数列は **1 つあたり 8 バイト**（SQL INTEGER / int64 幅）で数えます。このフィールドを `dbstat` にはしません（`physical_bytes` がそれです）。リネームだけにもしません。

`length(CAST(<integer> AS BLOB))` は十進の桁数です（`pgno=123456` → 6）。BLOB / TEXT 列はこれまでどおり `length(CAST(x AS BLOB))` です。

## shadow 列（実測）

| テーブル | length | 8 バイト整数 |
|---|---|---|
| `*_fts_data` | `block` | `id` は含めない（もともと SUM に無い） |
| `*_fts_idx` | `term`（BLOB） | `segid`, `pgno` |
| `*_fts_docsize` | `sz`（BLOB。整数ではない） | `id` |
| `*_fts_config` | `k` と整数でない `v` | `typeof(v)='integer'` の `v` |

## テスト

`TestSearchProjectionFTSLogicalBytesUsesIntegerColumnWidth` は recent 文書を入れたあと `SearchProjectionStatus` を呼びます。期待値は Go 側で 8 バイト規則を歩いて出します。この fixture では旧桁数 SUM と一致してはいけません。

この数は報告であり、予算ではありません。
