# Antigravity native 認定: 1.5.0

[English](antigravity-1.5.0.md)

**状態：初回 native turn は成功。増分 capture を伴う文脈再開を観測しましたが、実 owner の後始末は未完了で、復旧は未認定です。** [#2476](https://github.com/duck8823/traceary/issues/2476) を追跡します。
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
しかし SIGTERM 後の実 owner はまだ Wait を返しておらず、controller は handle を保持して稼働中です。
実終了までは terminal PASS、新たな resume、PID adopt、model/provider 認定を行いません。

native USER_INPUT は CLI 引数を包むため、native content 全体の hash は元 CLI 入力との同値判定に使えません。
既知の合成引数が完全一致で1回含まれることを確認しました。
domain workspace projection も repository identity であり、正規 cwd の path ではありません。
fixture path との直接比較は target の証拠にならず、これらの harness 修正で製品の target 検証を弱めてはいけません。
