# Claude Code native 認定：2.1.318

[English](claude-2.1.318.md)

**状態：未完了。** [#2475](https://github.com/duck8823/traceary/issues/2475) を追跡し、native-host の親や runtime 復旧 service の完了を意味しない。[本文を含まない証拠](evidence/claude-2.1.318.json) に実測・失敗・未認定を分けて記録する。この fixture で native 現在状態や製品の復旧機能を有効にしない。

## Source と認証

導入済みの通常 CLI は `2.1.318 (Claude Code)` を報告した。native artifact の SHA256 は `677a2ea7bf576cedd20e191d973914ed8a41796f53a63b1923381ddc4d5b5e7d`、通常 entry は `079234054a9e035839f50118b618c1568c8ae1e20950f9b668a60bf27722e3cc` である。検証前後に両方を確認し、upstream commit や互換版範囲は推測しない。

通常の foreground CLI 自身が既存の cached claude.ai／firstParty サブスク認証を扱い、要求前に同じ非 secret 最小環境で方式を確認した。orchestrator は credential cache を読取／コピー／表示せず、child 環境に credential 値を転送しない。host 認証を利用するのは native CLI 自身だけである。print／SDK の課金経路は認定対象外とした。plan、空の model tool、strict な空 MCP、no-Chrome で検証を制限し、既存 native hook／Guardian は維持した。wrapper、login、install、永続的な global／project／auth／permission 設定変更、承認回答は追加していない。

## 実際の完了済み会話の再開

合成マーカーだけの turn が native model `claude-opus-5-5` で完了した。最初の harness は user record の content が常に list と誤認し、この版の string を認識できず完了を見落とした。元の FAIL は保持する。元入力 hash、最終マーカー、native turn 完了、tool 0 を検証した後、fresh な実 parent／UID／command／cwd／session marker で対象を確認し、その隔離 child だけを SIGTERM で片付けた。exit 143 は**正常終了の成功でも、turn 途中の中断の証拠でもない**。

修正 observer は string／list と古い prompt の否定 test に成功してから使用した。同じ自作 UUID を foreground で明示 resume し、元 cwd と capture store で新しい文脈確認質問から元の高エントロピーマーカーを復元した。native model とサブスク provider は同じで、再開した実 owner は exit 0 だった。元入力／保留入力／承認／tool を再送せず、name／picker／continue／fork／background／新 chat／model／API の fallback は行っていない。証明したのは完了済み会話の文脈再開だけであり、任意の中断副作用の復元や runtime 排他／crash 安全性ではない。

## 実際の中断会話と明示的な PTY 入力

後続の foreground fixture は合成 user turn を受け付け、その turn の assistant 記録がない段階で中断しました。
正確に所有する native child の Wait 結果は exit 143 でした。
通常の foreground resume は同じ UUID/cwd の native SessionStart を発生させ、新しい RUN_ID/generation/epoch hint と一致しました。
`CLAUDE_CODE_RESUME_INTERRUPTED_TURN=0` を指定した起動後8秒間は、新しい入力を送らず、UserPromptSubmit/Stop callback と user/assistant 記録の追加がないことを確認しました。
この変数による中断 turn の自動継続の無効化は [公式環境変数リファレンス](https://code.claude.com/docs/en/env-vars) に記載されています。
この foreground fixture の挙動は実測に基づき、print mode の文書から推測していません。

観測器の失敗2件は FAIL のまま保持しています。
最初の raw terminal burst と Return では UserPromptSubmit がなく、native resume の失敗は証明されませんでした。
別の明示質問を標準 bracketed paste と別送 Return で入力すると、元の model/provider のまま最初の message の marker を復元し、実 owner は exit 0 で終了しました。
その fixture の最終件数判定は、native が保存した `/exit` の表示用 user 形状記録3件を誤って再送に数えました。
別の metadata verifier が実際の自作記録を検査し、元の保留入力と新しい明示入力が各1回だけ存在し、追加分が local-command caveat/exit/stdout の正確な3件組だけであることを確認しました。
修正した観測器の test は、任意の continuation や保留入力の重複を拒否します。
元の FAIL を PASS に書き換えていません。

再開 run の SessionStart、UserPromptSubmit、Stop、SessionEnd は各1回で、元会話と target、新しく注入した runtime hint に一致しました。
これは model tool 呼び出しがゼロの通常 CLI について、中断した文脈の再開と保留入力の非再送を確認した証拠です。
保留 tool/approval の fixture、global 排他の証明、現在の waiting 状態の認定、製品 Runtime adapter の認定ではありません。
raw native ID、会話本文、account data は共有していません。

## 実際の保留 tool callback の中断

別の通常 CLI fixture は plan mode のまま、read-only command `rtk proxy pwd` だけを要求しました。
同期の test 用 PreToolUse guard は metadata を記録してその command を保留し、継続した場合も allow ではなく deny を返します。
他の command は直ちに deny するため、driver が停止しても tool は実行されません。
PreToolUse の保留中に、正確に所有する native AI を中断し、exit 143 を確認しました。
tool を実行したり、承認に自動回答したりしていません。

最初の観測器は、対応する tool-use block が canonical transcript にあることを要求して失敗しました。
この FAIL は保持しています。
別の修正 fixture は callback を保留したまま書込遅延を測り、5秒後でも SIGTERM 前に tool-use block が保存されていないことを確認しました。
保留要求の証拠は、session、cwd、runtime hint、Bash command、plan mode が一致した実 native PreToolUse event です。
transcript の欠落を「保留要求がなかった」証明には使いません。
永続化済みの cutoff tool の fixture を検証したわけではありません。

実旧 owner の終了後、自動継続を無効にして同じ UUID を foreground resume しました。
新入力を送らない8秒間に、PreToolUse/PostToolUse/UserPromptSubmit/Stop callback、tool 要求、native の記録済み `api-request` は増えませんでした。
別の明示的な tool 不使用の質問は、元の model/provider で最初の message の marker を復元しました。
再開側の tool callback はなく、実 owner は exit 0 でした。
元の保留要求と新入力は各1回だけ記録されています。
permission dialog の復旧と製品 Runtime の統合は未認定のままです。
現在の waiting/approval 権限や、外部/global 排他の証明にはなりません。

## Native hook 入力と capture

テスト専用の追加 metadata observer が SessionStart、UserPromptSubmit、Stop、SessionEnd をそれぞれ1回受信した。stdin を制限し、stdout／判断を返さず、隔離 store に root field／型、照合 bool、hash だけを保持した。agent を wrap せず、元 recorder の置換や2つ目の primary recorder の追加は行っていない。元の Traceary transcript capture は1件だけ増え（4→5）、実 native owner は exit 0 だった。

4つの native payload は canonical な string の session_id と、自作会話／target に一致する cwd を持った。UserPromptSubmit、Stop、SessionEnd には string の **prompt_id** があり、turn_id はなかった。SessionStart には両方ともなかった。prompt／Stop の permission_mode は plan、SessionStart／End には存在しなかった。run／generation／epoch field はなく、Traceary runtime hint 名も渡していない。これだけで managed generation、鮮度、現在の待ち、承認権限を認証しない。prompt_id は prompt 相関であり、**native turn_id へ暗黙に読み替えない**。native 履歴を現在状態の interface にしない。

現行の [公式 hook 文書](https://code.claude.com/docs/en/hooks) は prompt_id の prompt 相関と、transcript 書込が遅れる可能性を説明する。これは意味の参考であり、当該版の実測を代替しない。Stop は process 終了／readiness ではない。SessionEnd は lifecycle telemetry であり、process の adopt／signal 権限ではない。

## 却下した合成候補

追加 per-run settings に**同じ既存の** `traceary hook transcript claude` を入れても、この導入構成では deduplicate されなかった。native Stop handler は3→4になり、合成応答1回で core transcript event が**2件**増えた（2→4）。native hook error は0で、その後の実 owner は exit 0 だった。これは記録済み FAIL として却下し、duplicate primary handler を製品化しない。descriptor flag の追加で global handler が安全に置換されるとも推論しない。後続の metadata-only observer は primary capture を1件に維持したが、primary handler の拡張や generation binding は解決していない。

## 実際の foreground 並行起動

後続の metadata-only test で、完了済みの同じ合成 UUID を通常 foreground process 2つから開いた。A の生存と一致する native SessionStart を確認してから B を試し、B も同じ UUID の SessionStart を返し、両実 process が生存していた。自作会話の user／model-request record は増えていない。その後、実際に所有する fixture handle だけを意図的に SIGTERM で片付けた（143／143）。正常終了とは扱わない。

この導入版では foreground resume／session-start だけで**単一 process の所有を保証しない**ことが分かった。同時 model turn、任意の子孫 process の非稼働、安定した global lease は検証していない。CLI 起動1回、履歴中の PID、Traceary 内の要求直列化だけから排他を推論しない。修正 test collector は実際の RUN_ID hint 名を確認したが、generation や復旧の権限は有効にしない。

## 残る認定と安全 gate

| 要求 | 状態 |
| --- | --- |
| 正確な source と4種の native hook field／型 | 上記と実保留 PreToolUse を実測。permission dialog と成功した PostToolUse は NOT_RUN |
| 同じ native 会話の文脈／cwd／store／model／provider | 完了済み文脈と制御した中断文脈の fixture は PASS。同じ UUID の foreground 並行起動2件を実測。保留 PreToolUse callback は検証済み。永続化済み cutoff tool、permission dialog、製品の管理内排他は未認定 |
| global／per-run primary handler 合成 | duplicate-primary 候補は FAILED・却下。安全な descriptor 拡張は未認定 |
| 同 cwd 並行、独立 generation、ID 再利用／遅延／hint 不達／重複／逆順／失効 | 未完了。descriptor／env／cwd／native ID 単独では unbound |
| working／waiting_input／waiting_approval／process | 未認定。permission metadata は可視の待ちや承認判断ではない |
| 製品 adapter、独立実装 review、final-head CI／security／wave | 未実施。Issue close や製品 capability を推論しない |

安全な runtime 復旧 #2478 は durable な検証済み identity、元 source／target／profile、実際の旧 owner 非稼働、session 排他、crash／重複の意味を担当する。正確な UUID だけで実行を許可しない。特に導入 CLI help は、background resume が実行中会話を copy する場合を説明するため、復旧では background／fork／copy を禁止する。foreground の起動挙動は上記で実測しており、native の global lease ではない。[#2478](https://github.com/duck8823/traceary/issues/2478) についてユーザーが2026-11-04に明示決定した後は、旧Traceary管理AI processとTraceary内の要求排他を保証範囲とする。外部native CLIの調停や全子孫の終了証明は保証対象外である。このnative制約は表示し、追加serverで迂回したり外部sessionの排他要件に戻したりしない。管理内の排他と旧管理AIの終了確認は、引き続き#2478の必須条件である。

物理 sleep／wake はユーザーが後回しにした NOT_RUN のままで、sleep／launchd／電源設定を操作していない。Muse native は保留する。UI／framework の採用や配布は含めない。rollback は元 capture／history／auth を維持し、任意の host／version capability だけを無効にして unknown／unbound へ戻す。
