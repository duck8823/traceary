# Owner限定のローカルDaemon IPC

[English](daemon-ipc.md)

[R0](../adr/2026-11-03-headless-runtime-r0.ja.md)のtransport部分を実装する。
agent起動、PTY所有、native待機状態の解釈、session再開は別の実装/認定leafとして残る。
wrapper、TCP listener、model request、core store migration、login/設定変更は追加しない。

## 明示operator command

```sh
traceary daemon serve --runtime-dir /absolute/private/runtime
# 別terminalから:
traceary daemon status --runtime-dir /absolute/private/runtime --json
```

serveは中断までforegroundで動作し、login auto-startや暗黙のbackground launcherではない。
statusはDaemonを起動しない。
root既定は`$XDG_STATE_HOME/traceary/desktop/runtime`、未指定時は`~/.local/state/traceary/desktop/runtime`。
絶対・安全・Unix socket path長上限以内を要求する。
symlinkやprivate-root mode不適合は黙って修復せず拒否する。
`--db-path`はDaemon optionではなく、このnamespaceはcore storeを選択しない。
core store path/config bootstrapより前にDaemon専用compositionを選ぶため、明示rootはHOMEなしでも動作する。
core reclaim検査も除外する。

statusは`protocol_version`、新しい32桁hex `epoch`、`state: listening`、`capabilities`、`observation_delivery`を返す。
**listeningはtransportの準備状態であり、agentのrunning/working/waiting/approvalや入力権限ではない。**
既定CLIはnative receiver未接続なのでstatusだけをadvertiseし、観測にはunsupportedを返す。
native状態を受け入れたと偽装しない。
applicationはtyped receiver portを注入できるが、queuedはtransport queueだけを意味し、永続化/因果binding/解釈の成功ではない。

## Filesystem、process、peer境界

current UID所有0700 rootをno-followで辿り、registryと共有する安全なdirectory実装でdescriptorを固定する。
有界process内claimとprivate 0600 `daemon.lock` OS lockで協調する重複instanceを排除する。
要求処理前にlease identityを再検証する。
stale socketを自動unlinkせず、既存pathは起動拒否とする。
第2endpointが起動できなければ、第1の所有endpointだけを片付け、競合resourceを変更しない。

0600 socketのroleを固定する。

- `daemon.sock`：名前付き管理method（現時点はstatus）。汎用exec/file/reflection/任意shell/暗黙process操作は提供しない。
- `observations.sock`：固定derived metadataのobserveだけ。input/stop/approval/Memory mutation/管理status/role変更を許可しない。

client/server双方がnative peer UIDを検証する。
macOSは`LOCAL_PEERCRED`/`Xucred`のnative ABI version 0、Linux CI fixtureは`SO_PEERCRED`を使う。
他platformはtransport unsupportedでfail closedとし、既存CLIのbuildは維持する。
UIDは特定native hostの証明ではない。
同UIDの悪意あるsoftwareや未検証ACL/異UID caseを認定済み隔離境界としない。

listenerの自動path削除を無効化する。
cleanupは固定rootとowner/type/device/inode一致を確認して所有socketだけをunlinkし、観測した置換file/socketは保持する。
path検査で同UIDの敵対的raceを完全隔離したとは主張しない。
ユーザーworktree/store/historyを削除しない。

## V1 canonical frame

4 byte unsigned big-endian byte長に続くUTF-8 JSON object、prefixを除き1–65,536 byteとする。
不正UTF-8、重複/大小文字alias key、過度なnest、未知field、v1のnull値を拒否する。
未知versionはunsupportedとし、互換schemaを推測しない。

handshake要求:

```json
{"protocol_version":1,"request_id":"example-hello","operation":"hello","role":"management"}
```

socketでroleを固定し、roleを書き換えて昇格できない。
hello後は新しい有界request_idと明示operationを要求する。
statusはoperation=status、observationなし。
observeはoperation=observeと固定object（host、host_version、run_id、generation、epoch、target_id、event_id、native_session_id、kind、任意native_turn_id）を使う。
値は有界なprintable opaque identifierであり、raw prompt/code/audit/terminal本文やcommand引数ではない。
receiverはhost/version/native witness/generation/順序を独立検証し、これらのfieldやepochから操作権限を生成しない。

応答はversion、一致request ID、固定code（ok/queued/invalid/unsupported/busy）とし、statusには型付きreadiness objectを付ける。
不正/途中切断/oversize frameはackなしでcloseしてよい。
connection内の重複request IDを拒否し、最大1,024 IDとする。
connectionを越えるexactly-once実行やnative event重複排除の保証ではない。

## Admission、時間、失敗

- Daemon単位で管理32 connection、観測32 connection。超過はhandler goroutineを増やさずbusy拒否。
- 観測queueは256 frameかつ8 MiB、先に達した方で拒否。consumerは1 worker。queue/consumer失敗はobservation_delivery=degradedを保持し、native interaction状態や欠落回復の証明にしない。
- handshake/frame deadlineは最大1秒。constructorで厳しくできるが固定上限を拡大しない。
- Status/Observe clientはpath検査/dial/handshake/write/ackを含む単調時計250 msの任意予算。blockし得る検査はprocess全体32 slotで有界化し、実際に終了するまでslotを保持する。timeout後にworkerを無制限生成せず、遅延sendもしない。
- 注入receiverはcancelを尊重する契約とする。不応答consumerが残れば有界root/lease claimを終了まで保持し、transportが無期限待機したり不明instanceへ第2ownerを入れたりしない。

frame拒否/transport errorでcore DB初期化/query、host stdout/approval判断変更、capture成功判定を行わない。
native hook転送、receipt/spool互換性、状態投影、active run shutdown、安全なnative session再開は後続所有者の責務であり、検証済みとしない。
raw frame、env、receiver error本文をlog/diagnosticへ出さない。

## 証跡とrollback

実native Unix peer双方向、HOMEなしの別process CLI serve/status、schema/role拒否、frame上限、slow/truncated peer、重複ID、count/byte圧力、lease/path置換、重複起動、cleanup保護、client deadline、fuzz seedを試験する。
Windows cross-buildは既存CLI互換性でありWindows Daemon認定ではない。
異UID/ACL native認定は明示的に未実施で、同UID試験から合格と推測しない。

rollbackはadmission/connectionを停止し、確認済み所有socketだけを削除する。
registry/core/history/Memoryと競合artifactを保持する。
transport停止はagent停止の許可ではなく、active run lifecycleの別所有者が統合runtime認定前に実装する。
