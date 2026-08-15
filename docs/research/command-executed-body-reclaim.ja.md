# 決定: compact 中に重複した command_executed body を回収する (#1853)

[English](./command-executed-body-reclaim.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1853

## 決定

`traceary store compact` は、`command_audits` 行がある `command_executed` の
履歴 `events.body` を、work copy 上で空にします。残本文の符号化と
`VACUUM INTO` の前です。新しいコマンドは追加しません。暗黙の store open では
実行しません。

`store payload-backfill` と `store dedupe` は #1872 で削除済みです。履歴
payload の書き換えとファイルへのページ返却は compact が唯一の operator 面です。

## 残すべきガード

- `EXISTS (SELECT 1 FROM command_audits a WHERE a.event_id = events.id)`。
  `traceary log --kind command_executed` は audit 行なしで body を書きます。
  その body が唯一のコピーなので残します。
- 空 identity メタデータ: `encodePayload(nil, identity)`。`body_sha256` は
  空文字の digest になり、他の空書き込みと同じ整合性検査に通ります。

## live cursor にしない理由

#1853 は 2.24 GiB の live `UPDATE` が kill されるとゼロから再開するため、
`payload_backfill_runs` 型の resume を求めていました。compact はすでに
ファイルをコピーし、work copy をフィルタし、phase を journal し、元 inode を
rollback として残します。中断しても原本は失われません。物理バイトが減るのは
compact がもともと走らせる `VACUUM INTO` のあとです。

## 実測の回収量

JSON は `released_command_body_rows` と `released_command_body_bytes` を出します。
バイトは選択行の `SUM(length(CAST(body AS BLOB)))` です。stored size であり
`body_plaintext_bytes` ではありません（その膨張は #1744）。

## VerifyPair

audit があり非空 body だった `command_executed` を、candidate 側で空に
decode できる書き換えは許可します。それ以外の available body 書き換えは
これまでどおり失敗します。

## 手順

1. `traceary store backup create …`
2. 回収したい session を fold する（discard 対象が未 refine の transcript
   だけのストアは compact が拒否する）
3. `traceary store compact`
4. swap 後に search が drifted なら
   `traceary store search-projection start` のあと `resume --until-complete`
5. ファイルサイズは `bytes_after`。受け入れたら rollback inode を消す

compact は work copy で search family を落とすので、そこの body 更新は
live projection を invalidation しません。swap 後の rebuild は、廃止した
payload-backfill 手順と同じです。
