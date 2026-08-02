# 安全なストア圧縮

[English](safe-compaction.md)

ガベージコレクションとアーカイブ削除は論理行だけを削除し、in-place
`VACUUM` は実行しません。ファイル容量を回収するときは、リースに対応
しない旧版を含むすべての Traceary プロセスを停止し、明示的な
`traceary store compact plan`、`apply`、`status` を実行します。

`plan` は非破壊です。`apply` はソースと同じディレクトリへ `VACUUM
INTO` し、fsync 後に双方へ同一の互換性・整合性検証を実行してから、
同一ファイルシステム内でatomic exchangeします。元inodeはロールバック
用に保持されます。SQLite sidecar、検索メンテナンス遷移中、空き容量不足、
ファイルidentity変更、atomic exchange非対応環境ではfail closedします。

planは `lease_capability` も報告します。DarwinとLinuxでは通常のphysical
SQLite connectionが、隣接するstableな `<database>.traceary.lock` にshared
advisory lockを保持します。`apply`、`resume`、`rollback` はjournalや向きの
観測前から完了までexclusive lockを保持し、database inodeのexchange後も
排他を継続します。取得はcontext cancellationに従い、プロセス終了時はOSが
lockを解放します。lock file自体は意図的に残します。既存databaseと親directoryの
symlinkは同じlease namespaceへ解決し、安全にfenceできないhardlink databaseは
拒否します。非対応platformまたは
probe失敗時は `false` としてfail closedします。旧版や非協調processは引き続き
事前停止が必要です。
filesystem安全性は協調モデルです。参加するlive openerはすべて隣接leaseを使い、
破壊的境界ではsource、candidate、rollbackのhardlinkを拒否します。権限を持つ
非協調processによるdirectory entry変更はadvisory lockの境界外です。

中断後は `resume` を使います。journalの最終行ではなくファイルidentity
から向きを判定します。元へ戻す場合は `rollback` を使います。確認完了前に
`.traceary-compaction` journalやrollback artifactを削除しないでください。

## preview-first maintenance手順

1. live storeをcopyまたはbackupし、旧版・非協調processを停止します。
2. `traceary store compact plan --db-path /path/to/traceary.db`を実行します。
   payload/projection allocation、reclaimable page、filesystem headroom、実測診断
   latencyを確認し、file sizeだけでapplyを決めません。
3. review済みcopyで互換性preflightとscrubを完了します。in-place `VACUUM`は
   実行しません。
4. plan成功後だけ`compact apply RUN_ID`を実行します。safe engineがcandidateを
   構築・scrubし、atomic swap後も元inodeをrollback用に保持します。
5. 通常readを検証してからrollback artifactを削除します。失敗時は
   `compact rollback RUN_ID`を使います。
6. 中断後は`compact status RUN_ID`と`compact resume RUN_ID`を使い、candidateや
   rollback fileを手動renameしません。

v0.35のpayload/search policy activationは明示的にdeferします。v0.34 compaction
成功はstorage mechanicsの証明であり、次のpolicyを有効化する許可ではありません。
