# Antigravity native 認定: 1.5.0

[English](antigravity-1.5.0.md)

**状態：調査のみ。native 実行と復旧は未認定です。** [#2476](https://github.com/duck8823/traceary/issues/2476) を追跡します。
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
