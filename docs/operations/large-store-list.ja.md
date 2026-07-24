# 大容量 live store における bounded metadata list

[English](large-store-list.md)

大容量または書き込み中の store で、最新 event を body なしで素早く確認するには、次を実行します。

```sh
traceary list --limit 1 --fields ts,kind --color never
```

この無条件の形だけが bounded latest-metadata path を使用します。SQLite は read-only で開き、store の初期化や migration、workspace-observation catch-up は実行しません。event body、prompt、response、command payload、hook spool、credential、identifier sample も読み取りません。既存の timestamp index を使い、最新秒に属する timestamp だけを正規化して並べ替えるため、RFC3339Nano の順序を保ちながら store 全体の sort を避けます。

## 結果の解釈

- 1 行の出力は完了した metadata 結果であり、全 subsystem の health を保証するものではありません。
- SQLite の `busy` または `locked` error は lock contention です。slow query とは区別します。競合 writer を停止または分離してから同じ bounded command を再実行してください。lock 対策として timeout を延長したり、`doctor --fix` を繰り返したりしません。
- `message` の追加、`--wide`、`--sensitive`、filter の指定は通常の read path を使います。古い store では初期化され、時間がかかることがあります。bounded check の成功後だけ使用してください。
- この command は data 削除、retention 適用、index build、SQLite sidecar の変更を行いません。capacity の確認は別操作の `traceary store gc --dry-run` を使い、retention 適用前に archive を作成してください。

## ロールバック

schema や data の migration はありません。release を戻すと以前の full initialization path に戻りますが、保存済み event は変更しません。
