# UIから独立したローカルruntime checkpoint（R0）

[English](2026-11-03-headless-runtime-r0.md)

- 状態：**Proposed。独立レビューと人間による明示的な受入が必要**。
- 設計Issue：[#2471](https://github.com/duck8823/traceary/issues/2471)。
- 親：[#2414](https://github.com/duck8823/traceary/issues/2414)。
- 基準：`dc6055f6545749c449708f621217a60bc4a105f7`。
- 対象：macOS、ローカルownerのみ。公開TCP、remote relay、cloudは含めない。
- 採否：主sessionでの設計会話と人間の受入。goal、fixture成功、レビュー推奨、PR作成だけではR0を受け入れたことにならない。

## 要求と判断の境界

2026-11-03、ユーザーはDaemonとUI採用の分離を明示承認した。
headless Go daemonはclientのclose/reopenを越えてmanaged agent processとPTYを所有し、有界出力と明示的な対象限定操作を提供する。
nativeの証拠があるinteraction状態だけを投影する。
Daemon不在でも既存CLIの取得は継続する。
認証、課金、権限、承認判断は実AI CLIに残す。

従来のDesktop D0一括停止を、**headless scopeだけ**R0に置き換える。
[UI D0 #2424](https://github.com/duck8823/traceary/issues/2424)は開いたままとし、Electron/Wailsは未採用を維持する。
IME、VoiceOver、複数monitor等はUI/native統合trackの必須gateとして残し、R0の前提にはしない。
R0の設計・文書・隔離probeは今すぐ進められる。
製品runtime実装は、このcontractのレビューと人間による明示受入後に開始する。
過去の不合格、unknown、hardware gateを合格に変更しない。

非対象：wrapper、PATH shim、alias、shell-profile変更、必須`session run`、agent自動resume、credentialの参照・コピー、新しいmodel API、core store migration、Sessionのrunning/ended lifecycle、review自動送信、Memory自動採用、Desktop実装、署名、配布。

## 概念モデルと不変条件の所有者

| 概念 | 振る舞い・所有者 | 不変条件 |
|---|---|---|
| Session | 既存coreの履歴group | 時刻とIDは生存や権限ではない |
| Target | registryのcanonical project/worktree/store identity | 選択変更で起動済みrunを再bindingしない |
| Host profile | operator所有のexecutable/version/argv規則 | 管理clientはprofile IDを渡し、任意executable/argv/envを渡さない |
| Run | runtime所有child handleと固定target/profile | 保存PID、cwd、会話IDだけでは入力・停止を許可しない |
| Generation | 起動ごとのidentityとdaemon epoch | 古いcallback/receiptで新実行を変更しない |
| Binding | descriptorと独立検証したnative witness | 環境変数hintやdescriptor pathの所持は認可ではない |
| Observation | adapter証拠、source identity、単調受信時刻 | 因果関係や版が不明ならunknown |
| Projection | 受理した観測のpure reducer | 表示状態から承認・入力の権限を生成しない |
| Input receipt | request identityと有界な配送結果 | 部分配送・中断はunknown、自動再送しない |

Process、interaction、capture health、client connection、commit済みhistoryを別軸とする。
`Stop`はturn boundaryであり、process終了や安全な自動入力の証拠ではない。
permission eventはtelemetryであり、人間向けpromptの表示とは限らない。

## 責務、package、interface

| 所有者 | 担当 | 担当しないもの |
|---|---|---|
| 新`domain/runtime` | run/target/generation identity、投影・上限の不変条件 | SQL、socket DTO、OS child handle |
| 新`application/runtime` | consumer単位のregistry/runtime serviceと狭いport | CLI表示、protocol parse、native syscall詳細 |
| 新`infrastructure/runtime` | private metadata、Unix IPC、PTY/child所有、host adapter I/O | core Session lifecycle、課金・認証判断 |
| 既存`application/usecase`・query service | history/Memory/refinementの規則とcore永続化 | terminal buffer、registry layout |
| `presentation/cli` | 明示operator command、検証したprotocol変換 | native状態の推測、巨大service logic |
| 後続native UI | 型付きallowlist consumer、人間操作 | raw SQLite/socket/file/shell、credential |

これは責務境界であり、汎用plugin frameworkではない。
public contractを変えない範囲で各Issueのfile配置を詳細化できる。
既存`application/usecase/memory_usecase.go`の`MemoryUsecase`と実装、`presentation/cli/session_run.go`を比較する。
RPCごとにusecase classを作らず、Registry/Runtime serviceの対象単位methodとする。
対話PTYを既存one-shot runnerへ追加しない。
core domain/usecaseからruntime infrastructureへ依存させず、entry pointで組み立てる。

## Operator surfaceと互換性

追加command groupは`traceary daemon`（serve/status/shutdown計画）と`traceary runtime`（registry、profile、launch/list/attach/input/resize/stop）とする。
個々のflagとJSON出力は担当実装Issueで出荷前に固定する。
`traceary runtime observe`は狭いhook受付であり、汎用command runnerや承認endpointではない。
既存hookのstdout/判断、exit、redaction、receipt/spool contractは不変とする。
hookの記録を副作用としてDaemonを自動起動しない。

operatorがlocal CLIで実絶対executableと固定argvをhost profileへ明示登録する。
Daemonは起動時にprofileをsnapshot・検証し、信頼しない管理callerによるexecutable/argv/cwd/任意env/store pathの上書きを認めない。
登録target IDを使う。
実CLIは継承したhost認証を利用できるが、Daemonはsecretを検査・serializeしない。
保存は安全な起動metadataとallowlist対象の非機密override名だけとし、env値やraw terminalを保存しない。
HOMEやglobal設定を置換して隔離を装わない。

## Version 1 local IPC contract

`$XDG_STATE_HOME/traceary/desktop/runtime/`、既定`~/.local/state/traceary/desktop/runtime/`にowner-private Unix socketを2つ設ける案とする。

- `daemon.sock`：型付き管理要求と出力購読。
- `observations.sock`：derived metadataのみ。input/stop/approval/Memory操作なし。

rootは絶対canonical pathとし、private runtime pathのsymlink component、UID、信頼する祖先のgroup/world writableを検査する。
bind/connect前にOSのsocket path長、directory 0700、socket 0600を検査する。
unsafeな既存path、owner/mode相違、稼働instance競合を拒否する。
他processのsocketをunlinkしない。
cleanup時にowner/device/inodeを再検証し、path検査だけで同一UIDの敵対的raceを完全防御したとは主張しない。
対応OSではdescriptor-relative/no-follow操作を使い、identityを検証できなければ拒否する。
client/server双方がpeer UIDを検査し、macOSでは実OSのpeer APIを使う。
失敗をprocess名やcwd一致で代替しない。

脅威境界は同じownerのlocal serviceであり、**ownerとして動く悪意あるsoftwareからの隔離ではない**。
OS peer identityは必須だが特定native hostの証明ではない。
公開run/session IDやsocket pathを独立した認可credentialとしない。
native観測bindingには信頼できるhost/version witnessが必要で、曖昧ならunbound/unknownとする。

frameは4 byte unsigned big-endianのbyte長に続くUTF-8 JSON objectとし、prefixを除き最大65,536 byteとする。
oversize、途中切断、不正UTF-8、未知field、schema不適合はframe全体を拒否し、先頭部分だけを受け付けない。
handshakeでprotocol version 1とroleを宣言し、socket選択でrole/operation allowlistを固定する。
未知versionはmutation前に拒否する。
request IDを有界化し、管理targetにはepoch/run/generation/固定target identityを含める。
responseはaccepted/rejected/unsupported/busy/gap/delivery-unknown/exitedを区別する。
英語diagnosticへraw payload/env/command引数/secretを含めない。
PTYのbinary byteは明示base64 fieldであり、状態として解釈するJSON textではない。

観測roleから管理roleへ切り替えられない。
管理は名前付きtyped methodだけとし、`exec`、任意file参照、generic method reflectionを設けない。
native UIのsender/frame検証は後続の追加境界であり、peer UIDで代替しない。

## 上限とadmissionの判断

以下はv1の保守的admission値であり、過去probeから導いた性能保証ではない。
変更は明示contract/test更新とし、実装時に固定負荷でmemory/overload挙動を証明する。

| 対象 | v1上限・結果 |
|---|---|
| 観測frame | 64 KiB、全体拒否 |
| CLI任意配送 | connect/handshake/writeを含む単調時計250 ms全体予算、best effort |
| 観測queue | Daemon単位256 frameかつ8 MiB、先に達した方でbusy拒否 |
| 管理connection | 同時32、超過はallocation前にbusy拒否 |
| run別PTY出力 | 2 MiB byte ring、eviction時cursorを進めgap明示 |
| subscriber出力 | 購読別256 KiB、run別最大4購読、遅いconsumerはgap付き切断 |
| active run | 256、新launchを拒否し既存runをevictしない |
| input request | decode後16 KiB、超過はwrite前に拒否 |
| receipt ledger | run別1,024件、本文なしmetadataのみ。16件をstop/lifecycle用に予約し、inputを先に拒否。不明receiptを忘れない |
| registry | project 100、worktree 1,000、超過mutationを拒否 |
| 永続runtime metadata | 全体128 MiB、version付きowner-private atomic write、超過拒否 |
| interaction投影 | waitingを含め最初の非重複受理から60秒 |

consumerが遅くても出力pumpはPTYをdrainして有界ringへ送る。
subscriberを無期限待機しない。
gapと欠落byte区間を明示し、evictionを越えた正確な配送を保証しない。
metadata予算とPTY buffer予算は別とする。
raw terminalは既定でmemory内のみ。
registry/receipt/run/ユーザーdraftを黙って消去しない。

## 起動、対象検証、明示操作

`Launch(profileID, targetID, requestID)`は登録canonical Git worktree/common-dirとstore identityをspawn直前に検証し、実binaryをshellなしで直接起動する。
変更・消失はfail closedとする。
所有child handleと固定descriptorを新run/generation/epochへbindingする。
有界request ledger内ではlaunchは冪等で、同時重複で2 processを作らない。
launch失敗/cancelはchild所有を照合してから成功・失敗を返す。
descriptor fileはowner-privateでsecret/model promptを含めない。

`Input`/`Resize`/`Stop`は現在target/generationと明示的な人間操作を要する。
保存PIDではなく、操作境界内でtargetとchild所有を再検証する。
人間が意図して操作するmanual terminal keystrokeはinteractionがunknownでも使えるが、それを根拠に自動review paste/Enterや承認判断を許可しない。
input receiptにrequestとwrite済みbyte数・結果を記録する。
部分write/crashの可能性があればdelivery-unknownとする。
重複・不明receiptを再生しない。
Daemon再起動後の旧epochはstaleであり、exactly-onceとして復旧しない。
Stopは所有process groupへの明示操作のみとし、descendantとwork-lossを説明する。
orphanへの自動signalやPIDでのadoptionを行わない。

外部起動は履歴/unbound観測だけでmanaged runとしない。
native session/resume IDは相関に限る。
native証拠で観測bindingしてもinput/approval権限は増えない。

## Detach、終了、crash、更新、wake

client close/crashはdetachだけとし、Daemonはchild handle/PTY masterを保持する。
reconnectではrun/generation snapshotと有界byte cursorを要求し、不明・evict済みcursorへgapを返す。
購読で新launchを行わない。
process exitは所有wait handleで観測し、native hookの解釈と分離する。

初期判断は**Daemon自身がPTY masterを所有し、別PTY holderを設けない**。
holderはprotocol/process所有境界を増やすため後続へ送り、crash復旧が無償だとは扱わない。
Daemon crashでmasterが閉じSIGHUP/agent終了/work-lossが起こり得る。
再起動時の旧active recordはunknown/orphanedで、保存PIDの再open/attach/kill/resumeを自動実行しない。
Daemon crash後の完全PTY復元は初期保証外である。
別holderは障害証拠付きの設計変更でのみ再検討する。

完全終了は現在epochとactive run集合へ固定したplan/confirmの2段階とする。
影響run、PTY close/SIGHUP/終了/work-lossを表示しcancel可能にする。
run集合が変われば旧planを拒否する。
確定shutdownと新launchを競合させない。
window closeを完全終了へ変換しない。
OS logout/shutdownのflushはbest effortで、安全終了の保証ではない。

active runがあればdaemon/protocol更新を延期し、更新試行時と24時間後に通知するが強制停止しない。
auto-startはopt-in、既定offで、agentは起動しない。
launchd job導入やlogin設定変更は別の明示scope許可を要し、既定ではdescriptorを隔離検証する。
sleep/wake/gapでinteraction freshnessを失効させる。
heartbeatではなく新しい因果的にboundな証拠を要する。
native sleep/wakeは実macOS証拠を要し、合成時計はreducer contractだけを検証する。

## Capture、native witness、投影

historyは既存CLI pipelineが保存する。
任意live配送で永続化を妨げたりhookを二重記録したり判断stdoutを変えたり、全体任意予算を超えてhookを遅延させたりしない。
global/per-run native hookはユーザーhandlerを置換せず合成し、未検証の合成はunknownとする。
global設定上書きやhost trust/auth変更は許可しない。

adapterはhostと正確なversion単位とする。
launch descriptorは相関hintで、adapterが独立したnative session/turn/generation証拠を検証できる場合のみlive観測を受け入れる。
env伝播、callback受信時刻、PID親子関係、cwd一致だけでは足りない。
受信直後でも因果関係が曖昧ならunknownとする。
受信単調時計は投影寿命を制限するだけでsourceからの配送ageを証明しない。
重複はTTLを延長しない。
古いwaitingは時刻付きの最後の観測として保持できるが、現在waitや権限としない。
terminal byteや無音からinteractionを推測しない。

Codex/Claude Code/Antigravity/Grokは#2468配下のhost別leafで独立認定する。
全状態を対応できるという約束ではない。
信頼できるwitnessがないhostは理由付きunbound/unknownのままとする。
Muse native検証・capture追加は保留し、Gemini/Kimiも初期認定対象外とする。
未対応状態はIPC/PTY基盤を止めず、合成fixtureで認定済みにしない。
host auth失敗後にengineや課金経路を暗黙変更しない。

## Registry、history、maintenance

#2472が唯一のheadless registry backendを持ち、Desktop #2431は同じserviceを使う。
canonical identity、duplicate/missing/foreign path、明示export/delete、admission上限はRegistryが所有する。
metadata削除でGit worktree/branch/core storeを消さない。
profile登録は別の明示operator操作であり、rendererへ任意shellを公開する機能ではない。
起動ごとにprofile snapshotを固定し、編集で既存runを再bindingしない。

runtime metadataは既存SQLiteから分離したowner-private/version付きenvelopeとし、安全なregistry/run/receipt summaryだけを含める。
不明receiptとorphan recordはoperatorの明示cleanupまで残し、超過時に黙ってevictせず拒否する。
atomic replaceはowner/no-follow/sizeをcommit前に検証し、失敗時に直前の正常fileを残す。
新しいschemaはunsupportedとし旧codeで勝手にmigrationしない。
export/delete cancelでユーザーデータを変更しない。
PTY出力を永続化しない。

core historyは既存query/usecaseとlease対応connectionを使う。
commit後通知はhintであり、欠落時に再snapshotする。
既存日時cursorを永続commit順の証明にしない。
新outbox/sequence/core schemaを追加しない。
readerを有界化し、upgrade/compactionでは解放してbusyを明示しretryするがPTYを維持する。
spool drainerを追加せず既存receipt/idempotency/claim所有を維持する。
新cursor/drainerが必要ならR0の拡張ではなくschema/claim互換性checkpointへ分割する。

## 脅威モデルとerror contract

terminal escape、hook本文、IPC frame、profile参照、filesystem path、保存metadataはuntrustedとする。
rendererを信頼しない。
出力はdataであり実行markupや権限証拠ではない。
OSC clipboard/linkを自動操作しない。
diagnostic codeと安全IDだけを記録し、raw input/argv/secretを含むenv/native payloadをlogに出さない。

| 条件 | 必須の観測可能な結果 |
|---|---|
| unsafe socket/peer/path/version/schema | 拒否/unsupported、mutation・foreign unlinkなし |
| Daemon不在・任意配送timeout | 既存history/判断contract不変、live unavailable |
| stale epoch/generation/foreign target | control拒否、reducer不変またはunknown |
| 部分write・receipt gap | delivery-unknown、自動再送なし |
| queue/connection/metadata超過 | busy/limit error、silent evictionなし |
| 出力cursor gap | 欠落byte区間と新snapshotを明示 |
| 未対応host/witness/順序 | unbound/unknown、waitを捏造しない |
| maintenance | busy/retry明示、store lease解放、PTY維持 |
| client消失 | detach、child重複起動なし |
| Daemon消失 | work-loss説明、orphan/unknown、PID adoptionなし |

同一UID ownerが侵害された場合の隔離を保証しない。
OS peer/path検証ができなければlive配送/controlをunavailableとし、黙って弱めない。
これはinteraction証拠の未対応とは別の失敗である。

## 振る舞いtestとTDD順序

| 条件 | Red test・Green境界 | 段階 |
|---|---|---|
| UI framework未採用 | headless依存graphにUI gateなし | docs/graph |
| foreign/symlink/oversize/slow socket | mutationなしで全体拒否 | 実Unix IPC・fuzz |
| 同cwd、異run/generation | 誤targetへのinput/binding/stopなし | domain・実PTY |
| 重複launch/cancel/起動失敗 | 所有child最大1、正直なreceipt | runtime fixture |
| slow consumer/大量出力 | memory有界、child drain、gap明示 | byte-count統合 |
| detach/reattach/resize storm | 同generation、byte/ack identity一致 | PTY fixture |
| 部分input/crash/duplicate | unknown receipt、再送なし | fault injection |
| expiry後重複/reorder/wake | unknown、wait復活・Attention重複なし | fake monotonic clock |
| Daemon不在・global/per-run hook | history/receipt/stdout不変、二重eventなし | CLI・host/version実機 |
| maintenance・遅延history挿入 | lease解放、再snapshot、PTY維持 | 実SQLite |
| shutdown cancel/run集合変更 | 暗黙stopなし、旧plan拒否 | process fixture |
| native sleep/wake/approval/witness | host/version証拠、未実施は未実施 | native macOS |

順序はpure identity/projection Red/Green→registry永続化→Unix IPC→実PTY/control/fault→既存CLI capture/history→host別native probe→同統合SHA headless QAとする。
unit fixtureでUI/native/manual試験を合格にしない。
external test packageとpublic APIの観測結果を使う。

## Delivery、検証、rollback

| Issue | scope・依存 |
|---|---|
| #2471 | R0設計/文書/review/人間受入。UI前提なし |
| #2472 | R0後の共有registry backend |
| #2460 | R0後のowner限定IPC |
| #2461 | IPCとheadless registry後のPTY起動 |
| #2462 | launch後のstream/detach/reconnect |
| #2463 | stream/launch後の明示target control |
| #2464 | control後の終了/更新/wake |
| #2465 | IPC/launch後のhistory/maintenance |
| #2466 | IPC/launch後の任意観測受付 |
| #2467 | launch identity contract後のpure投影 |
| #2468 | intake/reducer後のhost/version leaf認定。Muse保留 |
| #2470 | 適用runtime/adapter後、同SHAのheadless統合 |

#2432/#2433は集約trackingであり、完了を待ち合う循環にしない。
D4は受入済みlaunch基盤#2461を使い、#2470を含むD3全完了を待たない。
UI D0/D1/D2は後続consumerであり、headless runtimeの祖先依存ではない。
実装leafごとにbranch/draft PRを作り、そのleafだけをcloseする。
親は必要なrelease/統合policyまで開いたままとする。

commitは`scripts/test-select-staged.sh`、docsは`make docs/check`と`git diff --check`を使う。
実装ではaffected package/reverse dependency、fresh final-head full CI/lint/security、統合SHAのwave E2Eを実施する。
exact command/input SHA/結果/log hash/未実施gateを残す。
性能はworkload/機材/上限を事前固定し、全process CPU/RSS、byte/gapを測定し、UI event-loop推定で代替しない。
本docs PRはnative成功を主張しない。
製品runtime実装前に独立High-risk design reviewと人間の明示受入を要する。
architecture PRのReady化/mergeは人間操作であり、一般的delivery goalから自動許可を生成しない。

rollbackは新launch/control/live配送を停止し、Daemon shutdown前に影響runとcancelを提示する。
既存CLI/hooks/history/Memoryとユーザーmetadataを維持し、worktree強制削除やorphan kill/resumeを行わない。
新core schemaは対象外で、将来migrationは別backup/restore checkpointを要する。
R0不採用なら製品runtimeは未実装のままとし、UI D0を代わりに採用しない。
