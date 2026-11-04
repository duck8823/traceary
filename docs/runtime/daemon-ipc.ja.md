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

statusは`protocol_version`、新しい32桁hex `epoch`、`state: listening`（新規起動受付停止中は`draining`）、`capabilities`、`observation_delivery`を返す。
**listeningはtransportの準備状態であり、agentのrunning/working/waiting/approvalや入力権限ではない。**
既定 CLI の legacy observe receiver は未接続のため observe は unsupported を返す。managed 構成には selected untrusted intake（observe_selected_v1）があり、configured は native origin の認証ではない。legacy observe の対応は個別 capability で確認する。Managed runtime構成ではtyped launch/listのcapabilityをadvertiseするが、native状態の認定ではない。
native状態を受け入れたと偽装しない。
applicationはtyped receiver portを注入できるが、queuedはtransport queueだけを意味し、永続化/因果binding/解釈の成功ではない。

## Filesystem、process、peer境界

current UID所有0700 rootをno-followで辿り、registryと共有する安全なdirectory実装でdescriptorを固定する。
有界process内claimとprivate 0600 `daemon.lock` OS lockで協調する重複instanceを排除する。
要求処理前にlease identityを再検証する。
stale socketを自動unlinkせず、既存pathは起動拒否とする。
第2endpointが起動できなければ、第1の所有endpointだけを片付け、競合resourceを変更しない。

0600 socketのroleを固定する。

- `daemon.sock`：名前付き管理method（statusおよびtyped managed launch/list）。汎用exec/file/reflection/任意shell/暗黙process操作は提供しない。
- `observations.sock`：固定 derived metadata の observe、observe_selected_v1、coarse history hint。input/stop/approval/Memory mutation/管理status/role変更を許可しない。

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
- Status/Observe/ObserveSelected clientはpath検査/dial/handshake/write/ackを含む単調時計250 msの任意予算。blockし得る検査はprocess全体32 slotで有界化し、実際に終了するまでslotを保持する。timeout後にworkerを無制限生成せず、遅延sendもしない。
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

## Managed 起動との接続

CLI の typed `launch` / `list_runs`、8 秒の明示管理予算、active child の所有権を保持する Serve の `draining` は [managed 起動](managed-launch.ja.md) を参照してください。
Default CLI の legacy consumer は未接続のままで、`observe` は `unsupported` です。managed Runtime は selected intake を提供します。起動 capability は native interaction/recovery 認定を意味しません。

## Selected untrusted observation V1

typed SDK と managed daemon の source capability を実装しています。public CLI/hook と native 認定は未完了です。operation observe_selected_v1 は一つの selected_observation arm と `{selection:{epoch,run,generation,worktree,profile}, metadata:{host,host_version,event_id,kind,native_session_id?,native_turn_id?}}` を使います。推測した target_id、PID、command、approval、nested version は持ちません。別 operation から legacy observe への再送はしません。必須 field、正確な canonical 名（Unicode case alias も拒否）、重複/null/未知 field と raw nested value 4 KiB を検査します。outer frame は 64 KiB のままです。

observations socket のみで受理し、実 Runtime の in-memory owned identity・固定 host/version・initial publication 成功を共有 queue の前で照合します。ACK 前に Registry/Git/filesystem を待ちません。既存の単一 worker が有界 context と owner lock 外で物理 source を再検証し、同じ owner を再確認して一つの volatile untrusted sample を保持します。失敗は sample を捨てて degraded とし、新しい file/core event/Registry/spool/drainer は作りません。最後に到着した telemetry を native binding・現在の interaction・入力権限・承認に昇格させず、native 証拠は後続 adapter が担当します。

queued は canonical header と request ID が一致する admission-only bare ACK であり、消費・core 永続化ではありません。data/role が混在する ACK を拒否します。旧 daemon の未知 arm は相関しない invalid となり、新 SDK は request ID を緩めず protocol error として扱い、再送/fallback はしません。単一の単調時計 250 ms は安全な root 検査・接続・handshake・write・ACK 全体を含み、transport slot は worker の実終了まで保持します。queue/frame/byte 上限と loss 診断を共有します。capability は受付可能な Runtime がある場合だけ広告し、native host は認定しません。[現在の配送契約](native-observation-delivery.ja.md)に CLI/hook の残件と検証範囲を示します。
