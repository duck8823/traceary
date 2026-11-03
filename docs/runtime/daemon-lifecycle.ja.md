# Daemon の明示終了と中断履歴

[English](./daemon-lifecycle.md)

この文書は lifecycle 作業で実装した shutdown SDK/CLI を説明します。作業全体はまだ Draft です。native power/gap intake、更新 handoff・auto-start descriptor、最終検証は未完了です。リリース済み・実 AI の復旧認定済みとは扱わないでください。

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

## offline history と上限

```sh
traceary daemon shutdown history --runtime-dir /absolute/private/runtime
```

history は既存 private metadata だけを開きます。daemon の起動、core store の作成・migration、PID adoption、native session 排他の検証、resume の許可は行いません。`clean` は daemon marker の publication だけを示します。後続 daemon が clean に終了しても、旧 unknown/orphaned の証拠は unknown のままです。元 launch/control outcome を exited/delivered に書き換えません。

`lifecycle.json` は owner-private・versioned で最大 16 MiB、共有 128 MiB envelope に含めます。shutdown intent は epoch を跨いで保持し、最大 **1,024 件**です。容量・storage・schema・ownership の問題では暗黙の pruning/repair をせず、新規 mutation を拒否します。read-only evidence を実行権限に変換しません。history の opt-in preference や deferred-update metadata は、host manager/updater がインストール済み・動作中である証拠ではありません。

## crash/restart と endpoint 所有権

`endpoints.json` は固定 2 socket だけの owner-private・versioned な receipt で、最大 4 KiB です。両 endpoint を組として mutation 前に検証し、再 bind と durable receipt publication を lifecycle epoch の記録・readiness より先に完了します。reclaim には実際の daemon 排他 lease、同じ物理 private root、旧 epoch の owner/device/inode/ctime 完全一致、期限付き Unix dial の `ECONNREFUSED`、unlink 直前の root/lease/stat 再検証が必要です。接続成功は active/busy であり cleanup 許可ではありません。receipt の欠落・破損・不一致、foreign file、symlink、不正 mode、stamp 変更、その他の probe error では socket や lifecycle history を変更せず拒否します。receipt publication 前の crash で未記録 socket が残る場合は、意図的に自動再起動を拒否します。緩い legacy cleanup は提供しません。

復旧するのは所有権を確認できた socket path だけです。旧 child の kill/adoption や session resume 許可は行いません。receipt は共有 private metadata 128 MiB 予算に含め、publication 失敗時は ready daemon を公開しません。悪意ある同一 UID との競合に対する完全分離は主張しません。

## 残る検証

実 CLI の shutdown/cancel/history と、有限 agent fixture による crash/restart は実装検証であり、実 AI/native-session や物理 macOS sleep/wake の認定ではありません。認識できない・未記録の stale socket は変更してはいけません。readiness 到達済み daemon は正確な private endpoint receipt から再起動できますが、認識できない・未記録の socket は変更しません。power/gap 失効、更新延期・通知、正確な opt-in host descriptor、最終独立 review/native/full CI/wave gate は引き続き必須です。これらの command は host の login settings、launchd job、workstation sleep、配布を変更しません。
