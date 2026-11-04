# 管理対象AIの明示的な native 復旧

[English](./managed-recovery.md)

## 実装状況

復旧用 branch では application coordinator、private recovery journal、管理 SDK/IPC、`runtime resume` コマンドを実装しています。
通常の Claude 2.1.318 CLI を使う候補 adapter も実装しています。
実際の Daemon/CLI で、完了済みの会話と、管理対象AIの入力を実際に中断した会話について、新しい明示入力前に再送せず文脈を復旧する検証に成功しています。
host 全体の対応認定、最終の独立実装レビュー、最終 head の CI/wave gate は未完了です。
リリース済みの機能や host の対応認定を意味しません。
broker、app-server 実行方式、model API、agent wrapper は採用しません。

adapter の前提として、実行ごとの private な起動 metadata 保存と、無出力の内部 SessionStart 処理を実装し、source が厳密に一致する対応候補だけに接続しています。
この処理は主要な session log を記録せず、復旧の権限も確立しません。
最新の callback が変更、不正、読み取り不完全となった場合、以前の metadata は無効になります。
private な一時ファイルの後始末は、root の交換や未知のファイルを検出すると削除を拒否します。

Claude 2.1.318 用の private な履歴 reader も実装し、既存の合成実機 fixture で検証しています。
計画済みの private なファイルだけを symlink をたどらずに開き、物理的な同一性と、上限付き JSONL 全体を検証して、namespace の識別子と最後に記録された実際の model だけを返します。
native のローカルな `<synthetic>` model は、model の証拠に使いません。
16 MiB を超えるファイル、4 MiB を超える行、証拠不足、識別子の不一致は拒否し、project の走査や fallback は行いません。
この reader だけで native 復旧の対応を認定するものではありません。

静的な source 検証は登録済みの実行ファイルを維持し、固定した native artifact を確認します。
既知の non-print entry では、二つのローカルな routing import の固定と、既存の native 解決先が変わっていないことも必要です。
認証、provider、コード注入の上書き設定がある場合、経路を切り替えず復旧を未対応とします。
別の上限付き native `auth status` 検証は、既存のログイン済み first-party subscription の種別を要求します。
host 認証は native CLI だけが利用し、account 項目と stderr は破棄します。
既存 host で source と status の検証に成功し、所有中の child による復旧処理へ接続しています。
通常の起動計画は、provider status や native 履歴の確認を待ちません。

所有中の child の metadata を管理する内部処理は、実際の Wait 後に、最新の marker の取得と slot の後始末を同じ lock 内で実施します。
以前に取得した生存中の binding は使い回しません。
source、履歴、provider の検証が返すのは metadata だけであり、application の永続化済み終了記録の代わりにはなりません。
対応候補の通常起動は、新しい計画済み `--session-id` と、metadata 専用の `--settings` handler 一つを追加します。
resume は記録済みの会話を `--resume`、model を `--model` で指定するだけです。
対応候補では native の中断ターン自動継続を無効にし、入力は送信しません。
未対応 source の通常起動引数は変わりません。

daemon は、起動時に登録済み source が静的な対応条件を満たす場合だけ resume を通知します。
各操作は引き続き、正確な target/source/history/model/provider を再検証します。
capability は daemon の epoch ごとに固定します。
後から登録した profile でこの任意機能を使うには、明示的に新しい daemon epoch を起動する必要があります。
自動再起動や PID の adopt は行いません。

## 安全性の契約

同じ native 会話を再開するには、元の管理対象AIについて、実際の Wait による終了を Traceary が正確な実行識別子とともに永続化している必要があります。
保存 PID、daemon の消失、lifecycle の中断ラベル、client が指定した検証済みフラグは終了証拠になりません。
初期実装は、不足する終了証拠を全 process の走査や PID の adopt で補いません。
管理外の CLI との排他と、全子孫 process の非稼働は保証対象外です。

元の正規 worktree/store/profile、固定した native source/version、会話と model/provider が一致する必要があります。
native adapter は、通常 CLI の resume が中断した入力、tool、approval を自動再送しないことを検証してから対応を認めます。
Start が成立した可能性がある場合、caller の cancel や確認失敗でも、Traceary は実際の child の所有権を保持します。
native identity の確認と結果の永続化が済むまで、新しい入力と観測を拒否します。
実際に所有する child に対する人の明示的な Stop は利用できます。
自動 kill や自動承認は追加しません。

## 明示コマンドの形式

次は placeholder を使った例であり、既存 runtime に対する実行コマンドではありません。

```sh
traceary runtime resume --runtime-dir /absolute/private/runtime \
  --epoch CURRENT_DAEMON_EPOCH --previous-epoch ORIGINAL_AI_EPOCH \
  --run ORIGINAL_RUN --generation ORIGINAL_GENERATION \
  --worktree ORIGINAL_WORKTREE_ID --profile ORIGINAL_PROFILE_ID \
  --request-id EXPLICIT_NON_SECRET_REQUEST_ID --acknowledge-work-loss
```

表示した flag はすべて必須です。
`--epoch` は現在の daemon、`--previous-epoch` は元の管理対象AIを指定します。
両者が同じ epoch でも構いません。
stdin は読み取らず、位置引数の payload、argv/environment、native 会話、model/provider、tool、approval の上書きは受け付けません。
認証と承認は host が管理します。

JSON は元の request key と選択、記録済み phase、判明している後継の選択を保持します。
`actual_owner_handle` は、この Runtime が実際の child handle を保持することを示し、現在の生存状態は保証しません。
daemon 再起動後に返す保存済みの confirmed 結果から、handle や制御権限を復元しません。
出力は `external_actors_coordinated=false`、`descendant_inactivity_proven=false`、`pending_input_replay=never_requested` を明示します。

## 不確定な結果と記録の保持

新たに永続化した一つの intent だけが、一回の native Resume 呼び出しへ進めます。
同じ要求の再指定は既存の操作または結果の確認となり、元の選択の変更は拒否します。
再起動後の reserved/started/unknown 記録から起動し直すことはありません。
送信後の通信断、応答の不一致、曖昧な process 作成は unknown とし、未起動が確定した結果として扱いません。
不確定性を回避するために新しい request key を自動生成したり、入力を再送したりしないでください。

過去の unknown 要求を再起動可能にしたり、成功へ黙って書き換えたりはしません。
正確な後継AIについて、所有中に観測した execution 終了と、それに一致する検証済み native 終了記録の両方が永続化された場合だけ、**その終了済み後継AIを選ぶ新しい明示要求**で会話を再開できます。
未確認の先行要求では、model/provider を含む期待した binding 全体が一致する必要があります。
保存 PID、execution のラベルだけ、native 終了記録の欠落、異なる binding から予約を解放しません。
元の要求と元の run は確認専用のままで、metadata の読み込みから process を起動することはありません。

再開した run の確認と公開が済んだ後、操作者が対話中に model を変えることは可能です。
実際の所有AIの終了時に、検証済みの最後の実 model を、まだ終了未確定だった復旧 snapshot に確定し、次の再開で使います。
会話、履歴、namespace、target、profile、provider は変更できません。
unknown/未公開の再開は、元の要求 model との一致を引き続き要求します。
終了確定済みの記録は再変更できず、Traceary が model を選択・代替することもありません。

復旧 metadata は件数制限付きの binding と attempt 結果を保持し、terminal 本文や保留コマンドは保存しません。
export は revision と件数だけを返します。
明示確認付きの offline Forget は復旧用の結び付けを放棄する操作であり、process の終了や不確定な会話の再開許可を証明しません。
詳細は [metadata の監査と損失](./history-reconciliation.ja.md#明示的な-runtime-metadata-監査と損失) を参照してください。
