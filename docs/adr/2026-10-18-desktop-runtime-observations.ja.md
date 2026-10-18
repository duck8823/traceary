# Desktop の実行状態観測と macOS D0 checkpoint

[English](2026-10-18-desktop-runtime-observations.md)

- 状態：**提案。D0 未完了。人間の判断が必要なアーキテクチャ変更**
- 対象：[計画 #2424](https://github.com/duck8823/traceary/issues/2424)
- 親：[計画 #2414](https://github.com/duck8823/traceary/issues/2414)。開いたまま維持する。
- 基準：`c6c4d2dbb8790be33a4d6903a9b9e833cbdb2af5`。v0.53.0 は公開済み。
- 範囲：macOS、Codex、Claude Code、Antigravity CLI、Muse Code、Grok Build。

## 要求と非対象

Desktop は履歴を現在状態と混同せず、正しい worktree、端末、履歴、注意対象を表示する。
Desktop が不在でも既存 CLI の hook 記録を維持する。
認証、サブスクリプション、権限、承認判断は各実 CLI に残す。

wrapper、PATH shim、alias、shell 設定変更、必須の `traceary session run`、資格情報取得、端末文字列による状態推定、agent 自動再開、core schema 変更、製品 Desktop 実装は本 ADR の対象外。
Gemini/Kimi の履歴は表示できるが、初期の状態認定には含めない。
Architecture 可視化、cloud 同期、remote relay、GitHub へのレビュー投稿も対象外とする。

## 比較と採用案

| 方式 | 利点 | 判定条件 |
|---|---|---|
| Superset のような wrapper と hook | 起動対象を明示して通知を結び付けやすい | wrapper は不採用。明示的な実行 identity は参考にする |
| Claude Squad、agent-deck のような端末文字列やハッシュの分類 | 不足する通知を補完できる | 状態の正本としては不採用。表示変更や本文で誤判定する |
| native hook と限定したローカル観測 | 既存 Traceary と実 CLI の認証を再利用できる | **提案**。版別の証拠で確認できた状態だけを投影する |
| ホスト別 SDK/app-server | 構造化された入力や承認要求を扱える可能性 | ホスト別の後続検証。五ホスト共通 contract や API 課金への暗黙変更にしない |

Superset 自身もホストごとの通知対応差を説明している。
cmux は Codex の承認 hook を、操作可能な承認要求とは別の telemetry として扱う。
Conductor の公開 UI 資料から内部の状態分類方法は確定できない。
他製品の表示を Traceary の実機認定の代用にしない。

## 概念モデル

| 概念 | 状態と振る舞い | 不変条件 |
|---|---|---|
| Session | 既存履歴のグループとホスト会話 ID | 現在の running/ended 状態を所有しない |
| Run | store/worktree/host を固定した管理対象実行 | session ID と PID だけでは操作を許可しない |
| Generation | 起動・restart ごとの識別子。daemon epoch を含む | 古い実行が新しい実行を上書きしない |
| Binding | 固定した起動 descriptor と検証済み native session の対応 | 曖昧または他実行の対応は unbound |
| Observation | source、受信時刻、ホスト時刻、turn/request ID、event identity を持つ版付き観測 | 因果順の証拠を捏造しない |
| Projection | 受理済み観測からの純粋な状態投影 | 表示状態だけを生成し、権限は一切生成しない |
| Review snapshot | 変更前後の immutable content と anchor | 内容変更後は明示 reanchor まで送信しない |

Process、Interaction、Capture health、Desktop connection、History を独立した五軸とする。
Process は launching/running/exited/unknown、Interaction は working/waiting_input/waiting_approval/unknown を扱う。
`approval_requested` のような観測ラベルは、telemetry であり、人間に承認画面が表示されたという断定ではない。
Stop は turn boundary であり、process 終了や安全な貼り付けの証拠ではない。

## 責務と境界

| 所有者 | 担当 | 担当しないもの |
|---|---|---|
| native host/version adapter | native event の意味と制限 | ホスト横断の推測、承認判断 |
| 既存 Traceary CLI | redaction、保存、receipt/spool、任意の限定観測配送 | PTY 寿命、汎用 Desktop RPC |
| Go daemon runtime | child/PTY、run generation、対象再検証、状態投影、購読 | Session domain の lifecycle、モデル認証 |
| 既存 application service | 履歴、audit、memory 候補と採用、refinement と coverage | layout、live PTY buffer |
| native shell | window/menu bar、ユーザー操作、typed IPC | renderer への任意 shell/file 公開 |
| renderer | pane、terminal、diff、review draft、証拠表示 | SQLite、資格情報、raw socket、任意 command |

`presentation/cli/session_run.go` は one-shot の契約であり、対話 PTY の所有者に拡張しない。
`application/types/session_summary.go` は履歴の read model で、その時刻は生存の証拠ではない。
memory/refinement は既存の操作対象単位の usecase を保つ。
この ADR は core usecase を新設しない。

## wrapper なしの観測 contract 案

### 起動と binding

daemon は設定された実 executable を argv/cwd/env で直接起動する。
shell を前置したり PATH の agent 名を置き換えたりしない。
native hook は限定した Traceary 観測受付を呼ぶ。
その command 名と schema は、別の public contract 実装 PR で確定するまで提案に留める。

環境変数は配送ヒントであって認可ではない。
Muse のローカル echo probe では任意の起動環境変数が hook に届かなかった。
対応ホストでは、実行ごとの native hook 設定に owner-private な binding descriptor の path を固定する案を検証する。
global 設定を置き換えない。
descriptor は store、canonical worktree、host executable/version、run、generation、daemon epoch を結び付ける。
native session/turn ID は検証後に関連付け、secret として扱わない。
descriptor path や公開 run ID に入力や停止の権限を持たせない。
wrapper なしに一意な binding を検証できないホストは、履歴と unbound 観測だけを表示し、管理対象の live interaction は unknown とする。
cwd 一致や PID の親子関係だけで操作を許可しない。
CLI が起動前の新しい native session ID 指定に対応する場合、それも相関には使えるが認証には使わない。
resume で会話 ID を再利用しても generation は識別できない。
独立した generation の証拠がなければ、遅延 callback は曖昧なままで live 状態を更新できない。

### ローカル transport

socket は `$XDG_STATE_HOME/traceary/desktop/runtime/daemon.sock`、既定は `~/.local/state/traceary/desktop/runtime/daemon.sock` を提案する。
絶対 path、OS の path 長上限、owner UID、非 symlink directory、directory 0700、socket 0600 を確認する。
危険な既存 path は拒否し、他者の socket を unlink しない。
macOS の peer identity と version handshake、run binding を検証する。
同じ OS ユーザーの悪意ある process を完全隔離する境界とは主張しない。

観測受付に入力、承認判断、memory mutation、process 制御を含めない。
renderer IPC は別の allowlist とし、sender/frame、schema、対象 resource を検証する。
任意 shell/file/socket API を公開しない。

実装前の上限案は frame 64 KiB、CLI の単調時計で追加配送全体 250 ms、daemon 所有 queue 256 frame かつ 8 MiB とする。
60 秒は後述の投影 TTL であり、source からの配送 age の保証ではない。
超過や切断では Capture health を degraded、Interaction を unknown とする。
agent をブロックせず、hook 判断の stdout を書き換えず、既存履歴と spool の配送を妨げない。
これらは現行 CLI の保証ではなく提案値である。

### 順序、再接続、エラー

| 条件 | 観測できる結果の案 |
|---|---|
| daemon 不在 | 履歴記録は継続。live 配送は unavailable |
| unsafe socket、他 UID、他 binding | 観測拒否。入力や停止の権限は付与しない |
| 未知 protocol/host version | 理由付き unsupported/unknown |
| event identity 重複 | 状態と通知を冪等処理 |
| 古い epoch/generation、期限切れ | live 遷移拒否。適格な履歴は保存可能 |
| 因果順の逆転 | 古いと証明できる観測は無視。曖昧なら unknown |
| cursor 欠落や期限切れ | snapshot と gap を表示。無損失再開と呼ばない |
| store maintenance/lease 競合 | reader を解放し有界 retry。PTY は継続 |
| crash 後の入力配送結果不明 | indeterminate を表示。自動再送しない |

transport の受信 sequence は native の因果順ではない。
turn/request ID は並行 callback の順序まで保証しない。
wall clock だけで矛盾を解決しない。
reducer の合成テストを native host 認定と混同しない。

live 状態は daemon epoch ごとの snapshot と有界な memory cursor で扱う。
古い履歴や spool replay から現在の working/waiting を復活させない。
`presentation/cli/tail.go` の時刻 cursor は永続 commit 順 stream ではない。
遅延挿入された履歴は別に再照合する。
初期案は bounded な read refresh と明示 gap 表示とし、永続 sequence/outbox が必要なら独立した schema checkpoint に分ける。

doctor は明示診断に使い、heartbeat として poll しない。
store 初期化や host probe を伴い得るためである。
daemon は idle、upgrade、compaction をまたいで shared store lease を保持しない。
既存 receipt の所有権を保ち、claim/lease 設計なしに spool drain を競合させない。

## ホスト対応表と証拠の区分

D は公式の可変な文書、N は native binary の観測、F は local echo/合成 fixture を表す。
いずれも、それだけで対話実行が認定済みという意味ではない。
各セルの出典番号は末尾に示す。

| ホストと導入版 | 作業中の証拠候補 | 入力待ち | 人間の承認待ち | turn boundary | process 終了 |
|---|---|---|---|---|---|
| Claude Code 2.1.302 (N0) | prompt/tool (D1)、native 未確認 | 種別付き通知 (D1)、遅延あり、native 未確認 | permission_prompt (D1)、要求だけでは不足、native 未確認 | Stop (D1)、native 未確認 | child wait のみ。hook 単独は unknown |
| Codex 0.163.0 (N0) | prompt/tool (D2)、native 未確認 | 専用の証明は unknown (D2) | PermissionRequest は telemetry。人間への表示は unknown (D2,D6) | Stop (D2)、native 未確認 | child wait のみ。hook 単独は unknown |
| Antigravity CLI 1.4.0 (N0) | invocation/tool (D3)、native 未確認 | fullyIdle 単独では入力受付を証明しない (D3) | unknown (D3) | Stop と fullyIdle (D3)、native 未確認 | child wait のみ。hook 単独は unknown |
| Muse 1.4.10-R6427.1 (N0) | PreLLMCall を echo で観測 (F1,D4) | unknown。Stop だけでは不足 (F1,D4) | Notification の文書あり、native 未確認 (D4) | Stop を echo で観測 (F1) | F1 では SessionEnd 未観測。child wait のみ |
| Grok 1.0.62 (1474f9cbdd91) (N0) | prompt/tool (D5)、native 未確認 | main 文書に idle_prompt、導入版は未確認 (D5) | main 文書に permission_prompt、native 未確認 (D5) | Stop の文書あり、native 未確認 (D5) | child wait のみ。hook 単独は unknown |

N0 は空 project/HOME で実 binary の version/help を確認したもの。
PATH の claude は既存の print routing wrapper だったため、変更せず `~/.local/bin/claude` を直接使った。
Muse/Grok の導入と既存ログインはユーザーが確認済みだが、hook 配送の認定ではない。
ログイン自動化や資格情報のコピーは行っていない。

F1 は空 HOME、合成 prompt、`muse exec --provider echo --preset empty` と一時的な `-c hooks=...` を使った。
shell/write/web/reminder を無効にし、foreign personal context と session log を読み書きせず、approval-mode never、max-model-steps 1 を指定した。
SessionStart、UserPromptSubmit、PreLLMCall、PostLLMCall、Stop を観測した。
すべてで任意の環境 binding が失われ、SessionStart 以後には turn ID が存在した。
登録した SessionEnd は観測されなかった。
これは echo だけの証拠であり、Meta provider、入力受付、承認待ち、SessionEnd が常に未発火という主張ではない。

F2 は空 HOME/project を別々にした echo 2 実行を並行起動し、各 hook command の引数に固定した合成 binding を渡した。
各実行で SessionStart/UserPromptSubmit/Stop、計 6 観測を得た。
binding ごとに別の native session が一意に対応し、起動環境変数はどちらも復元されなかった。
これは native の実行別 hook 引数で相関を渡せた証拠であり、公開 literal に認証能力があることや Meta provider の認定を意味しない。
環境継承の代案であって wrapper や権限回避ではない。

Grok の build suffix は公開 repository の commit に解決できなかった（GitHub は 422）。
D5 は導入版に固定した source 証明ではなく、可変な文書のままとする。

## Desktop metadata と寿命

registry/layout/review draft は既存 store から分離し、独立 schema version、明示 export/delete UI を持たせる案とする。
初期上限案は project 100、worktree 1,000、active run record 256、review draft 100 件で各 1 MiB、全体 envelope 128 MiB。
超過時は理由を表示して新規 mutation を拒否し、下書きを黙って削除しない。
上限値も設計レビューの採否対象である。
raw terminal を既定で永続保存せず、run ごとの scrollback と出力 queue を有界化し、表示欠落を明示する。

window close は detach だけにし、再表示時は同じ managed generation に接続する。
menu bar を持つ UI process と headless daemon は別の寿命とする。
完全終了では稼働 run と影響を提示して cancel を許可する。
停止時は対象を再検証し、保存済み PID だけで kill しない。
UI crash で PTY を終了させない。
daemon crash 後の完全 PTY 復旧は初期保証に含めず、unknown/orphaned を表示し、発見した process の自動 kill/resume をしない。

更新は agent を暗黙停止しない。
active run 中の daemon/protocol 更新は延期または影響を示して確認する。
login auto-start は opt-in、既定 off で、agent を自動実行しない。
sleep/wake 後は再照合まで freshness を失効させる。
OS shutdown/logout の flush と復旧は best effort である。
Attention/OS 通知は受理した遷移単位で重複抑止し、prompt、code、audit、memory、private title を既定 payload に載せない。
通知権限が拒否されても app 内 Attention を使えるようにする。

## 脅威モデル

terminal escape、file/diff、hook payload、Markdown、audit を untrusted とする。
navigation、link、clipboard に制限を置き、自動 OSC clipboard 書き込みを無効にする。
Electron は renderer sandbox、context isolation、Node 無効、CSP、sender 検証を必須とする。
Wails も限定した binding が必要であり、native WebView だけで安全とはしない。
操作時に run/store/worktree/snapshot を再確認する。
新鮮な観測でも承認権限や agent によるレビュー理解の証拠にはならない。

## 技術 spike と採用 checkpoint

計測前の 2026-10-18 に、macOS 26.5 arm64、RAM 16 GiB、physical core 10、Electron 44.9.0、Wails v2.16.0、xterm.js 6.0.0、Monaco 0.57.0、esbuild 0.28.2 を固定した。
同じ bundle を使い、80x12 の terminal 3 個、scrollback 1,000 行、各 inflight 2 MiB、各 1 MiB/s を 60 秒、resize 200 回、diff 入力 100,000 行を 5,000 行に制限した明示 preview とする。

記録済み protocol に固定し、計測後は動かさない閾値は起動 5 回の記述的 p95 が 3 秒以下、操作 p95 が 100 ms 以下、idle 60 秒平均が logical CPU 1 個換算 5% 以下、agent を除く総 RSS が 1 GiB 以下。
5 回の測定から統計的確信は主張しない。
nearest-rank p95 は n=5 では最大値（今回の初回起動）であり、cold filesystem cache の証明ではない。
WebKit helper を含む process tree を記録する。
renderer event-loop delay を native 入力遅延の代用にしない。
package size は同等の package 同士で比較し、単独 executable と runtime 全体を比較しない。

**実装の判定は no-go であり、両 framework とも未採用とする。**
未完了の native gate を解消し、新しい採用判断が受理されるまで D1–D10 の実装を止める。
この否定的 checkpoint は D0 完了ではない。
日本語 IME の確定と取消、TUI/Unicode、clipboard、VoiceOver、複数 monitor、close/reopen と menus、sleep/wake、障害試験は必須である。
build、画面、合成 throughput だけで合格にしない。
spike code は production package 外に置き、製品実装として merge しない。
計測後に閾値を動かして合格化しない。

### 採用判定に使わないローカル計測

[最小化した証跡](evidence/desktop-d0-2026-10-18.json)に入力 hash、native/echo の区分、計測値を記録した。
両 native app が同じ xterm/Monaco fixture を build し、描画できた。
Wails は当該 build だけに `CGO_ENABLED=1` と `CGO_LDFLAGS='-framework UniformTypeIdentifiers'` が必要だった。
製品設定や framework source は変更していない。
package はローカル生成だけで、配布していない。

| 診断値 | Electron | Wails v2 |
|---|---:|---:|
| .app の新規 process 5 起動からの記述的 ready p95 | 2,724.65 ms | 1,483.88 ms |
| 同じ fixture を含む app bundle の非圧縮 regular file bytes | 305,440,958 | 11,240,190 |
| 直接 binary 起動の terminal 配送 bytes（必要量 188,743,680） | 188,743,680 | 20,761,881 |
| 直接起動 resize（必要 200） | 200 | 66 |
| 直接起動 renderer event-loop p95（入力遅延ではない） | 1.10 ms | 1,072 ms |
| .app 起動の terminal 配送 bytes（必要量 188,743,680） | 88,395,012 | 181,509,198 |
| .app 起動の resize 回数（必要 200） | 200 | 200 |
| .app 起動の renderer event-loop delay p95（native 入力遅延ではない） | 2.30 ms | 8.00 ms |

**.app の負荷試験は両方とも必要な配送量に未達だった。**
native focus/App Nap/timer を統制できておらず、Electron の .app 試験中には CUA による読み取り専用の window 確認も行った。
直接 binary 起動と .app 起動の結果も大きく異なる。
この結果から framework の速度順位や 3 MiB/s の terminal 経路の合格は主張できない。
生成器は timer 駆動の書き込みであり実 PTY stream ではないため、次は統制された実 PTY producer と明示的な foreground/background 条件で検証する。
負荷を減らして合格化しない。

ps の sample は app と探索できた child だけで、OS が切り離した WebKit helper を含まない可能性がある。
報告 CPU 値は EWMA であり、CPU time の積算ではない。
この sample や main 関数からの ready 時間で総 CPU/RSS/native 入力の gate を合格にしない。
external ready は process 起動から report までの単調時刻で、quick 起動の poll 間隔は 10 ms。
filesystem cache は消去していない。
native screenshot は描画だけを確認し、IME や VoiceOver の証明にはしていない。

### 改訂した native PTY fixture

第二試行も事前に protocol を固定し、3 terminal、各 1 MiB/s を 60 秒、各 source queue 2 MiB、200 resize、有界 5,000 行 diff preview を維持した。
ローカル broker は実際の raw PTY 3 本から読み、renderer は有界 read と xterm write acknowledgement を待つ。
直接起動した両 native variant は 188,743,680 bytes 全量、報告上の欠落 0、200 resize、source EOF を確認した。
所要時間は Electron 60,281.4 ms、Wails 60,269 ms だった。
terminal write-ack p95 はそれぞれ 22.60 ms と 10.00 ms だが、native 入力遅延や公正な framework 順位ではない。
same-UID、path、protocol、UTF-8 の smoke 2 ケースが合格したが、他 UID への耐性は未検証である。

負荷を減らさず、従前の timer producer の配送欠落を解消した。
同等 packaged build の再試行、全 process の CPU/RSS、native TUI/IME/clipboard/accessibility、crash/sleep-wake 検証の代用にはならない。
隔離した手動 fixture を CUA で再起動する試行も `timeoutReached` を返したため、手動試験の合格を主張しない。
別 GUI driver や OS 保護の回避は使っていない。
その後の read-only 署名検証では、コピーした自作手動アプリの署名不整合を検出した。
通常の ad-hoc 開発署名と strict 検証に成功した後も、CUA は `timeoutReached` を返した。
残る起動失敗の原因は不明であり、OS 保護を無効化していない。

## 振る舞いテストと TDD

| 条件 | 期待する観測可能な結果 | 段階 |
|---|---|---|
| 古い generation が新 run に届く | 新 run を変更しない | 純粋 reducer、Red 先行 |
| 重複、逆順、因果順不明 | Attention 重複なし。曖昧なら unknown | reducer/adapter |
| 背景作業中の Stop | 終了や安全な入力可能状態を捏造しない | native host |
| 承認 hook が自動解決 | 人間待ちを誤表示せず、判断を変えない | native host |
| daemon 不在、unsafe/foreign socket | 履歴継続、追加配送拒否 | CLI integration |
| 環境消失、未知版、foreign run | unbound/unknown。入力と停止を拒否 | binding/native |
| maintenance と tail 競合 | reader 解放と retry。PTY 継続 | store integration |
| reconnect、replay、cursor gap | snapshot/gap を表示し、古い待機を復活させない | fault E2E |
| review 対象や内容が変化 | stale anchor を拒否し、draft を維持 | unit/E2E |
| 入力配送途中の crash | 結果不明。自動再送なし | fault E2E |

まず隔離 spike で純粋遷移と binding invariant を Red/Green にし、その後 adapter、IPC、native vertical test を接続する。
host/version の差異を adapter に閉じ、巨大 handler や仮想的な汎用 plugin framework を作らない。
commit selector、final-head CI、wave E2E は CONTRIBUTING に従う。
docs-only 検証で native QA の合格を主張しない。

隔離した実行可能仕様で reducer の合成 16 テストが成功した。
期限切れの重複で working が残る回帰テストは先に失敗し、重複処理より前に freshness を確認する修正後に成功した。
実 daemon、native の因果順、peer identity、socket、store lease の検証ではない。

## 実装 Issue の分割と rollback

計画用 sub-issue は #2414 配下に作成したが、D0 の受入と framework 採否が確定するまで実装禁止とする。
起票は製品実装や native 認定の承認ではなく、要件を黙って免除しない。
各 Issue は単独 writer、専用 branch/draft PR、positive/negative AC、検証 command、rollback を持つ。
既存の受入条件を黙って緩めない。

| Package | 依存 | 受入条件と rollback |
|---|---|---|
| [D1](https://github.com/duck8823/traceary/issues/2430) Native read-only shell | D0 | 承認済み pane と有界 Audit/History/Memory。UI だけ無効化可能 |
| [D2](https://github.com/duck8823/traceary/issues/2431) Registry | [D1](https://github.com/duck8823/traceary/issues/2430) | canonical path、重複、消失。既存 worktree を保持 |
| [D3](https://github.com/duck8823/traceary/issues/2432) Managed daemon/PTY | D0,D2 | 起動と再接続を分割。実 CLI、detach、出力上限、明示停止。強制 cleanup なし |
| [D4](https://github.com/duck8823/traceary/issues/2433) Observations/Attention | [D3](https://github.com/duck8823/traceary/issues/2432) | reducer と adapter を分割。五ホスト版別、unknown、順序/binding/障害。観測だけ無効化 |
| [D5](https://github.com/duck8823/traceary/issues/2434) Worktree 作成 | D2,D3 | branch/path 競合、部分失敗。force delete なし |
| [D6](https://github.com/duck8823/traceary/issues/2435) Immutable diff | [D2](https://github.com/duck8823/traceary/issues/2431) | staged/unstaged/untracked、rename/delete/binary/large。snapshot/draft 保持 |
| [D7](https://github.com/duck8823/traceary/issues/2436) Review と safe Submit | D4,D6 | stale、対象再検証、手動 fallback、不明 receipt。送信無効化 |
| [D8](https://github.com/duck8823/traceary/issues/2437) Memory/refinement | [D1](https://github.com/duck8823/traceary/issues/2430) | 明示採用、scope/source/coverage、既存復旧。Desktop write 無効化 |
| [D9](https://github.com/duck8823/traceary/issues/2438) Native QA/internal beta | D4,D5,D7,D8 | 統合 SHA の security/E2E/障害/互換性。自動 publish なし |
| [D10](https://github.com/duck8823/traceary/issues/2439) OS/host 追加 | [D9](https://github.com/duck8823/traceary/issues/2438) | OS/版別認定。未対応は unknown |

最初の技術 slice は既存 worktree 登録、実 CLI 起動、terminal、detach/reconnect、証拠付き状態表示まで。
次に diff と review draft を加える。
構造化送信を初期 beta から分ける案は親 Issue の gate 変更なので、明示承認が必要である。

rollback は任意の Desktop 機能を停止し、既存 CLI/hooks/history/memory を維持する。
既存 store の migration は加えない。
将来 commit cursor/schema が必要になったら別 checkpoint と full-store backup/restore 計画を作る。
旧 binary の新 schema 読み取りを無条件保証しない。

## 独立レビューで明確化した規範

以下は提案 contract であり、実装済みの保証や D0 採用ではない。

### 鮮度、identity、帰属

- 250 ms の配送予算は CLI 内の任意処理だけを単調時計で制限する。daemon は受信時の単調時刻を記録し、host と daemon の wall clock を比較しない。host 時刻は診断用であり、受信時刻から callback の配送遅延は証明できない。generation/turn/因果順の証拠が不足する callback は unknown とする。
- live interaction の TTL は、working と waiting の両方で、最初に受理した非重複観測から保守的に 60 秒とする。重複では更新しない。期限後は unknown とし、時刻付きの「最後に観測した待機・stale」を別の Attention 表示として保持するが、現在の待機や権限とは扱わない。長い待機には新しい証拠が必要であり、合成 expiry 回帰テストもこの仕様に従う。
- 後続の受理済み観測、process 終了、epoch/generation 変更、sleep/wake、cursor gap で従前の投影を無効化する。reconciliation は新しい因果関係付き native 観測を受理するまで unknown とすることを意味する。履歴 replay、heartbeat、単なる process 生存では復元しない。
- adapter の event identity は、証明できる native event/request identity と run/generation/epoch を含む。内容 hash だけで別 turn を重複にしない。identity/順序を区別できなければ unknown とし、捏造した重複排除をしない。unbound 観測は run/pane を更新せず、その Attention を発火しない。投影は権限を一切生成しない。

### Binding と hook 合成の行列

| Host | 環境変数・実行別設定・事前 session 指定の binding | Hook 合成 |
|---|---|---|
| Claude | native unknown | native unknown |
| Codex | native unknown | native unknown |
| Antigravity | native unknown | native unknown |
| Muse | 環境変数は欠落。実行別 literal の相関は F2 echo のみ。native provider は unknown | global と実行別設定の native 合成は unknown |
| Grok | native unknown | native unknown |

binding 不明では履歴と unbound 表示だけを許し、管理対象 live interaction は unknown とする。
記録 hook を追加するより、既存の単一 handler に binding を渡す経路を優先する。
host ごとの加算・上書きの意味を検証し、履歴抑止と二重保存を拒否する。
D4 は当初 exact host version を認定し、自動更新後は再検証まで未認定とする。互換 range は推定しない。
ユーザーが既存 custom executable を絶対 path で明示選択することはできるが、Desktop が黙って unwrap・置換・新設しない。

### Transport と入力安全性

CLI と daemon の双方が接続先 peer UID を検証する。
user-owned state root から各 path component を検査し、symlink、不適切な owner/permission を拒否する。
runtime directory は 0700 で作成し、umask 077 で bind し、bind/connect 後も path/socket owner と inode を再検証して検出した race を拒否する。
component 検査だけでは same-UID TOCTOU の防御を証明できない。実装では descriptor-relative/no-follow 操作と native negative test が必要である。
主制御は private directory と peer identity であり、socket mode 単独ではない。
Unix path 長超過では live を unavailable とし、履歴記録は継続する。

frame は検証済み派生 metadata のみを含む。protocol/source/host version、binding/generation/epoch、native event/turn identity、受信・診断時刻、state/evidence label を対象とする。
raw prompt、tool output、audit 本文、terminal 本文、credential を送らない。
64 KiB 超過 frame は全体を拒否し、途中で切り詰めない。
queue の所有者は daemon であり、256 frame と 8 MiB の双方を上限とする。
overflow は任意観測を落として degraded/unknown を表示する。
短命 CLI は永続 runtime queue を持たず、live receipt で既存 history/spool receipt を置換しない。

Review Submit は GitHub ではなく選択した agent への送信である。
untrusted text の C0/C1 制御文字を拒否または可視に無害化し、text として保持できるものは LF と TAB だけとする。ESC と bracketed-paste 終端も対象にする。
multiline paste を安全に区切れない host では preview と手動転送を使い、自動 Enter しない。
各送信は対象を再検証したユーザーの明示操作とし、waiting 観測を入力や承認の権限にしない。
制御文字注入、対象変更、不確かな途中送信を試験し、自動再送しない。

executable/argv/env profile は明示確認した native host 設定からだけ変更する。
renderer は allowlist の profile ID を参照し、任意 command を渡さない。
D9 は配布前に app/update の署名、notarization、hardened runtime、Electron fuse の方針を検証する。
ローカル fixture の ad-hoc 署名を配布認定にしない。

### 寿命、振る舞い試験、実装の対応

UI 終了は detach だけとする。
daemon の完全終了は影響 run を列挙して明示確認する。PTY master を閉じると SIGHUP、agent 終了、制御 terminal 喪失が起き得る。
crash は作業喪失を招き、orphaned 表示は復旧保証ではない。
D3 で PTY holder の分離案を評価するが、現時点で保証しない。生存 process を自動 kill/resume しない。
run がある間は更新を延期できる。24 時間後と明示 update 操作時に in-app 通知し、cancel/export/明示 stop を提示するが、timeout で強制停止しない。
auto-start は opt-in のままとする。

| Given / When | 必須の観測結果 | Level / 所有者 |
|---|---|---|
| daemon が PTY master を失う | SIGHUP と作業喪失の可能性を表示。推定復旧しない | native fault / D3 |
| 稼働 run 中の更新 | 延期し 24h で通知。自動停止しない | native fault / D3,D9 |
| sleep/wake、cursor gap | 新しい binding 付き証拠まで unknown | reducer + native / D3,D4 |
| Attention 重複、OS 通知拒否 | 一遷移一通知。metadata のみ。in-app fallback | component + native / D4 |
| metadata 上限、delete の取消 | 可視に拒否し、eviction/delete しない | integration / D2,D8 |
| queue 超過、unbound 観測 | 有界出力と gap 表示。他 run の Attention なし | PTY + reducer / D3,D4 |
| draft に ESC/control/paste 終端 | keystroke 注入、承認、自動 Enter なし | unit + native / D7 |
| global と実行別 hook の合成 | 履歴一回、または明示 unsupported/unknown | host integration / D4 |

| 要件 | 所有する計画 Issue |
|---|---|
| CLI 観測受付と public stdout/receipt/後方互換性 | D4 #2433 |
| runtime 寿命、更新延期、opt-in auto-start、sleep/wake | D3 #2432。統合は D9 #2438 |
| metadata 上限と export/delete | D2 #2431。既存 mutation usecase は D8 #2437 |
| metadata-only 通知と Attention | D4 #2433 |
| immutable text と安全な agent Submit | D6 #2435、D7 #2436 |
| 署名、packaging、最終 native/互換性証拠 | D9 #2438 |

計画 Issue は ownership、依存、正負の AC、rollback を含む。
新しい frontend の正確な command は framework と source layout の選定前に捏造せず、選定まで実装を止める。
既存 `make docs/check`、staged selector、final-head CI は維持し、native gate を docs 検証で代替しない。

## 完了状況

- [x] scope と制約の復元、実 binary の版確認。
- [x] 五ホストの文書ベース matrix と証拠区分。
- [x] Muse native echo probe と環境 binding 消失の記録。
- [x] Muse の並行した実行別相関 fixture と reducer 合成 16 テスト。
- [x] 構造、contract、脅威モデル、TDD、rollback の提案。
- [ ] 五ホストの native interaction/approval/binding 検証。
- [ ] 同等条件の native UI 計測とすべての必須手動 gate。
- [x] 独立した Anthropic/Claude の packet レビューを実施。指摘は分類し、承認とは扱わない。
- [x] 2026-10-18 に `5b0b41d6` の最終差分を独立した Anthropic/Claude がレビュー。文言・台帳の条件修正は主担当が別に確認する。
- [ ] ユーザーによる architecture/framework の採用判断。
- [ ] D0 全体の受入と Status の Accepted への変更。
- [x] ownership、依存、正負の受入条件、検証、rollback 付き D1–D10 計画 sub-issue を作成。D0 受入までは実装禁止。
提案文書の delivery：2026-10-18 に、ユーザーは今回の PR に限り、主担当による復元・検証・独立レビューの手配・修正・merge・後片付けを明示承認し、従前の本人 UI 限定経路を置き換えた。
ユーザーはさらに、#2424/#2414 を開いたまま提案成果を統合する今回限定の closing 規則例外も明示承認した。
有界な文書 checkpoint は別途 #2440 で管理し、native 受入や architecture 採用とは扱わない。
統合状態の正本は PR #2429 の実際の履歴であり、本書の D0 受入 checkbox ではない。

未完了 gate がある間は #2424 を閉じず、認定完了と報告しない。

最初の独立レビューは英日 ADR と最小化した証跡だけを Claude Fable 5.1/xhigh に渡す要求だった。
共有 launcher が provider 実行前に `delegation_adapter_denied reason=author_unknown` で停止した。
設定上の作者 model `gpt-6.1-sol` がローカルのモデル登録簿に存在しないためである。
この最初の試行による AI review の結果は存在しない。
別の作者 ID と偽ったり、gate を弱めたり、空の試行を承認と扱ったりしない。
これは旧 launcher の metadata とモデル版番号の結合による失敗であり、版番号の登録や固定が必要だという根拠ではない。
dotfiles の変更は許可されておらず、行っていない。
作者の偽装や権限の緩和をせず、登録済み Claude engine と CLI の現行モデル選択による直接の read-only レビューを利用できる。
主担当の docs 検証と合成テストで必須の独立レビューを置き換えない。

手動入力試験 fixture は CUA の native app lookup が timeout し、開けなかった。
IME、clipboard、VoiceOver の合格は記録しない。
別の GUI 自動化手段や OS 保護の回避は使っていない。

## 出典

確認日 2026-10-18。
可変な公式文書は候補の根拠であり、導入版の実機証拠ではない。
native の結果は上記に要約し、私的 transcript を公開しない。

- D1：[Claude hooks](https://code.claude.com/docs/en/hooks)
- D2：[Codex hooks](https://learn.chatgpt.com/docs/hooks)
- D3：[Antigravity hooks](https://www.antigravity.google/docs/hooks)
- D4：[Muse hooks](https://dev.meta.ai/docs/muse-code/extending)
- D5：[Grok official source documentation](https://github.com/xai-org/grok-build/blob/main/crates/codegen/xai-grok-pager/docs/user-guide/10-hooks.md)
- D6：[cmux Feed](https://github.com/manaflow-ai/cmux/blob/main/docs/feed.md)
- [cmux agent hooks](https://github.com/manaflow-ai/cmux/blob/main/docs/agent-hooks.md)
- [Superset status](https://github.com/superset-sh/superset/blob/main/apps/docs/content/docs/agent-status.mdx)
- [Claude Squad classifier](https://github.com/smtg-ai/claude-squad/blob/main/session/tmux/tmux.go#L224-L244)
- [agent-deck status](https://github.com/asheshgoplani/agent-deck/blob/main/docs/status-detection.md)
- [Conductor harnesses](https://www.conductor.build/docs/reference/harnesses)
- [Electron security](https://www.electronjs.org/docs/latest/tutorial/security/)
- [Wails v2 options](https://v2.wails.io/docs/reference/options/)
