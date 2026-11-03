# Daemon の明示終了と中断履歴

[English](./daemon-lifecycle.md)

この文書は lifecycle 作業で実装した shutdown SDK/CLI と継続性 fence を説明します。作業全体はまだ Draft です。更新 handoff・auto-start descriptor、native host binding、最終検証は未完了です。リリース済み・実 AI の復旧認定済みとは扱わないでください。

## detach と shutdown の違い

出力 reader や window を閉じる操作は detach だけです。所有 PTY を閉じたり agent を終了したりしません。完全終了は別の preview/confirm 操作です。PTY master を閉じると **SIGHUP、制御 terminal の喪失、agent 終了、作業喪失**が起き得ます。master close は child/descendant の終了証明ではありません。実際の owned Wait と受理済み coordinator の照合が完了するまで daemon は lease と管理 endpoint を保持します。HUP を無視する child があれば draining が続きます。利用できる停止操作は別途明示する元 process group への `runtime stop` だけです。自動 TERM/KILL escalation、orphan sweep、adoption、入力再送、session 自動再開はありません。

master は daemon 自身が所有します。恒久仕様である安全な同 native session の明示再開に、別 PTY holder は必須ではありません。旧 PTY/process の復元は保証しません。

## 正確な全 roster を preview する

[managed launch](./managed-launch.ja.md) の手順で private runtime を登録します。以下の `<...>` は返された値に置き換えます。`/absolute/private/runtime` は placeholder であり、インストール先の指定ではありません。

```sh
traceary daemon status --runtime-dir /absolute/private/runtime --json
traceary daemon shutdown preview --runtime-dir /absolute/private/runtime --epoch <epoch>
```

preview は native effect を起こさず、新規 launch も止めません。CLI は有界な全 page を取得し、全 roster の件数と digest を検証してから JSON を表示します。行には selection/state/publication uncertainty だけを含め、terminal bytes、command、環境変数、保存 PID は含めません。実際の active ownership は最大 256 件です。終了後の publication uncertainty が残るため、preview は launch record 上限の 1,024 件まで扱います。これは追加の native 制御権ではありません。pending launch/control/exit-publication があれば confirm を拒否できます。

元の **8 個すべて**の binding 値を保持してください。digest/ID は相関値であり、人間が読んだ・承認したことの暗号学的証明ではありません。launch、control の受理・完了、終了、publication の変更で旧 plan は stale になります。次 page の取得では plan を置換しませんが、新しい preview の作成では置換します。

## cancel、confirm、receipt

`cancel`、`confirm`、`receipt` には同じ binding flags を指定します。

```sh
traceary daemon shutdown cancel --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --preview-id <preview-id> --revision <revision> \
  --roster-digest <digest> --total-runs <total-runs> \
  --pending-launches <pending-launches> --pending-controls <pending-controls> \
  --pending-reconciliations <pending-reconciliations>
```

cancel は confirm の受理前に元 preview を破棄するだけです。閉じた terminal を復元しません。confirm するときは `cancel` を `confirm` に置き換え、**`--acknowledge-work-loss`** を追加します。counter は canonical decimal（`00` でなく `0`）で指定します。任意 PID・filename・executable・signal は受け付けません。server は現在の epoch/revision/全 roster と pending work がゼロであることを再確認し、durable intent の後に native effect を行います。commit 後は新規 launch/input/resize を拒否します。生存 child には別途明示する guarded original-group Stop を利用できます。

`draining` は terminal close を示し、**daemon/process/descendant の終了を意味しません**。`pending`/`unknown` は delivery 成功や再試行許可ではありません。caller cancel、ACK の喪失・不正・矛盾、一部 close、永続化の不確定で `unknown` になります。daemon の終了処理と競合して最後の Stop ACK が失われる場合もあります。client/daemon は不確定な effect を自動再送しません。owner に接続できる間は元 binding の `receipt` を使い、終了後は read-only history を使います。

intent 予約の commit が不確定なら、新規 launch/input/resize を凍結し、master を閉じたり shutdown commit 済みと扱ったりしません。owner は元の read-only decision receipt と、別途明示する guarded Stop を維持します。空 roster でも lifecycle commit が不確定なら clean marker を記録せず、後から child が実際に終了しても不確定性を消去しません。単なる新規 launch の凍結では、明示承認した安全な shutdown の権限を取り消しません。

## offline history と上限

```sh
traceary daemon shutdown history --runtime-dir /absolute/private/runtime
```

history は既存 private metadata だけを開きます。daemon の起動、core store の作成・migration、PID adoption、native session 排他の検証、resume の許可は行いません。`clean` は daemon marker の publication だけを示します。後続 daemon が clean に終了しても、旧 unknown/orphaned の証拠は unknown のままです。元 launch/control outcome を exited/delivered に書き換えません。

`lifecycle.json` は owner-private・versioned で最大 16 MiB、共有 128 MiB envelope に含めます。shutdown intent は epoch を跨いで保持し、最大 **1,024 件**です。容量・storage・schema・ownership の問題では暗黙の pruning/repair をせず、新規 mutation を拒否します。read-only evidence を実行権限に変換しません。history の opt-in preference や deferred-update metadata は、host manager/updater がインストール済み・動作中である証拠ではありません。

## crash/restart と endpoint 所有権

`endpoints.json` は固定 2 socket だけの owner-private・versioned な receipt で、最大 4 KiB です。両 endpoint を組として mutation 前に検証し、再 bind と durable receipt publication を lifecycle epoch の記録・readiness より先に完了します。reclaim には実際の daemon 排他 lease、同じ物理 private root、旧 epoch の owner/device/inode/ctime 完全一致、期限付き Unix dial の `ECONNREFUSED`、unlink 直前の root/lease/stat 再検証が必要です。接続成功は active/busy であり cleanup 許可ではありません。receipt の欠落・破損・不一致、foreign file、symlink、不正 mode、stamp 変更、その他の probe error では socket や lifecycle history を変更せず拒否します。receipt publication 前の crash で未記録 socket が残る場合は、意図的に自動再起動を拒否します。緩い legacy cleanup は提供しません。

復旧するのは所有権を確認できた socket path だけです。旧 child の kill/adoption や session resume 許可は行いません。receipt は共有 private metadata 128 MiB 予算に含め、publication 失敗時は ready daemon を公開しません。悪意ある同一 UID との競合に対する完全分離は主張しません。

## 電源変化と因果的な観測の継続性

macOS では固定の kernel timestamp `kern.sleeptime`/`kern.waketime` だけを参照します。検証済みの組を 2 回読み取って完全一致を要求し、読み取り完了までの時間と時計の境界を確認します。組の変化、時計の逆行、参照不能・不整合、30 秒以上の watcher gap で旧 intake context を失効させます。参照できても sleep が進行中なら unknown です。1 秒周期の watcher で idle/in-flight evidence を失効させますが、intake と guarded projection read/commit でも直接再確認するため、poll 間の短い sleep で旧 stamp が現在有効と扱われることはありません。これらの参照は sleep の要求や host の電源設定変更を行いません。

明示的に構成した causal receiver には **enqueue 前**の opaque stamp を渡し、遅延した dequeue 時には付け替えません。queue overflow と consumer error では queued/in-flight scope を失効させ、stale item が新 stamp を取得することを防ぎます。native read が停止しても gate ごとに single-flight とし、caller の全待機を 250 ms に制限して process ownership lock を保持しません。遅れて返った read で失効済み scope を再開せず、daemon shutdown は中断不能な native read の終了を待ちません。gate revision と run ごとの native event revision は別の軸です。正常な poll が戻っただけでは native interaction の知識を復元せず、現在の完全な binding に対して新しく独立検証した event が必要です。

これは adapter contract であり host 認定ではありません。causal receiver は descriptor/host/version/worktree/run/generation/epoch/native event の因果関係を検証し、元 stamp で projection commit と現在の read の両方を guard する必要があります。非空 metadata や transport 受付はその検証ではありません。causal receiver を明示構成した場合はそれだけに配送し、power evidence が未対応でも history receiver に fallback しません。他 platform には現在認定済み power witness がありませんが、従来の history-only 配送は native freshness を主張せず維持します。通常の one-shot SDK socket の close は通信 gap と扱いません。入力・承認・停止・再開の権限を追加せず、これらの command から host 固有 native receiver を暗黙に有効化しません。

## 更新ポリシーの実装範囲

owner API は child の停止や通常の launch/control の凍結を行わず、継続的な更新延期を記録します。status は process 件数だけでなく、pending run/control/reconciliation/lifecycle/uncertainty の全阻害条件を含みます。target 変更と epoch 初期化を跨いで元の UTC 起算時刻と通知公開履歴を保持します。status 参照では通知を消費せず、対応済みの明示 attempt は busy・永続化失敗時にも attempt notice を返します。idle attempt の成功が返すのは正確な空 shutdown preview だけで、installer の競合実行許可ではありません。

別の foreground scheduler が 24 時間後の private metadata notice を永続公開し、native power polling を遅延させません。`notified_24h` は公開の重複抑止であり、**人間への表示や OS 通知の配送証明ではありません**。応答喪失後も overdue status を参照できます。lifecycle metadata coordinator は実際の owner join に含め、以前の shutdown preview を失効させます。不確定な commit では新規 effect と clean marker を拒否します。binary updater は実行しません。公開 update SDK/CLI と opt-in autostart descriptor は、この Draft ではまだ未実装です。

owner 側の update handoff は shutdown coordinator を再利用しますが、正確な**空** digest/roster、pending counter がゼロ、対象 target の一致、明示的な daemon exit の確認を構造的に要求します。新規 work で preview は stale になり、生存 PTY を閉じることはできません。既存の private envelope 内で元 shutdown intent の確定と対応する延期情報の退役を原子的に行い、その後に coordinator を join します。失敗時は software 更新成功と扱わず不確定性を保持します。同 ticket の join は元の purpose と target の両方の一致が必要です。closed terminal がゼロの `draining` receipt でも、OS 終了・更新の適用・旧 session 排他の証明ではありません。通常の confirmed shutdown は登録済みの最後の応答書き込みを有界に待ち、accept/attach loop は待ちません。通信喪失は引き続き unknown であり、再送許可ではありません。

## 残る検証

実 CLI の shutdown/cancel/history、有限 agent fixture による crash/restart、read-only native power API 参照、合成した causal queue/wake test は実装検証であり、実 AI/native-session や物理 macOS sleep/wake の認定ではありません。readiness 到達済み daemon は正確な private endpoint receipt から再起動できますが、認識できない・未記録の socket は変更しません。許可済み環境での物理 macOS sleep/wake、host 固有 binding/reduction、更新延期・通知、正確な opt-in host descriptor、最終独立 review/native/full CI/wave gate は引き続き必須です。これらの command は host の login settings、launchd job、workstation sleep、配布を変更しません。
