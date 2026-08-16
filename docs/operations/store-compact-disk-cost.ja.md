# `store compact` のディスク容量

[English](store-compact-disk-cost.md)

`traceary store compact` は、source database と `VACUUM INTO` が書き出す
candidate のために空き容量を必要とします。このボリュームに source サイズの
work copy を置けるときは、preflight は従来どおり store size の **1.1 倍**
を予約します。置けないときは dest サイズ（source から metadata-only の
回収見積もりを引いた値。下限は source の 10%）だけを予約し、それも足りなければ
in-place の filter + `PRAGMA incremental_vacuum` に落ちます（rollback inode
はありません）。

source サイズの work copy を別ディスクへ置くには `--work-dir` を使います。

```text
traceary store compact --work-dir /volumes/external/traceary-compact
```

candidate は live store の隣に書かれます（live ボリュームには dest サイズの
空きが必要です）。rollback は source 隣の `<db>.rollback-<run id>` です。
in-place 実行だけは `compact_strategy=in_place` で `rollback_retained=false`
になります。

この予約がディスク容量の全コストではありません。実行が成功した後も、元の
database は recovery copy として残ります。

```text
<db>.rollback-<run id>
```

これは source database と同じサイズで、何も自動削除しません。削除してよい
時期は運用者が判断します。削除すると、その run に対する
`traceary store compact rollback RUN_ID` が使えなくなります。新しい database
を検査して受け入れるまで保持してください。

たとえば issue #1790 の 30 GB rehearsal では、preflight は
36,539,352,678 bytes を要求しました。compact 後の database は
3,094,962,176 bytes、source の 9.3% でした。一方、run が committed になった
後も 33,217,593,344-byte の
`rehearsal.db.rollback-e59b9aca6ac60852432f077621f09001` が残りました。予約は
意図的に保守的で、rollback copy も意図的に保持されます。どちらのコストも
自動ではなくなりません。
