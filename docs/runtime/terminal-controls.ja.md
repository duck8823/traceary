# 所有terminalの明示操作

[English](./terminal-controls.md)

privateなローカルmanagement socketは、`input`、`resize`、`stop`、`receipt`を提供します。現在の`(epoch, run, generation, worktree, profile)`の完全一致と、runtimeが実際に所有する子プロセスが必要です。保存PID、公開ID、observation telemetry、旧epochは操作権限になりません。両peerでOS UIDを検証しますが、同じownerとして動く悪意あるソフトウェアからの隔離は保証しません。

## CLI

`runtime launch` / `runtime list`の選択を指定します。

```sh
traceary runtime input --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id> \
  --manual-terminal < /path/to/operator-selected-input

traceary runtime resize --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id> \
  --sequence 1 --rows 31 --cols 101

traceary runtime stop --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id> \
  --acknowledge-work-loss

traceary runtime receipt --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --request-id <request-id>
```

入力は**stdinだけから1〜16,384 byte**を読みます。機密入力をargvやshell historyへ入れないでください。CLIはEnter/改行を付加せず、byteを解釈せず、review batchのpasteやnative承認の自動回答もしません。`--manual-terminal`はoperatorの明示意図の分類であり、callerが人間だという暗号学的証明ではありません。waiting/approval observationからこの権限を生成しません。認証・subscription・model既定値・承認判断はhostが所有します。

resizeは正規形10進文字列の厳密な次sequenceを要求します（初期owner acknowledgmentは`0`、24×80）。行・列は1〜65,535です。native ioctl成功後にだけowner acknowledgmentを進めます。同一receiptの再照会はioctlを再実行せず、新規requestによるsequence再利用・飛び越しは拒否します。子の自己resize・出力テキスト・capture終了からacknowledgmentやprocess終了を推論しません。

**stopで作業を失う可能性があります。** 元のowned launch process groupへ固定SIGTERMを送ります。任意groupや現在のforeground groupを選択しません。group内の子孫には影響しますが、別session/groupへ逃げた子孫をsweepしません。leader終了に伴うkernelのcontrolling-terminal hangupがforeground groupへ影響することがあります。自動SIGKILL昇格、orphan signal、一括kill、旧PID採用はありません。deliveredのstop receiptはkernelへのsignal試行であり、**process終了ではありません**。終了は実際のowned Waitで別途確認します。

## Receipt、不明結果、上限

request IDは非機密の相関参照です（ASCII英数字・`-`・`_`の1〜128文字）。同一intent/content/selectionの再利用は元のreceiptを照会し、子の終了後・disk witness変更後・admission枯渇時でも入力を再送しません。同じIDの内容・intent変更は拒否します。unknownを同じIDまたは新IDで自動再送しないでください。receiptを確認し、状況を理解したoperatorが明示判断する必要があります。

安全なJSON出力はselection、request ID/kind、正規形10進文字列の`input_bytes` / `written_bytes`、resize evidence、`pending` / `delivered` / `unknown` / `rejected`を含みます。既知のwrite prefixはnative側の理解やterminal処理のexactly-onceを保証しません。部分write・割り込み、送信後timeout、ACK欠落・不正・矛盾、publication不確実はunknownです。既知writeがゼロでも「何も届いていない」とは限りません。receipt照会はread-onlyで、処理中のpendingはdurable intentのcommit完了を保証しません。

新規操作はreservation前に登録target/store、保存profile、固定済みexecutableのdisk witnessを再検証します。一致する既存receiptは新規effect用の再検証を迂回して照会します。coordinatorは全体最大32、各owned runは同時1操作で、超過はbusyです。無制限queueは作りません。callerキャンセルやstopによる子の先行終了があっても、admit済みcoordinatorのreconciliationまで所有権を保持してjoinします。effect/admission予算8秒と、別途最大2秒のmetadata reconciliationがあります。割り込めないOS I/Oを強制キャンセルできるとは主張しません。

各runは最大1,024 receiptで、input+resizeは最大1,008、lifecycle用16枠を残します。unknown evidenceはevictしません。確実なprecommit失敗では未commit reservationだけを破棄し、intent commitが不明ならunknownにしてnative effectを行いません。epochごとのHMAC競合検出値はメモリ内だけです。privateなversion付き`receipts.json`はmetadataだけで、raw byte、content hash、HMAC key、environment、PIDを保存しません。単体64 MiB、既存root全体128 MiBの上限を共有します。予約枠はdisk空き容量の保証ではありません。旧epochは履歴metadataだけで、live操作権限へ復元しません。

## Native安全性と認証範囲

Darwinは非reapのkqueue終了witness、Linuxはpidfd pollを使います。すべてのnative effectと永久的なcontrols-closed latchは同じmutexを共有し、唯一の実Waitがreapする**前**にlatchを確立します。FDはpump完了後に閉じます。spawn/control前にkernel SIGCHLD dispositionをread-onlyで照会し、ignored/NOCLDWAITを拒否します。process全体のsignal設定を書き換えません。未対応signal ABI（現状Linux MIPS）はfail closedです。watcher失敗時は操作admissionを閉じ、実際の子のWaitは保持します。pure-Go Darwin syscallは検証済みold-action ABIをread-onlyで使い、利用不能なら拒否します。private runtime/linknameやCGoへの迂回ではありません。

この保証は、信頼するstandalone daemonが唯一のreaperで、外部のin-process signal設定変更者がいないことを前提とします。同UIDの悪意ある処理・debuggerからの隔離は保証しません。合成PTY/socket/CLIテストを実host、native session再開、UI/IME、VoiceOverの認証には代用しません。安全な同一native sessionの明示再開は別途必須であり、ここで自動resumeや入力replayを導入しません。

rollbackはmutating admissionを無効化し、read-only list/attach、実際の所有権、安全なreceiptを維持します。unknownを自動再送で解決しないでください。
