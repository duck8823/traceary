# 安全なストア圧縮

[English](safe-compaction.md)

`traceary store compact` がストアファイルを書き換えます。in-place
`VACUUM` は実行しません。ファイル容量を回収するときは、リースに対応
しない旧版を含むすべての Traceary プロセスを停止し、
`traceary store compact` を実行します。`plan` / `apply` / `resume` /
`status` はなくなりました。元 inode へ戻すときは
`traceary store compact rollback RUN_ID` です。

書き換えはソースと同じディレクトリへ `VACUUM
INTO` し、fsync 後に双方へ同一の互換性・整合性検証を実行してから、
同一ファイルシステム内でatomic exchangeします。元inodeはロールバック
用に保持されます。SQLite sidecar、レガシー検索インデックスの残存、空き容量不足、
ファイルidentity変更、atomic exchange非対応環境ではfail closedします。
preflight は、`VACUUM INTO` が完了するまで candidate size が分からないため、
source store size の 1.1 倍の空き容量を要求します。成功後も source size と同じ
大きさの元 database rollback copy が残ります。運用者向けの詳細は
[`store compact` のディスク容量](../operations/store-compact-disk-cost.ja.md) を参照してください。

`store compact`は書き換え全体でstoreのexclusive leaseを保持します。`-journal`がなく、
non-zeroの`-wal`もない場合は、存在するregularな`-wal`と`-shm`を、`-shm`が
non-zeroでも両方stale artifactとして削除します。directoryをsyncしてから、storeを
openする前に再検査します。shm fileはdatabase contentを含まず、fsyncもされません。
empty WALではすべてのcontentをmain database fileから取得します。non-zeroのWAL、
サイズにかかわらずすべての`-journal`、symlink、FIFO、その他のnon-regular pathは
削除しません。sidecarが残っている場合は、すべてのTraceary process（projection/status
reader、旧版、非協調版を含む）を停止して同じcommandをretryしてください。sidecarを
手動削除してはいけません。non-zero WALまたはnon-regular sidecarにはliveなSQLite
stateが含まれる可能性があります。

candidate 構築前にも同じcleanupを実行します。lease 取得前にreaderがsidecarを
作りうるためです。cleanupはexclusive leaseを取得できたときだけ走り、liveな協調接続は
すべて同じleaseのshared formを保持しているため、いずれかがstoreを開いている間に
sidecarが削除されることはありません（lease取得側が待ちます）。exchange直前の最終検査は
strictのままです。その時点ではcleanupが済んでいるため、そこでsidecarが現れることは
run中にopenerが現れたことを意味し、runは中止されます。

このsidecar recoveryは`store compact`に限られます。

`hook memory-extract-worker` は Extract job のあいだ shared store lease を保持します。
次の job を始める前に、lease file の隣にある内部 compact-pending marker を見ます。
marker があるときは新しい job を始めません。実行中の job は最後まで走り、worker は
残ジョブを spool に残して終了します。compact は exclusive lease 待ちの前にこの
marker を書き、lease 解放時に消します。exclusive timeout のエラーは、`lsof` が
見えるとき holder の pid と command を含みます。extract worker を kill しないで
ください。backoff で空きができます。spool を空にしたいときだけ `doctor --fix` です。


退役済み検索インデックスは work copy 上で DROP します。source に残っていても
compact は拒否しません。詳細は
[`search-retirement.ja.md`](../operations/search-retirement.ja.md) を参照して
ください。

compact は `event_content_dedupe_archive`（content-event dedupe の隔離監査証跡）
を 90 日の retention window 内で保持し、それより古い行は compact 時に破棄します。

DarwinとLinuxでは通常のphysical
SQLite connectionが、隣接するstableな `<database>.traceary.lock` にshared
advisory lockを保持します。compact はjournalや向きの
観測前から完了までexclusive lockを保持し、database inodeのexchange後も
排他を継続します。取得はcontext cancellationに従い、プロセス終了時はOSが
lockを解放します。lock file自体は意図的に残します。既存databaseと親directoryの
symlinkは同じlease namespaceへ解決し、安全にfenceできないhardlink databaseは
拒否します。compact の実行前にはlease取得が必須なので、非対応platformでは
取得時点で失敗します。旧版や非協調processは引き続き
事前停止が必要です。
filesystem安全性は協調モデルです。参加するlive openerはすべて隣接leaseを使い、
破壊的境界ではsource、candidate、rollbackのhardlinkを拒否します。権限を持つ
非協調processによるdirectory entry変更はadvisory lockの境界外です。

中断後は `traceary store compact` を再実行します。進行中 journal があれば再開します。
元へ戻す場合は `rollback RUN_ID` を使います。確認完了前に
`.traceary-compaction` journalやrollback artifactを削除しないでください。

## rollback copy をいつ解放するか

apply 末尾の `VerifyPair` は実在する検査です（互換性、filtered / logical digest、
attestation）。しかし compacted store が実使用で正しいことの証明ではありません。
そのため Traceary は commit 時にも次回の成功 open 時にも
`<db>.rollback-<run>` を削除せず、release 用の新コマンドも追加しません。

operator が解放する手段は、compact 成功 JSON の `rollback_path`
（`rollback_retained: true`）を削除することです。削除するとその run の
`traceary store compact rollback RUN_ID` は使えなくなります。
`traceary doctor` は隣に残った copy を `compact-rollback-copy` として報告します。

## maintenance手順

1. live storeをcopyまたはbackupし、旧版・非協調processを停止します。
2. 回収したい最古の session を fold します（`traceary-session-refine`）。
   部分 fold のあと compact すると、その分だけ回収されます。
3. `traceary store compact --db-path /path/to/traceary.db`を実行します。
   in-place `VACUUM`は実行しません。`--force` は未 refine の破棄対象へ機械要約を
   書き、エージェントの判断理由が失われることを明示します。compact は
   `command_audits` 行がある履歴 `command_executed` body も空にし、
   `released_command_body_bytes` に stored blob の合計を出します。
   書き換え後のファイルサイズは `bytes_after` です。
4. search が drifted なら `traceary store compact --projection-rebuild` で
   開始または置き換え、同じコマンドをもう一度実行して complete まで resume
   します。通常 read を検証してから rollback artifact を削除します。失敗時は
   `compact rollback RUN_ID` です。
5. 中断後は`store compact`を再実行し、candidateや rollback fileを手動renameしません。
