# `store compact` のディスク容量

[English](store-compact-disk-cost.md)

`traceary store compact` は、source database と `VACUUM INTO` が書き出す
candidate のために空き容量を必要とします。開始前の preflight は、store
size の **1.1 倍**（および operation 固有の temporary budget）を予約します。
これは意図的な worst-case reservation です。SQLite は書き込み前に
candidate のサイズを報告できないため、安全側に倒して compact 後の store
が source と同じ大きさになると仮定します。実際の compact 結果はもっと小さく
なる場合がありますが、その小さい値を事前予約に使うのは安全ではありません。

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
