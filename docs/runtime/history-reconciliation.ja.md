# 履歴の再照合と maintenance

[English](./history-reconciliation.md)

実装途中です。maintenance に譲る SQLite 読取 capability、domain/application の履歴 page、実 SQLite reader/source adapter は実装しましたが、公開 daemon SDK/CLI transport、commit 後の hint、metadata export/forget、それらの統合検証は未完了です。リリース可能・native host/session の復旧認定済みとは扱いません。

## store の正本と reader の寿命

既存 core store に commit 済みの履歴が正本です。通知は hint だけで、日時 tail は永続 commit cursor ではありません。runtime metadata は core store と分離し、正規 target registry の正本は Registry だけとします。core schema・outbox・commit sequence・daemon 独自 spool drainer は追加しません。

`Database.WithYieldingReadScope` は daemon の履歴読取専用の明示 capability で、通常 CLI の reader と分離しています。既存・正規形・通常ファイル・単一 hard link の store、初期化済みの対応 format state、必要な body-free metadata projection を要求します。空・初期化前・非互換の store は core store を作成・migration せず拒否します。benchmark 用 immutable connection や通常 read scope の入れ子でこの制約を迂回できません。

一度の読取は store admission・schema 検査・query を含む 250 ms の取消 budget を持ちます。callback は取消に従い、query resource を完了・close する必要があります。返却前に実 connection を close し、pagination・UI 待機・応答・native 操作を跨いで shared store lease を保持しません。応答しないファイルシステムや driver の強制割り込みまでは保証しません。Git/target の検証は読取の前後、SQLite shared lease の外で行います。呼出元の取消、maintenance/busy、非対応 store の error を区別します。

新規 daemon history reader は upgrade/compaction の pending 時に譲ります。読取中に marker が現れた場合はその読取を取り消し、maintenance 後の新たな明示 query で再試行できます。同 process が持つ exclusive maintenance connection の借用は拒否します。marker 検査は有界・no-follow・nonblocking とし、安全性を検証できないファイルを拒否します。既存 maintenance が所有する stale marker は削除しません。marker PID の検査を native agent/session の所有・終了証明には使いません。既存 CLI reader と store maintenance の振る舞いは変更しません。

## 残っている delivery 契約

公開 daemon transport と CLI には、実装済みの履歴 core を厳密な要求照合、有界 frame、正しい prefix と継続位置の制御を伴って公開する実装が必要です。
commit 後の hint は capture 結果、標準出力、spool 所有権、receipt 冪等性を変更してはいけません。
明示 runtime metadata export/forget では範囲、version、上限、同意、部分変更の不確定性を定義し、Registry、core 履歴、Git data、native 会話を削除してはいけません。
実際の upgrade/compaction、spool replay、生存 PTY との同時実行の検証、最終レビューと delivery gate も残っています。
これらは未完了の delivery 作業であり、現在の daemon が提供済みの capability ではありません。

## commit 済み page と登録済みの読取 source

application の `History` service は登録済み source を解決し、有界な body-free page を読み、実 SQLite lease を close してから source を再検証します。
busy な読取の自動再試行、store の初期化、Registry の更新は行いません。
`RegisteredHistorySources` は同じ RegistryRepository を読み、別の registry は持ちません。
読取 source は project、worktree、common directory の物理 witness が不変であることと、現在の所有者専用かつ正規形の通常ファイルで単一 hard link の store を検証します。
store の inode だけが交換された場合は `StoreIdentityChanged` として示し、maintenance 後の新たな履歴走査を可能にしますが、native execution、control、resume の権限は更新しません。
継続 page は現在の store witness に結び付け、変更があれば拒否します。

要求 view は登録済み正規 workspace 内の明示的な開始時刻以上、終了時刻未満の範囲です。
一度の要求は既存の降順 `(created_at_norm, event_id)` keyset と limit+1 の先読みを使い、最大 64 件を返します。
page は一度の読取で得た commit 済み履歴であり、page 間を保持する transaction や永続 commit sequence ではありません。
不正または過大な metadata は明示的に拒否し、黙って読み飛ばしません。
body と command 内容は hydrate しません。
frame に収まる prefix に制限する際も、最後に保持した行から継続できるようにします。

hint 欠落、再接続、定期更新の後は先頭 page に戻り、`More` が真である限り要求 view 全体を走査し直し、event ID で重複を除く必要があります。
先頭 page の再読取だけでは、深い位置への遅延 insert や同時刻の行を見落とします。
日時を永続 watermark に進めても commit 順序は保証できません。
実 SQLite の test では、走査完了後に過去時刻と同時刻で commit し、hint がなくても全件を回復すること、未 commit 行の不可視性、既存 CLI の件数との一致、busy 後の再試行と実 lease の解放、不正 metadata の拒否、inode 交換後の新たな走査を検証しています。
inode 交換 fixture は、実際の upgrade/compaction 全体や native host の認定を完了した証明ではありません。
