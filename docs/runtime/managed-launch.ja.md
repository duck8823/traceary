# 明示的な managed PTY 起動

[English](managed-launch.md)

[R0](../adr/2026-11-03-headless-runtime-r0.ja.md) の起動機能です。
Daemon が実際の child handle と PTY master を所有し、client 接続とは分離します。
Core の Session は履歴のまとまりのままで、process lifecycle は追加しません。
Wrapper、PATH shim、shell command、credential コピー、model API、承認の迂回、host 設定変更は行いません。

## Operator による明示登録

`runtime` コマンドは JSON を出力します。
`daemon serve` と同じ絶対 path の `--runtime-dir` を指定します。
登録は明示的に private root を作成しますが、registry/profile の参照は既存 root のみを開き、欠損 directory を作成せず、agent も起動しません。
Namespace は core DB/config bootstrap より前に構成され、明示 root があれば HOME 未設定でも動作します。

以下の ID・path・version は**未検証の placeholder**です。
自分で明示的に選択した対象に置き換えてください。

```sh
traceary runtime project register --runtime-dir /absolute/private/runtime --path /absolute/project
traceary runtime worktree register --runtime-dir /absolute/private/runtime --project p_ID --path /absolute/worktree --db-path /absolute/existing/core.db
traceary runtime profile register --runtime-dir /absolute/private/runtime --id codex-local --host codex --executable /absolute/real/codex --expected-version 'EXACT NON-SENSITIVE VERSION'
traceary runtime registry list --runtime-dir /absolute/private/runtime
traceary runtime profile list --runtime-dir /absolute/private/runtime
```

ここでの `--db-path` は**既存**の worktree store の identity を指定します。
内容を開く・migration する・初期化する処理はありません。
起動直前にも Git common-dir 所属、canonical worktree/store identity、profile を再検証し、選択変更で既存 run を再 binding しません。

Host 識別子は `codex` / `claude` / `antigravity` / `grok` ですが、登録は**native interaction/recovery の認定ではありません**。
明示された実 executable の canonical path、物理/content witness、非機密の正確な version を固定します。
Probe は固定の `--version` のみで、stdin/PTY なし、probe timeout 2 秒、出力 4 KiB 上限です。
静的な不正指定は path 参照前に拒否し、probe 前に current UID/root 所有、安全な ancestor、regular executable mode、witness を検証します。
未知・変更 version は fail-closed とし、生の probe 出力を error に返しません。

対話起動 argv は現時点では空に固定します。
管理 client は executable / argv / env / cwd / store を上書きできません。
Host 固有の起動 policy は version-bound adapter の作業で認定し、wrapper を挟まないでください。
認証・subscription/課金・model default・承認判断は実 CLI が担当します。
継承 env は memory 内だけに保持し、ambient Git routing と Traceary store/runtime hint は固定 selection の値へ置換します。
HOME や global host config は変更しません。

## 起動と receipt の参照

`traceary daemon serve --runtime-dir /absolute/private/runtime` を明示起動し、
`traceary daemon status --runtime-dir ... --json` で現在 epoch を確認します。
登録 ID と自分で指定した一意の request ID を使います。

```sh
traceary runtime launch --runtime-dir /absolute/private/runtime --epoch CURRENT_EPOCH --worktree w_ID --profile codex-local --request-id operator-request-1
traceary runtime list --runtime-dir /absolute/private/runtime --epoch CURRENT_EPOCH
```

`launch` は typed 管理 request を 1 回送信し、optional hook delivery とは分離します。
Client 全体予算は 8 秒、optional 観測は 250 ms のままです。
Server の handshake/frame 受付は有界とし、有界な管理操作後に write deadline を更新します。
送信後の transport loss は **unknown** として元の public epoch/request ID を返します。
「起動していない」と解釈したり、自動的に新しい request を送ったりしないでください。
同じ epoch・request・target/profile を明示参照すると元の receipt を返し、別 child は作成しません。
同じ key の別 target は拒否します。
再起動後の旧 epoch は stale であり、自動 adopt しません。

Run / generation / epoch / immutable target/profile / diagnostic PID / process state / exit code / publication uncertainty は metadata です。
PID は操作 handle ではなく、process `live` は AI の working/waiting/approval や入力権限を意味しません。
List は最大 2 run、かつ JSON escape 後の 64 KiB wire 上限に収めます。
`next` を `--cursor` に、同じ `--revision` を指定して継続し、revision 変更時は混在 snapshot を返さず拒否します。
大きすぎる descriptor は起動前に拒否し、起動後に切り詰めません。

受付上限は active/pending 所有予約 256、epoch ごとの request receipt 1,024 です。
上限でも既存 receipt を隠さず、不確定 receipt を削除して新規作業を受け付けません。
永続 journal にも上限があり、owner-private な reserved intent を起動前に commit します。
Commit 前の中止は未起動予約を破棄できますが、その境界後の cancel は request を消去せず child を kill しません。
起動後の journal 障害では所有権を保持し、unknown を返して replay を防ぎます。

## 所有・中止・永続化

PTY は新しい child session の制御端末です。
Client がなくても出力を drain します。
この slice は出力を破棄し、ring/cursor/reconnect は別の #2462 であり、完了したとは扱いません。
実際に所有する `exec.Cmd.Wait` が終了を 1 回だけ確認し、保存 PID や外部 CLI 観測だけで attach/signal/adopt しません。

Foreground Serve の割り込みは**新規**起動受付を停止します。
所有 run が残る間は `draining` として lease と管理 endpoint を保持し、実 child の終了と journal 照合を待ちます。
生きた PTY の close や agent の暗黙終了は行いません。
既存 receipt・status/list は参照できます。
Child の引き渡しが不確定なら予約を保守的に保持します。
明示 target-bound stop (#2463) と二段階の計画停止 (#2464) は別要件であり、この作業で実装済みとは主張しません。

Profile と reserved/live/exited journal は pinned 0700 root 内の versioned 0600 metadata です。
Registry と OS lock、および committed metadata 合計 128 MiB 上限を共有します。
有界な一時 file、sync、rename、parent directory sync で更新し、生の env / prompt / PTY bytes は保存しません。
Lock 初回作成は no-follow の既存 open / exclusive create を分離し、EEXIST 競合だけを有界に処理します。
Permission 修復や pathname fallback はありません。
同一 UID の敵対 software、異 UID/ACL、実 host の挙動は合成 fixture で認定したとは扱いません。

Daemon crash では PTY が閉じ、agent が終了し得ます。
同じ native conversation の安全な明示再開は恒久仕様 #2478 です。
この launch journal だけでは recovery record にならず、自動 resume/replay を許可しません。
UI 採用・配布は対象外、Muse native 作業は保留です。

Rollback は新規起動を無効にし、active 所有権と確認済み metadata を保持します。
停止成功を装うための一括 kill、user store/worktree 削除、不確定 owner の unlock は行いません。
