# レガシー検索インデックスの退役

[English](search-retirement.md)

Traceary には検索インデックスが2系統ありました。migration 032 で導入した
全文コーパス系 (`event_search_documents` / `event_search_fts` /
`event_search_backfill_state`) と、それを置き換えた bounded な tiered
projection です。前者はすでにどこからも読まれていません。長期運用した store
ではファイル内で最大のオブジェクトになりがちで、maintainer の 24.7 GiB store
では 16.15 GiB、DB 全体の約 65% を占めていました。

v0.34 はこの系統の維持を停止しました。v0.35 では `traceary store compact` の copy 中に落とします。

## upgrade が自動で行うこと

migration 052 は初回起動時に適用され、定数コストのオブジェクトだけを DROP
します。writer trigger 8つすべて、`event_search_projection` view、
`search_maintenance_control` table です。大きな table には触れません。

`events` と `command_audits` 上の5つだけでなく、`event_search_documents` 上の
3つも削除します。`event_search_documents.event_id` は `ON DELETE CASCADE` を
持つため、`traceary store gc` や retention の実行はこの foreign key 経由で
インデックスに到達し続けていました。これらの trigger を残すと、event を削除
するたびに FTS5 の delete marker が追記され、削除対象のインデックス自体が
増え続けます。

この分割は意図的です。Traceary は store を開く際に未適用 migration を無条件で
適用するため、数 GiB の `DROP TABLE` を migration に入れると、upgrade 後の最初
の `traceary` 実行がそこで止まり、利用者に拒否する手段がありません。起動は
従来どおり速いままにし、領域回収は利用者の判断に委ねます。

upgrade 後、この系統は不活性になります。バイト数は占有しますが、読み書きは
一切発生しません。

## 領域を回収する

```sh
traceary store compact
```

`store compact` はストアを copy し、work copy 上で3つの table を `DROP` してから
`VACUUM INTO` します。退役済み系統は新しいファイルへコピーされません。
`store search-retire` と `store compact plan` / `apply` はありません。

DROP は行単位 `DELETE` ではなく直接 `DROP` します。FTS5 の content
table を空にすると、削除マーカーが新しい index segment に追記されるだけで領域は
回収されません。つまり先に削除するとファイルは一度**大きく**なります。
maintainer の store での実測ではファイルサイズ +14%、index サイズ +47%、所要
時間は DROP の約8倍でした。

`DROP` だけではページを SQLite の free list に返すだけです。ファイルは縮みません。
`auto_vacuum` は `NONE` です。領域をファイルシステムへ返すのは compact の
`VACUUM INTO` です。手順は [`safe-compaction.ja.md`](../storage/safe-compaction.ja.md)
を参照してください。

`traceary doctor` は、この系統が残っている間、占有バイト数と
`traceary store compact` を warning として報告します。

## rollback

roll-forward only です。`store search-restore` は存在せず、Traceary の
バージョンを戻してもインデックスは復活しません。writer trigger が削除済みの
ため、古いバイナリは upgrade 時点で更新が止まったインデックスを参照し、
不完全な結果を黙って返すことになります。

削除したデータが必要な場合は、退役前に取得した backup から store を復元して
ください。必要なら事前に取得してください。

## 退役後の検索

本文検索の正本は canonical events と command audits を新しい順に走査して候補を
復号する経路です。projection は fingerprint pre-filter と session tier を提供します。
migration-032 系列はもともと読まれていなかったため、退役しても結果は変わりません。

projection が incomplete / rebuilding / drifted でも検索は利用不能になりません。
fingerprint index は pre-filter にすぎないため、候補を復号して判定する経路へ
fail open し、正しい結果を返します。代償は正しさではなく処理量です。この状態の
検索は decode-bound になり、deep literal search budget を使い切った場合は結果を
切り詰めずにその旨を報告します。

制約は2点あり、いずれも本変更で新たに生じたものではなく bounded projection 由来
です。大きな結果集合への deep offset と、budget を使い切った検索は、部分的な
ページではなく明示的な上限を報告します。
