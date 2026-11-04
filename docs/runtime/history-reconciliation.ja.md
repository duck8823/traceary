# 履歴の再照合と maintenance

[English](./history-reconciliation.md)

履歴照合の leaf は実装・レビュー・マージが完了し、`main` に含まれています。未リリースの source 上の機能であり、配布済み daemon の保証ではありません。maintenance に譲る SQLite 読取 capability、domain/application の履歴 page、実 SQLite reader/source adapter、公開 daemon SDK/CLI transport、coarse hint transport は実装済みです。
任意の commit 後 hint 生成と信頼済み managed root への配送は実装済みです。metadata audit/preview/forget は対応する v1 lifecycle envelope と endpoint envelope も扱いますが、この leaf の独立実装レビュー・固定 source の統合検証・マージは完了しています。daemon 全体・実機/native・リリースの検証は別です。リリース可能・native host/session の復旧認定済みとは扱いません。

## store の正本と reader の寿命

既存 core store に commit 済みの履歴が正本です。通知は hint だけで、日時 tail は永続 commit cursor ではありません。runtime metadata は core store と分離し、正規 target registry の正本は Registry だけとします。core schema・outbox・commit sequence・daemon 独自 spool drainer は追加しません。

`Database.WithYieldingReadScope` は daemon の履歴読取専用の明示 capability で、通常 CLI の reader と分離しています。既存・正規形・通常ファイル・単一 hard link の store、初期化済みの対応 format state、必要な body-free metadata projection を要求します。空・初期化前・非互換の store は core store を作成・migration せず拒否します。benchmark 用 immutable connection や通常 read scope の入れ子でこの制約を迂回できません。

一度の読取は store admission・schema 検査・query を含む 250 ms の取消 budget を持ちます。callback は取消に従い、query resource を完了・close する必要があります。返却前に実 connection を close し、pagination・UI 待機・応答・native 操作を跨いで shared store lease を保持しません。応答しないファイルシステムや driver の強制割り込みまでは保証しません。Git/target の検証は読取の前後、SQLite shared lease の外で行います。呼出元の取消、maintenance/busy、非対応 store の error を区別します。

新規 daemon history reader は upgrade/compaction の pending 時に譲ります。読取中に marker が現れた場合はその読取を取り消し、maintenance 後の新たな明示 query で再試行できます。同 process が持つ exclusive maintenance connection の借用は拒否します。marker 検査は有界・no-follow・nonblocking とし、安全性を検証できないファイルを拒否します。既存 maintenance が所有する stale marker は削除しません。marker PID の検査を native agent/session の所有・終了証明には使いません。既存 CLI reader と store maintenance の振る舞いは変更しません。

## 残っている delivery 契約

公開 daemon transport と CLI は、履歴 core を厳密な要求照合、有界 frame、正しい prefix と継続位置の制御を伴って公開しています。
commit 後の hint は capture 結果、標準出力、spool 所有権、receipt 冪等性を変更してはいけません。
明示 runtime metadata export/forget では範囲、version、上限、同意、部分変更の不確定性を定義し、Registry、core 履歴、Git data、native 会話を削除してはいけません。
履歴 leaf の独立実装レビュー・同一 head の CI・マージ後検証は完了しています。lifecycle 統合・native host・リリースの最終検証は残っています。別の実 prepared upgrade と filesystem spool fixture の検証を後述しますが、実 host の認定ではありません。
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

## 公開 daemon SDK と CLI

`daemon serve` は起動時に core store を開かず、履歴 metadata service を構成します。
HistoryFactory がない custom daemon は履歴要求を明示的に拒否します。
独立した HistoryClient は既存の所有者専用 Unix endpoint を読み、daemon を起動したり任意 SQL、実行ファイル、profile、store path を選択したりしません。

```sh
traceary runtime history page --runtime-dir /absolute/private/runtime --epoch ORIGINAL_EPOCH --worktree REGISTERED_WORKTREE_ID --from 2026-11-04T00:00:00Z --to 2026-11-05T00:00:00Z --limit 64
```

時刻は正規形の UTC RFC3339Nano、limit は 1..64 が必要です。
継続要求では、直前の最後に保持した行と source から得た `--before-at`、`--before-id`、`--source-witness` をすべて指定します。
全 page で同じ epoch、worktree、時刻範囲、source witness を保持します。
hint、gap、再接続、定期更新の後は先頭から要求 view 全体を走査し直します。
cursor は永続 commit 順序ではありません。

JSON は body-free event、more/next、物理 source witness、store_identity_changed、参考値の hint_revision/hint_overflow、authority=`committed_page_not_commit_cursor`、consistency=`single_page_only`、native_target_revalidated=false を返します。
応答は元の要求と source identity の一致を検証します。
エスケープ後の実 frame byte 数を 64 KiB に制限し、末尾の行は正確な次の anchor で継続取得できるようにします。
行を黙って破棄することはありません。
一行または header だけでも上限を超える場合は明示的な limit error を返します。
欠落、不正、混合、null、別操作の応答を、有効な履歴や古い launch/control の成功として扱いません。

独立した空の `history_hint` arm は observations.sock を使い、飽和する in-memory generation だけを進めます。
native ObservationReceiver、架空の native ID、queue admission は不要で、SQL や native 操作も行いません。
ACK は commit や表示の証明ではありません。
SDK は任意 producer 向けの HintHistory を提供します。
この transport の追加では capture、receipt、spool 所有権、native 権限は変更しません。

## 任意の commit 後通知

main は、明示的な `TRACEARY_RUNTIME_DIR` が既存の正規形かつ所有者専用の runtime root を示す場合だけ、EventUsecase に metadata を含まない通知を設定します。
managed PTY 起動では環境から受け継いだ `TRACEARY_RUNTIME_*` を除き、実際の信頼済み root を渡します。
profile の `--version` 検査と従来の launcher には配送先 root を渡しません。
native 引数、実行ファイル、PATH、認証、model 選択は変更しません。
配送先が存在しない、または安全でない場合は hint を無効にし、directory の作成や daemon の自動起動は行いません。

log、audit、transcript 削除は永続化に成功した後だけ通知します。
失敗した書込では通知せず、正規 event ID、inserted/redelivery の結果、audit の参照付け替え、標準出力と終了結果の契約を維持します。
任意通知の context 予算は 25 ms、service ごとの実行中 callback は最大一つです。
追加 hint は捨て、timeout 後も callback が実際に終了するまで slot を保持します。
callback の失敗・panic・daemon 不在で、commit 済みの主書込を失敗に変更しません。
通知に event、body、session、native の識別子は含めず、別の spool/drain worker も使いません。

hint は欠落し得ます。
cancel、admission、timeout、observer を使わない writer では通知が一切届かない場合があります。
receipt と hint ACK は commit 済み query の代わりにはなりません。
再接続、gap、定期更新の後は要求 view の先頭から全 page を再走査する必要があります。

## 明示的な runtime metadata 監査と損失

次のコマンドには正規形の `--runtime-dir` が必須です。
HOME/XDG の既定値選択、存在しない root の作成、core SQL の open、daemon の起動・停止、保存 PID の検査・kill・adopt は行いません。

```sh
traceary runtime metadata export --runtime-dir /absolute/private/runtime
traceary runtime metadata forget-preview --runtime-dir /absolute/private/runtime
```

export version1 は復元用 backup ではなく、有界な監査要約です。
最大 1024 件の記録済み run、32 件の profile、安全な target/profile 参照、receipt と不確定性の件数、正確な revision 文字列、物理 root witness、raw envelope の digest を返します。
実行ファイル/store path、PID、terminal/input body、環境、native 会話 data は含みません。
符号化した出力は 16 MiB を上限とし、超過時は切り捨てず明示 error にします。
`recorded_state` と `uncertain` は永続化した証跡であり、現在の native 生存状態ではありません。
終了記録があっても pending/unknown receipt と interrupted-run の記録による不確定性は残ります。
lifecycle/endpoint の revision、中断記録の件数、保持する shutdown/update/autostart/endpoint の範囲を明示しますが、update の内容や socket identity は export しません。

`forget` は記録済み launch、control receipt group と lifecycle 中断記録の全件を消し、native 会話は消しません。
新しい preview の正確な `--preview-digest` と、`--acknowledge-forget-history`、`--acknowledge-recovery-record-loss` の両方が必要です。
消した履歴にある復旧用の結び付けは失われ、export 要約では復元できません。
profile、Registry、core 履歴/spool、Git、native 会話は保持します。
lifecycle の epoch/clean、以前の epoch 状態、shutdown 監査、update deferral と autostart 設定を保持します。
endpoint 証跡は byte 単位で保持し、socket の unlink や native 権限の認定は行いません。
preview を破棄しても履歴は変わりませんが、既存 root 内に調停 lock file が作られる場合があります。

確認時は実際の daemon lifetime 排他 lease、同一 process の root claim、metadata lock が必要です。
稼働中 owner は busy とし、停止や signal は送りません。
実 replace 境界でも lease/root identity を再検証します。
同じ内容の JSON 整形を含め、全 5 envelope の raw content/revision/file identity/root の変更は preview を無効にします。
同じ byte 内容でファイルを交換した場合も preview は無効になります。
receipt group、中断記録、launch 記録の順に置換し、各 commit 済み prefix の参照整合性を維持します。
すべての revision 増分、各 envelope の encode 後サイズ上限と合計 byte budget を、最初の置換前に検証します。
効果が発生した後の失敗/cancel は明示的な不確定結果とし、rollback・成功とは扱わず自動再試行もしません。
export と新たな preview で確認してから、別の明示判断が必要です。
すでに空の receipt、中断記録、launch は書き直さず、no-op は履歴 envelope を作成せず revision も進めません。
出力 error でも受け付け済み mutation は取り消せません。

対応する registry/execution/receipt/lifecycle/endpoint v1 envelope を厳密に検証します。
中断記録から launch への参照、保持する shutdown/update の不変条件と物理 root に結び付いた endpoint 証跡も検証対象です。
正しい JSON の未対応 version は v1 固有の field/null 検証より先に未対応として拒否し、壊れた JSON や重複 key は不正として拒否します。
未知、危険、非互換の metadata（実 schema を統合する前の recovery/history format を含む）は mutation 前に拒否します。
offline endpoint 検証は、保持する lifecycle epoch を新たな daemon 起動時の epoch と混同しません。
sidecar の一括削除、orphan の自動 cleanup、endpoint 回収や native 復旧は提供しません。

**delivery は未完了です。**
固定した実 lifecycle producer/reader fixture の検証、独立実装レビュー、新しい CI とマージ後検証は完了しています。lifecycle 全体・daemon wave・リリースの最終 gate は残っています。
この監査操作は native 会話の再開を実装、認定するものではありません。

## 実 compaction と所有 PTY の検証

別の統合 test で、実際の StoreCompactionUsecase、SQLiteCompactionBuilder、StoreReplacementFiles、StoreLeaseCoordinator を使い、移行済みの生成 store に対して履歴 page を連続要求しながら compaction を実行します。
有界な test gate は本物の排他 lease を取得した後だけ待機します。
その間の同期 page 要求 3 回は busy を明示する必要があります。
実 replica compaction が別の物理 store を公開し、要求 view 全件を保持し、新しい履歴読取に成功することを検証します。
古い source の継続要求と native Registry 再検証は changed で拒否し、native 権限を更新しません。

実 Runtime が、登録した有限 PTY fixture を maintenance の前後も所有します。
PID の検査・signal、自動 native 入力は行いません。
同じ identity が交換の前・中・後に live のまま維持され、前後の BEFORE/AFTER 出力を保持し、明示的な fixture 解放後だけ所有 Wait の exit7 に収束します。
交換前の core hook delivery は、交換後の exact redelivery でも正規 ID と inserted=false を維持します。
交換後に要求範囲内の深い過去へ commit した行も、hint なしで回復し、既存 CLI metadata query と件数・ID が一致します。

これは inode の copy fixture だけではなく、実 compaction と合成の所有 PTY/core receipt の検証です。
実 host/native 会話の復旧や物理 sleep/wake の認定ではありません。
prepared upgrade と filesystem spool は後述の別 matrix で検証し、この compaction fixture 単独の証跡では代用しません。

## 実 prepared upgrade と filesystem spool の matrix

schema86 の fixture は、現在の version 行を書き換えず、チェックイン済みの SQL1..86 を実行して初期化します。
実 PreparedUpgradeMigrationRecipe/PreparedMigrationVerifier、PreparedStoreUpgradeFiles、NewPreparedStoreUpgradeUsecase が、本物の StoreLeaseCoordinator の下で offline migration87 を適用します。
小規模 fixture 用の明示 budget で wall time、公開 lock、所有/WAL/一時 byte を制限します。
独立した履歴 consumer は連続要求を続け、個別の有界読取の timeout で workload を黙って停止しません。
実排他 lease の保持中、同期 page 要求 3 回は busy を返し、同じ所有有限 PTY は閉じません。
元の rollback inode の保持、別の source inode の公開、journal の committed 状態と receipt の正確な migration digest、migration87 の column の実作成を検証します。
全履歴、古い witness の拒否、native Registry の changed 維持、upgrade 後の hint なし deep late commit、既存 CLI との件数・ID 一致を確認します。
同じ PTY の BEFORE/AFTER 出力と所有 Wait の exit7 も維持します。

別の CLI matrix は、偽 repository や直接 Save を spool と呼ばず、実 filesystem spool の persist/claim/requeue/drain を使います。
親が本物の maintenance 排他 lease を保持する間、有限の別 process が既存 drainer を実行します。
同一 process の正当な connection 借用で失敗が隠れない構成です。
本物の typed maintenance error で一件だけ再 queue し、receipt/payload/取得済み workspace を保持し、core に先行 capture がないことを確認します。
実 replica compaction と物理 source 交換後、既存 drain が prompt 一件を保存し、record/claim を削除します。
同じ元の永続 spool byte を戻して file acknowledgement の欠落を再現し、新たな process の再配送でも同じ正規 event ID・件数を維持し、pending/claim が残らないことを検証します。
runtime 履歴全件と既存 CLI metadata query の結果も一致させます。
daemon の別 drainer、native 入力、実 AI は追加しません。

これらは生成 SQLite/filesystem と所有有限 PTY の検証であり、性能 benchmark、fresh-store bootstrap の採用、実 host/会話の復旧認定ではありません。
実 schema の確定後の recovery format 統合と、daemon 全体・native・wave・リリースの最終 gate は引き続き必要です。

compaction の診断用 build-progress worker は、停止時に実際の終了まで join します。
停止の反復・同時呼出しも扱い、cancel だけを完了とは扱いません。
phase の移行や出力 consumer が最後の emit と競合せず、設定は worker 起動前に取り込みます。
他の所有 I/O と同様、任意の writer/filesystem が停止したままなら、その I/O が戻るまで join できません。
見せかけの完了 timeout は提供しません。

新しい descriptor や recovery schema は、実際の metadata inventory・budget・各書込 prefix が有効な forget 契約を拡張する必要があります。完了した履歴 leaf が新 schema を認定するわけではありません。
