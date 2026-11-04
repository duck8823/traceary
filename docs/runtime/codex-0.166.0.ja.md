# Codex native adapter：0.166.0 の認定作業

[English](codex-0.166.0.md)

**状態：実装途中。隔離した native API の文脈再開は成功したが、command-hook／managed-PTY 認定は未完了。decoder、schema、合成 test を native 成功と扱わない。** このページは native 認定 #2468 の子 [#2474](https://github.com/duck8823/traceary/issues/2474) を追跡する。安全な runtime 復旧は #2478 が担当する。

## 実装済みの任意相関

通常の composition root が application の CodexObservationClaimDecoder に純粋な filesystem adapter を注入する。取得済み hook payload を借用し、stdin の再読、native 履歴、credential cache、transcript path の読み取りを行わない。新しい handler、待ち行列、worker、永続 schema、認証／承認設定、wrapper は導入しない。

format の選択は **codex-cli 0.166.0** の完全一致のみとする。bare 0.166.0、大文字小文字の変更、別 patch 版、未注入／nil decoder は unavailable とし、任意の native ID を省略する。通常 metadata は unbound/admission_only のままである。format の選択は、導入 source／版、generation、鮮度、native readiness の証明ではない。

候補となる command-hook field は canonical な session_id と任意の turn_id である。native event 名は想定した呼出し境界と一致する必要がある。root identity key を literal で decode し、処理前の payload 1MiB、top-level key 128 件、subtree 64 段の上限を設ける。identity field の重複／競合、case-fold alias、非文字列／null、不正な字句、壊れた／後続 JSON、過大入力は任意 claim を拒否する。未知の body は読み飛ばし、結果には保持しない。thread_id だけを持つ exec-wire envelope は command-hook payload ではない。

結果は SessionID、TurnID、decode の Available のみである。live binding、NativeProjectionEvidence、process 制御、承認、現在の working／waiting、復旧権限を作らない。EventID は元の spool receipt のままとし、host event claim で置き換えない。他 host と元の capture／stdout／exit／spool 順序は変更しない。元の primary helper は意図的に JSON の後勝ちを維持するため、session_id の重複でも primary capture は従来通り行い、厳密な任意 decoder は native ID 全体を省略する。

## 観測済み metadata と native 証拠の区別

調べた導入 binary は codex-cli 0.166.0 を報告し、package target は aarch64-apple-darwin、binary SHA256 は 39acf979729dd290f9a24ee38b2914e036c91b65952872e28ae2a6b88a7db7c1 である。binary 自身の offline app-server schema generator から protocol schema を得た。これはローカル artifact の固定であり、upstream commit や互換版範囲の証明ではない。現行の [公式 hook 文書](https://learn.chatgpt.com/docs/hooks) と過去の 0.145 fixture は format の参考情報であり、0.166 の native 挙動の証明ではない。

既存の cached ChatGPT サブスク認証は CLI 自身で確認し、credential の読み取り／コピー／表示を行っていない。標準 path／locale／CA path の最小 child 環境で metadata preflight は成功し、credential 環境を広域転送していない。startup-config 診断が報告するのは requirements であり、exec の実効 approval policy ではない。製品 launchEnvironment は変更していない。

隔離した通常 CLI の app-server stdio 検証で、model 要求前に実効 native cwd、gpt-6.1-sol／openai、on-request approval、auto_review reviewer、readOnly sandbox を観測した。CLI 所有の cached ChatGPT サブスク認証と非 secret の最小環境を使い、合成マーカーだけの turn が完了し、実際の owner が正常終了した。その後の明示的な thread/resume は同じ native UUID と元の target／model／provider／policy を復元し、新しい質問への応答が元の高エントロピーマーカーを保持していることを元入力の SHA256 で確認した。再開した実際の owner も正常終了し、tool／approval 要求は発生しなかった。元入力や保留入力の再送、新会話への fallback は行っていない。

最初の再開 harness は account/updated が毎回発行されると誤って要求し、検証 turn の前に停止した。これは観測の可用性に関する失敗であり、provider の認証失敗ではない。修正後は refreshToken=false の account/read で account type の chatgpt だけを保持し、account 詳細を破棄した。token／credential は要求も保持もしていない。この証拠が認定するのは隔離した app-server API の文脈再開契約だけであり、**command hook、exec／TUI 再開、managed-PTY の generation binding、製品 app-server broker の採用を証明しない**。

## leaf 完了前に必要な native 証拠

| 要求 | 現在の証拠 |
| --- | --- |
| 正確な native hook payload と core capture／redaction／stdout／receipt の同値性 | 0.166 では NOT_RUN。合成 decoder／Cobra test のみ |
| 同 cwd 並行、独立 generation witness、resume ID 再利用、遅延 callback、hint 不達、重複／逆順／失効 | NOT_RUN。descriptor／env／cwd／native ID だけでは binding しない |
| working／waiting_input／waiting_approval／turn／process／unknown の coverage | 未認定。Stop は process 終了／readiness ではなく、approval telemetry は権限ではない |
| global／per-run native hook 合成 | NOT_RUN。新 hash／trust／設定は人間の確認が必要で、bypass／自動 trust しない |
| 同じ native 会話と元の target／model／provider への再開 | 隔離した app-server stdio は PASS。同一 UUID、保持された合成文脈、元 cwd／model／provider／policy、実 owner の正常終了を確認。exec／TUI と managed-PTY 復旧は NOT_RUN |

Traceary は target／source の変更や未知 UUID を、resume plan の実行前に拒否する必要がある。native CLI の挙動は別に測定し、この拒否を provider に期待しない。friendly name／picker／last／fork／新 chat／model／API 課金の fallback は許可しない。native fixture は #2478 service の完成を待たずに検証し、service 側が durable record、実際の旧 owner 非稼働、session 排他、crash／重複の意味を担当する。

製品の selected metadata は ObserveUnbound にだけ入る。native 現在状態には独立検証済み source／順序／age と、commit／read の両方を guard する同じ元の #2464 ObservationStamp、native 不連続の失効、host event rate／台帳上限の認定が必要である。物理 sleep／wake はユーザーが後回しにした NOT_RUN のままとする。Muse native は保留し、Gemini／Kimi は対象外である。この leaf は UI／framework の採用や配布を許可しない。
