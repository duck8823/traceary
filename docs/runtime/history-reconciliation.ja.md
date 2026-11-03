# 履歴の再照合と maintenance

[English](./history-reconciliation.md)

実装途中です。maintenance に譲る SQLite 読取 capability は実装しましたが、公開 runtime history paging、commit 後の hint、metadata export/forget、それらの統合検証は未完了です。リリース可能・native host/session の復旧認定済みとは扱いません。

## store の正本と reader の寿命

既存 core store に commit 済みの履歴が正本です。通知は hint だけで、日時 tail は永続 commit cursor ではありません。runtime metadata は core store と分離し、正規 target registry の正本は Registry だけとします。core schema・outbox・commit sequence・daemon 独自 spool drainer は追加しません。

`Database.WithYieldingReadScope` は daemon の履歴読取専用の明示 capability で、通常 CLI の reader と分離しています。既存・正規形・通常ファイル・単一 hard link の store、初期化済みの対応 format state、必要な body-free metadata projection を要求します。空・初期化前・非互換の store は core store を作成・migration せず拒否します。benchmark 用 immutable connection や通常 read scope の入れ子でこの制約を迂回できません。

一度の読取は store admission・schema 検査・query を含む 250 ms の取消 budget を持ちます。callback は取消に従い、query resource を完了・close する必要があります。返却前に実 connection を close し、pagination・UI 待機・応答・native 操作を跨いで shared store lease を保持しません。応答しないファイルシステムや driver の強制割り込みまでは保証しません。Git/target の検証は読取の前後、SQLite shared lease の外で行います。呼出元の取消、maintenance/busy、非対応 store の error を区別します。

新規 daemon history reader は upgrade/compaction の pending 時に譲ります。読取中に marker が現れた場合はその読取を取り消し、maintenance 後の新たな明示 query で再試行できます。同 process が持つ exclusive maintenance connection の借用は拒否します。marker 検査は有界・no-follow・nonblocking とし、安全性を検証できないファイルを拒否します。既存 maintenance が所有する stale marker は削除しません。marker PID の検査を native agent/session の所有・終了証明には使いません。既存 CLI reader と store maintenance の振る舞いは変更しません。

## 残っている delivery 契約

history paging は既存 body-free keyset query を再利用し、各 page を明示的に制限して、hint 欠落や遅延 insert 後に要求範囲を走査し直す必要があります。page 間に固定 transaction や永続 commit 順序があるとは主張できません。compaction による store 交換は可視化し、native execution/control/resume の権限を更新してはいけません。commit 後の hint は capture 結果・標準出力・spool 所有権・receipt 冪等性を変更してはいけません。明示 runtime metadata export/forget では範囲・version・上限・同意・部分変更の不確定性を定義し、Registry・core 履歴・Git data・native 会話を削除してはいけません。これらは今後の必須実装・検証であり、現在の読取 capability が提供済みの機能ではありません。
