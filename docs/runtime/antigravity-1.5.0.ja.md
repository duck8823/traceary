# Antigravity native 認定: 1.5.0

[English](antigravity-1.5.0.md)

**状態：通常 native の文脈・増分 capture・metadata を実測。実 owner は回収済みですが、復旧は未認定です。** [#2476](https://github.com/duck8823/traceary/issues/2476) を追跡します。
親の host 対応を完了扱いにせず、native 現在状態や復旧を有効にしません。
[本文を含まない source 証拠](evidence/antigravity-1.5.0.json) で実測 metadata と未実施 gate を区別します。

## 実測した installed source

通常の導入済み実行ファイルは正確に `1.5.0` と報告し、SHA256 は `e61679b8ba54abae9a56534208e21f65e1dede6e4eb0144dbd64535f42d07724` です。
実 CLI help には、会話 ID による resume、session 限定 settings、plan mode、model 選択があります。
help は同じ会話や再送防止の証拠ではありません。
互換 version 範囲や upstream revision は推測しません。
source だけの調査段階では model request、login、install、global 設定、permission、認証の変更を行っていません。
別の native 試行は下記に記録します。

## 実測が必要な native 契約

現在の[公式 hook 文書](https://antigravity.google/docs/hooks) と導入済み vendor guide は、camelCase の会話・workspace・transcript metadata を説明し、SessionStart も掲載しています。
古い Traceary の 1.1.x 観測を、この実行ファイルの証拠にしてはいけません。
native callback payload と合成方式は、隔離した実測が必要です。

Traceary は、会話 ID、step/invocation/execution の counter、単独の fullyIdle を、相関情報や telemetry として扱います。
独立した管理対象 generation、現在の待ち・承認、process 終了の権限にはしません。
文書や CLI flag だけで製品 adapter を有効にしません。
core Session は log-only のままです。
live state の認定と、安全な会話復旧の認定は分離します。

## 初回 native 試行：認定ではなく trust gate

通常の Gemini4 Argon Medium foreground fixture を plan mode・sandbox を維持して起動したところ、この専用 worktree の初回信頼確認が表示されました。
trust/permission の回答や fallback は送っていません。
実際の所有 process だけを停止し、return code0 で Wait しました。
この後始末の終了値は **model turn 完了の成功ではありません**。
隔離 store の Antigravity session/event は0件です。
実 model、callback、同会話 resume、現在状態の認定は未証明です。
この会話で、ユーザーにこのフォルダだけを手動確認するよう依頼し、global 設定を変更せず他の Daemon 作業を継続します。

## 必要な認定 gate

- 通常 CLI 自身が既存認証を利用し、credential の収集や print/SDK/API 課金 fallback は行いません。sandbox、承認、既存 native hook を維持します。
- 合成入力と隔離した target/store を使い、前後の source を固定して実所有 process handle を保持します。trust/login/permission prompt に自動回答しません。
- native field と意味、主要 capture/stdout/receipt の維持を実測します。descriptor 拡張のために Traceary の主要 recorder を重複追加しません。
- 同 cwd、ID 再利用、遅延・foreign・hint 不達、重複・逆順・失効を検証します。曖昧な claim は unbound/unknown を維持します。
- 通常 CLI の明示 resume 前に、元の会話・正規 target/model/provider と、旧管理対象AIの実終了を検証します。保留入力・tool・承認を再送せず、新 chat への置換や PID adopt はしません。
- 製品コードの前に Structure-Behavior 設計と独立 checkpoint を記録します。その後、影響・逆依存テスト、最終独立 review、fresh CI/security、同 SHA wave を実施します。

ACP、app-server 実行方式、remote-control、agent wrapper、新 model API、UI/framework 採用、配布は対象外です。
Muse native は保留を維持します。
実機 sleep/wake は別途、ユーザー指示で保留しています。
rollback は任意の host/version adapter だけを無効化し、元の認証・capture・history を保持します。

## 後続の実測と保持する失敗

ユーザーがこのフォルダだけを手動で信頼したと回答した後、通常 native foreground の合成 marker turn が完了しました。
primary capture は session_started1/prompt1/transcript1 で、実 owner の後始末の Wait は0でした。
これは turn 完了の証拠であり、実 model/provider や live state の認定ではありません。
要求 model は gemini-4-argon-medium ですが、独立した native model/provider witness は未取得です。

その後、同じ会話の明示 resume で、新しい質問への返答が元の marker と完全一致しました。
ただし初回 harness は既存 transcript 件数を受け入れて早期に後始末したため、raw verdict を保持したうえで完全な capture 成功としては不採用にしています。
修正版は別の一意な新質問を使い、baseline prompt2/transcript1 に対して prompt3/transcript2 を要求し、文脈保持と増分件数を観測しました。
この試行の実 owner は SIGTERM だけでは終了せず、ユーザーが検証用 process だけの強制終了を許可しました。
controller との直接親子関係・UID・完全な argv/input hash・cwd・source を再確認して対象 child だけを停止し、元の Popen の Wait が -9 を返しました。
正常 shutdown や製品の PID adopt 許可とは扱いません。

native USER_INPUT は CLI 引数を包むため、native content 全体の hash は元 CLI 入力との同値判定に使えません。
既知の合成引数が完全一致で1回含まれることを確認しました。
domain workspace projection も repository identity であり、正規 cwd の path ではありません。
fixture path との直接比較は target の証拠にならず、これらの harness 修正で製品の target 検証を弱めてはいけません。

## metadata 専用 hook の実測

元の primary recorder を変更せず、専用 worktree に判断を返さない一時 metadata hook を追加しました。
実終了確認後、同じ合成会話への別の新質問で prompt4/transcript3/session_started1 を観測し、実 owner の Wait は0でした。
PreInvocation/PostInvocation/Stop の callback で conversationId が元の会話と一致し、workspacePaths は元の正規 target を含み、modelName は要求した gemini-4-argon-medium と一致しました。
Stop の fullyIdle=true から現在状態や終了権限を作りません。
resume 時の SessionStart は未観測で、inventory は選択した key の型だけです。
実 provider と独立した generation witness は未認定です。
本文・認証情報・transcript path は保存・共有せず、元 recorder の重複はありません。
一時 hook/helper は実 Wait 後に作成時 hash が一致したものだけ削除しました。

## 実装した任意相関：復旧認定ではない

application に session-only の AntigravityObservationClaimDecoder port を設け、純粋で上限付きの filesystem parser を通常 composition root の両経路に注入しました。
元の取得済み payload を借用し、stdin の再読、native 履歴・環境変数の参照、primary recorder の追加を行いません。
source version1.5.0 の完全一致は format の選択だけであり、provenance の認証ではありません。
実測済みの prompt/turn 呼出し境界だけを対象とし、session/tool 境界は unavailable にします。
native event-name field を合成しません。

canonical な小文字 UUID の conversationId だけを受け入れ、重複・case-fold alias・他 host の identity field 混在を拒否します。
完全な UTF-8、payload1MiB、root key128件、未知 container64段の上限を検証し、未知の本文を claim や error に保持しません。
nil、未対応版、unavailable、error では両 native ID を空にし、snake_case の共通抽出へ fallback しません。
結果は turn、model、provider、generation、現在状態、承認、終了、復旧の権限を持てません。
他 host と元の capture/stdout/receipt/spool は維持し、配布 hook に --observe flag は追加していません。
製品側の配送は dormant/未実測です。

合成 parser、durable intake、公開 Cobra、runtime の同 cwd・会話 ID 再利用テストで契約を検証します。
native metadata は実測 shape の証拠であり、管理対象 generation の binding ではありません。
実装前の独立 design checkpoint はこの限定 slice を承認しました。
#2476 を閉じるには、native 復旧、実 provider の証明、最終の別 family review、final-head CI/security、同 SHA wave が引き続き必要です。

## 中断 oracle の失敗と、別の入力なし再開

後続 fixture は d0c9244815055ed30434d7c4e330bb3a03b45cbd から build した candidate を使いました。
実 PreInvocation は元の会話・正規 target・model と一致しましたが、想定した primary prompt の増分と public history の USER_INPUT を観測時間内に確認できませんでした。
結果は **FAIL** であり、実 owner の Wait は0です。
中断した model request や durable な保留入力は証明していません。
非同期の capture を中断前の条件にした harness の前提は未証明で、失敗を保持し、自動再送や成功への読み替えは行いません。

その実終了後、別の通常 CLI 再開では同じ会話 ID を指定し、**入力を送りませんでした**。
元の marker が描画され、10秒間に PreInvocation/PostInvocation callback はなく、自己所有の履歴と primary 件数も prompt4/transcript3/session_started1 のままでした。
実 owner の Wait は0です。
これは時間範囲を限定した文脈読み込み・入力なし観測であり、保留 model/tool/approval の復旧認定ではありません。
metadata collector を配置しても resume 時の SessionStart は再び未観測でした。
native TUI 内の email pattern は検出され、Gemini API key の header 文言はありませんでしたが、account 値は保持していません。
この boolean だけで durable な provider/認証を証明しません。
一時 metadata file は両 owner の実終了後、作成時 hash が一致したものだけ削除しました。

復旧には、resume 中の fresh な native identity acknowledgment、provider の再検証、実際の中断状態の coverage が残っています。
CLI 引数、古い履歴、descriptor hint、端末描画で acknowledgment を置き換えません。
現在の[公式 resume 文書](https://antigravity.google/docs/cli/commands/resume) は ID 指定を説明しますが、この導入版の startup callback や再送防止契約を証明しません。
wrapper、app-server、broker の迂回は採用していません。

## 許可された1回限りの passive statusLine 実測

ユーザーが許可したのは**隔離テスト1回だけ**であり、製品への採用ではありません。
通常 CLI を入力なしで再開し、process 限定の `--settings` に判断を返さない native statusLine command を指定しました。
global 設定と元の primary hook は変更していません。
現在の[公式 statusLine 文書](https://antigravity.google/docs/cli/statusline/) にある構造化 stdin を参考にし、導入済み1.5.0の実 callback は別に実測しました。

最初の snapshot は会話が未一致で、model も要求と異なりました。
続いて会話が一致し、その後で要求 model が一致しました。
両 native identity alias、正規 cwd/workspace、要求した具体的 model、version、product がそろった最初の snapshot は **1.8566秒後**でした。
実 owner の Wait は0で、自己所有の履歴と primary 件数は変わらず、model 入力は送っていません。
1回の計測は起動時間の保証ではなく、一部の ID だけが一致した初期 snapshot を採用してはいけません。

email は存在 boolean だけを保持し、account 値と raw payload は保存していません。
plan_tier は未観測であり、provider/認証 class は未認定です。
自己所有の合成会話の full transcript と message JSON には、選択した model/provider field がなく、本文・識別子を含まない schema だけを記録しました。
statusLine は入力前の構造化 metadata を得る具体的候補ですが、復旧全体の成功ではありません。
製品利用には、process 内の表示上書きに対する明示採用、source/order/model 確定の設計と review、新 session の動的 identity binding、残る native fault matrix が必要です。
この実測で native 現在状態・承認権限、wrapper、broker、app-server を導入していません。

その後、ユーザーは **Traceary 管理下の AGY1.5.0 process 内だけ**の metadata 経路の採用と、その process の custom statusLine 表示の一時置換を許可しました。
global 設定、元の記録 hook、認証、native model default の変更は許可していません。
provider・最終 model・順序の検証が終わるまで、復旧成功を公開しません。
独立 review は入力前 surface の存在を確認しましたが、過去の any-match 選択と A-B-A を失う fingerprint 重複排除は、製品の latest binding protocol には使えないと指摘しました。
今回の実測は一致する callback の存在であり、安定した最新 binding の証明ではありません。
launcher や復旧を有効にする前に、独立した dormant な private codec を設計しています。

dormant な infrastructure-private statusLine codec を実装しました。
返すのは session・正規 path・model の縮約表現と account field の存在 boolean だけです。
version1.5.0/product、UUIDv4 alias の一致、current cwd、native catalog の model label、完全な UTF-8、重複・alias 拒否と上限（1MiB、object128 key、array128件、root を含む container16段）を検証します。
初期化中の snapshot は binding にせず、未対応版・model や不正・矛盾入力では固定 sentinel と zero result を返します。
account 値やその hash、agent state、承認、provider class は返しません。
A-B-A は別々に decode され、latest/any-match/重複排除 protocol は実装していません。
この codec は下記の owned metadata producer へ接続しましたが、**native 復旧成功の経路には接続していません**。
元の Claude codec と journal 規則は変更していません。
1回の検証に使った process 限定 settings file は、実 Wait 後に作成時 hash が一致することを確認して削除しました。
collector は private な証拠 artifact としてだけ保持しています。

## 実 daemon 所有の metadata producer

通常の Traceary-owned PTY 起動で、完全一致の pinned AGY1.5.0 artifact だけに任意の metadata 観測を追加しました。
child ごとの private settings file から、許可済み statusLine command を指定します。
model/mode/認証/承認の override、global hook の書き換え、agent wrapper はありません。
未知 source/版、loader injection、重複環境変数、temp namespace 不一致では、元の通常起動を維持して metadata を unavailable にします。
CLI の両 composition に、silent・existing-root-only の専用 sink を注入し、元の Claude sink/lifetime は分離しています。

owner は縮約した不変 launch intent を固定します。
上限128KiBの constant-size private journal は、同じ snapshot でも **各 delivery** で local sequence を進め、session/model/project/account-presence の変化と A-B-A に対する uncertainty を取り消しません。
partial/invalid/unsupported callback で古い知識を再有効化しません。
local sequence は native causal order ではなく、raw account/payload、入力、環境、PID、承認・現在状態の権限を保存しません。
実 `exec.Cmd.Wait` の後で、snapshot と既知 file だけの cleanup を同じ lock 内で行います。
遅延 callback は退役 root を再作成せず、未知・交換済み file は再帰削除せず保持します。
metadata の失敗は process の終了結果を変えません。
AGY の RecoveryBinding/NativeResumer は引き続き未実装です。

隔離した実 candidate daemon が、本物の AGY executable を登録して controlling PTY を所有しました。
native model は override していません。
入力前には sequence5 まで initializing delivery を記録しました。
明示した合成 operator input は207/207 byteで ACK され、保留・元入力の再送や native 承認への自動回答はありません。
新しい native 会話が marker を返し、元の記録は session_started1/prompt1/transcript1、private journal は sequence10/observed/uncertaintyfalse でした。
native default は gemini-4-argon-high です。
通常の ACK 付き Stop の後、実 native Wait/journal exit0、private slot の削除、実 daemon Wait0 を確認しました。
bounded output は gap なしで13213 byteを観測し、保存していません。
これは owned metadata/control/primary 経路の実測であり、中断復旧・provider/最終model/順序・native現在状態の認定ではありません。
全過程で source fingerprint は変わっていません。

## Provider gate：account の存在だけでは不十分

[公式認証仕様](https://antigravity.google/docs/install/) には、account・Gemini API key・ADC の別経路があります。
[statusLine](https://antigravity.google/docs/cli/statusline/) の email は account 情報であり、明示的な active provider の識別子ではありません。
独立した実装前 checkpoint は、AccountPresent と未定義の override guard だけで `native-account` を確定する案を承認しませんでした。
effective route の入力元と優先順位、active route との排他性、元と再開時の provider class の保存・再検証について、追加証拠が必要です。
別認証の試験や設定変更は行っていません。
この近道を不採用としたのであり、native 復旧が不可能という判定ではありません。
metadata codec と復旧 unsupported の契約は維持します。

### Provider 入力の具体的な証拠境界

現在の [CLI 認証文書](https://antigravity.google/docs/cli/install/) では次の入力を説明していますが、導入済み1.5.0 artifact の完全な実装契約ではありません。

| 入力 | 文書上の役割 | 未証明の項目 |
|---|---|---|
| 永続設定の `modelProvider` と `GEMINI_API_KEY` | Gemini API への直接経路を選択。key 単独では選択しない | この完全一致版での effective settings 解決 |
| `GOOGLE_GEMINI_BASE_URL` | API 経路の custom endpoint | 元と再開時の経路の照合 |
| `AGY_ADC_AUTH` | ADC 認証を有効化 | signed-in account metadata との排他性 |
| process 専用設定と永続設定 | effective configuration に影響し得る | provider に関係する全入力と優先順位 |

static 調査では pinned executable の digest 一致を再確認しました。
binary の文字列候補は調査の手掛かりに限ります。
連結された literal や無関係な library 文字列も含まれるため、環境変数一覧や effective-provider denylist として扱えません。
host 設定・credential の読み取りや別認証 process の起動は行っていません。
次の認定には、AccountPresent の追加推測ではなく、対象版の設定解決と active route の証拠が必要です。
この表で provider・復旧 capability を有効化しません。
