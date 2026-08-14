# Payload圧縮リハーサル（v0.34）

[English](payload-rehearsal.md)

> v0.35.0 で削除（#1872）。`store payload-rehearsal` は unknown command です。
> 符号化は `traceary store compact` の途中で行います。

v0.34が提供するのは、コピーしたストア上のリハーサルだけでした。
canonical圧縮書き込みを有効化せず、設定済みのliveストアを変更しません。

codec互換性preflightは、新たにupgradeしたstoreではevent body、command、input、outputの4 laneをconstant-sizeのtransactional counterで管理します。
このためcodec column追加時に保存済みhistoryをscanしません。
旧migrationを適用済みの開発storeは、明示的なlegacy互換modeで既存partial indexを利用します。
互換性証跡がunknown、invalid、または不整合の場合、rehearsal run作成前にfail closedします。

1. writerを停止し、checkpoint済みの単一SQLiteファイルをコピーします。
   コピーには`-wal`と`-shm`を残しません。
2. `traceary store payload-rehearsal preview --target COPY --live-db LIVE`を実行します。
   previewはimmutable/query-onlyで開き、検査前後のDB/WAL/SHM snapshotが一致しなければ失敗します。

migrationの要否はembedded versionと`schema_migrations`の差分から判定します。
run前にexact cloneを独立した1秒上限内で検証済みWAL modeへ切り替え、pending migration一式を単一の`BEGIN IMMEDIATE`/`COMMIT`で実行し、live WAL bytesとlock durationを記録します。
コピーtargetは同じpending状態、journal mode、WAL予約を再現しなければなりません。
targetのjournal変更後に失敗した場合は検証済みphysical backupを復元し、明示的なno-pending planの場合だけmigration実行を省略します。
3. `... run --target COPY --live-db LIVE --backup ROLLBACK`を実行します。
   圧縮結果は`payload_rehearsal_rows`だけに保存し、event/auditのcanonical payloadは変更しません。
4. 中断または上限到達後は、同じ設定とrollback artifactで`... resume`を実行します。
   checkpointとshadow insertは同じtransactionでcommitされます。
5. `... scrub`でshadow範囲をdecode/checksum検証し、`... rollback --backup ROLLBACK`で物理復元を検証します。

liveストアとpath、symlink、hardlink、file identityのいずれかが一致するtargetは拒否します。
出力は集計値とopaque hashだけです。v0.34にはactivation commandがなく、有効化はv0.35で行います。
