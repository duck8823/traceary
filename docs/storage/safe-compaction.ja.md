# 安全なストア圧縮

ガベージコレクションとアーカイブ削除は論理行だけを削除し、in-place
`VACUUM` は実行しません。ファイル容量を回収するときは、リースに対応
しない旧版を含むすべての Traceary プロセスを停止し、明示的な
`traceary store compact plan`、`apply`、`status` を実行します。

`plan` は非破壊です。`apply` はソースと同じディレクトリへ `VACUUM
INTO` し、fsync 後に双方へ同一の互換性・整合性検証を実行してから、
同一ファイルシステム内でatomic exchangeします。元inodeはロールバック
用に保持されます。SQLite sidecar、検索メンテナンス遷移中、空き容量不足、
ファイルidentity変更、atomic exchange非対応環境ではfail closedします。

中断後は `resume` を使います。journalの最終行ではなくファイルidentity
から向きを判定します。元へ戻す場合は `rollback` を使います。確認完了前に
`.traceary-compaction` journalやrollback artifactを削除しないでください。
