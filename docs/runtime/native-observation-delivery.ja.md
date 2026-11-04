# Native 観測配送の設計チェックポイント

[English](native-observation-delivery.md)

状態は **修正済み実装前契約を主担当が採用し、domain・Runtime・selected wire/SDK・CLI・opt-in hook 配送を実装済み、leaf 最終検証・独立レビュー・delivery は未完了** です。
独立 report の CHANGES_REQUIRED は変更せず、明示 triage と修正契約を保持します。実装の認定ではありません。
[観測配送の leaf](https://github.com/duck8823/traceary/issues/2466) の実装前設計資料であり、新しい CLI command の利用案内や native host の認定ではありません。
[採用済み headless R0](../adr/2026-11-03-headless-runtime-r0.ja.md) を正本とします。
末尾の具体的な契約と checkpoint 採否に従って実装します。最終の独立実装レビューと native host 認定は別の gate です。

## 要求と既存の境界

既存 native hook の capture は daemon がなくても動作する必要があります。
任意の live 配送は導出、redaction 済み metadata のみを運び、prompt、code、audit、memory の本文、credential、terminal input、承認判断、停止要求、再開指示を運んではいけません。
queue 受付の成功は、native 出所の認証や入力可能状態の証明ではありません。

現在の `application/runtime/daemon.go` にある `ObservationMetadata` と `Daemon.Observe` は未信頼 metadata の transport です。
既存の owner-private Unix transport と launch identity は再利用できる境界ですが、native hook と run の対応を証明しません。
`infrastructure/runtime/pty_unix.go` の `launchEnvironment` は外部から継承した runtime 変数を除去して run/generation/epoch の hint を設定しますが、それらの一致、cwd や native session ID だけで観測を bound にしてはいけません。

[従来の観測 ADR](../adr/2026-10-18-desktop-runtime-observations.ja.md) は、descriptor path を native hook の invocation ごとの引数で渡す案を示しています。
すべての host での対応や、host 設定方式の採用を証明したものではありません。
host/version ごとに一意な伝播と非破壊な hook 合成を実証する必要があります。
descriptor path は対応付けの情報であり、入力や process 制御の権限を与えません。
伝播を検証できなければ観測は unbound、native interaction は unknown のままとし、wrapper や cwd/PID による代替は認めません。

## 概念モデルと責務

| 概念 | 所有者 | 不変条件 |
| --- | --- | --- |
| 導出した観測 | domain value | 閉じた有界 metadata とし、本文や効果を持つ arm を含めず、native 出所の主張は未信頼とする |
| managed binding descriptor | runtime metadata adapter と不変の domain binding | store/worktree/profile host/version/run/generation/epoch と物理 witness を照合し、保存 PID に権限を与えない |
| native hook context | 既存 host CLI adapter | native payload は一度だけ読み、元の capture、receipt/spool、stdout/exit 判断を保持する |
| 任意配送 | application port | 全体を一つの単調時計 deadline で制限し、処理中の数を制限し、自動 replay や主結果の変更をしない |
| local transport | 既存 daemon adapter | 観測 endpoint のみを使い、daemon 自動起動や管理操作をしない |
| native の因果証跡 | version ごとの adapter と reducer | descriptor の対応付けだけでは native 状態や承認表示を確定しない |

identity と上限は domain が所有し、JSON、file、socket の詳細を持ち込みません。
application は任意配送の調停を担当し、host 判断の整形や SQL への直接 access を行いません。
infrastructure は descriptor の no-follow read、root lock、atomic publication と wire deadline を担当します。
主 capture と host の stdout/exit 整形は既存 CLI hook の責務です。
既存の対象単位 service に capability を加える設計とし、hook ごとの新しい class や二つ目の target Registry は作りません。

## 修正版の実装前レビューに出す具体的 v1 契約

この節は、先行する未決定の表現・schema の比較案に優先する checkpoint 入力です。実装や native 認定の完了ではなく、受入済み R0 の範囲内です。host 設定の導入や product-wide な設計変更は採用しません。

### Descriptor と receiver の所有

既存 Runtime が所有し `execution.json` に保存する immutable な完全 launch snapshot を使います。明示 selection は既存 OutputSelection と同じ epoch/run/generation/worktree/profile です。client は descriptor file を読みません。同一 epoch の live・publish 成功済み owned run と全 selection・profile host/version を照合し、保存 record/PID、reserved/exited/uncertain、環境変数や native ID の一致では受理しません。admission 前は Runtime.mu 内の immutable な owned snapshot のみを照合し、file・Registry・subprocess を参照しません。物理的な TargetRegistry.Revalidate は消費時に owner lock 外の有界 worker context で実行し、その後同じ owned run の identity/state を再確認してから sample を保持します。失敗・timeout では sample を捨てて degraded とし、queued を消費完了と扱いません。native 引数伝播が未認定なら telemetry は unbound のままです。

新 selected intake は既存 Runtime が所有し、legacy ObservationReceiver とは分離します。既存 daemon の単一 observation queue/worker で admission 前と消費時に照合します。main の既存 RuntimeFactory を使った実 composition で動作させ、legacy observe の nil receiver は引き続き unsupported とします。二つ目の queue・drainer・ring は作りません。live run ごとに最大 4 KiB の最後に到着した untrusted sample を一つだけ保持し、全体を 256 個・1 MiB 以下に制限します。実 Wait 完了・uncertainty・owner shutdown・epoch 失効で消去します。重複・遅延・順不同は native 状態・入力許可・core Session を変更しません。後続 reducer は同じ所有境界を拡張できますが、この sample を新しい因果的証拠として再生してはいけません。

### Wire と版の相違

outer protocol 1 と legacy observe は変更せず、operation observe_selected_v1 と request arm selected_observation を追加します。legacy への fallback はしません。closed な selected schema は `{selection:{epoch,run,generation,worktree,profile}, metadata:{host,host_version,event_id,kind,native_session_id?,native_turn_id?}}` です。operation 名を版の識別に使い、重複した nested version field は追加しません。必須欠落・null・重複 key・後続 JSON・未知 field・複数 arm を拒否します。protocol 1 の非 null・closed field 制約は維持します。protocol 1 として正しい field を持つ未対応 operation（例 observe_selected_v2）は unsupported、未知 field・null・壊れた JSON は invalid です。future operation を selected v1 として解釈せず、legacy に再送しません。outer decoder の特例化や緩和はしません。TargetID の意味を推測せず、新 schema には含めません。host/version は固定 profile と一致させます。kind は prompt_boundary/tool_boundary/turn_boundary/session_boundary のみです。event/native ID は有界な相関識別子であり本文ではありません。selected value は 4 KiB 以下、既存 frame は 64 KiB 以下です。旧 daemon は未知 arm に相関しない invalid response を返すため、新 client は既存の protocol error のまま invalid として扱い、request ID 検証を緩めず別 schema に再送しません。総 budget 外の capability pre-check は追加しません。

observations endpoint だけが受理し、実 runtime intake がある場合だけ status が observe_selected_v1 を広告します。これは native host 認定ではありません。成功 ACK は相関した queued のみで、effect/approval/data arm を含めません。ACK は admission だけであり、消費・core commit・native association・可視の承認待ちを保証しません。既存 SDK 全体で request one-of と混在 reply arm を拒否し、別 lifecycle branch との統合時も再確認します。frame/byte/queue 上限と degraded 表示を維持し、payload を含まない既存の invalid/unsupported/changed/not_found/rejected/busy error code を使います。

### Operator と hook

public runtime observe は stdin と位置引数を読みません。明示的な絶対パスの既存 runtime-dir、epoch/run/generation/worktree/profile/host/host-version/event-id/kind を必須 flag とし、native-session-id/native-turn-id は任意の相関値です。既定 root・環境選択・native association は推測しません。不正 flag は nonzero、正しい optional 配送の結果は `{schema_version:1,outcome,binding:"unbound",authority:"admission_only"}` の closed JSON 一つを出して zero とします。outcome は queued/unavailable/unsafe/unsupported/busy/rejected/changed/not_found/invalid/timeout です。出力失敗は CLI error とし、daemon/core を自動作成しません。

既存 hidden hook group に observe-runtime-dir/observe-epoch/observe-run/observe-generation/observe-worktree/observe-profile/observe-host-version の opt-in flag を追加します。不完全・不正・未指定なら optional 配送だけを省略します。host/kind は既存の対応 client/action、event ID は取得済み spool receipt、native ID は既に読み込んだ payload の対応 allowlist field からのみ導出し、stdin を再度消費しません。未対応 client/action は省略します。global/per-run 設定の導入、argv policy、auth/model/PATH は変更せず、実 native 伝播・合成の版別認定は host leaf に残します。

primary capture 成功と現在の spool clear の後、backlog drain の前だけで optional 配送します。spool 不使用 fallback は primary 成功後に配送できますが、新 spool は作りません。defer/failure・backlog replay・clear 失敗からは配送しません。packaged hook の残り時間が 250 ms と既存 drain reserve の和より多い場合だけ開始し、それ以外は省略します。hook stdout/stderr・return 値・receipt/event ID を変更しません。

### 総 deadline・検証・rollback

optional metadata 導出と root 検証の前から単調時計の 250 ms を一つだけ使い、connect/handshake/write/ACK すべてを含めます。各 phase で budget を更新しません。既存 transport worker slot は実終了まで保持します。hook の導出と配送も process/service ごとに一つの保持 slot、panic containment、不変入力、cancellation check を使い、cancel を無視する callback が際限ない置換 worker や primary の時間超過を起こさないようにします。retry・遅延再送・新しい durable spool は禁止です。server の物理 target 再検証は消費時だけ、owner lock 外の既存 management budget を上限とする context で実行し、無応答の依存に対しても既存 connection/queue/worker 上限を維持します。

RED/GREEN では、main 相当の実 composition と owner の実 API で selected sample の受理・参照を確認します。全 selection/profile/host/version/state 不一致、物理 target 交換、dequeue 時の終了/epoch 失効、sample 上限と消去、旧新 socket、全 request/reply arm、future/null/重複/未知 field、各 phase の総 deadline、slot を保持した無応答 callback、queue 圧力、panic/cancel・非再送を検証します。観測後も metadata inventory/digest/export/forget と全 prefix が不変であることを実 reader で確認します。hook の stdout/exit/receipt/core/spool の比較、明示 flag のみの選択、一度だけの payload 読取、未認定 native は unknown、backlog 非配送、drain reserve 維持を検証します。rollback は selected optional 配送だけを無効にし、capture/history/spool/native 会話は保持します。実装後の独立 review・新 head CI/security・統合 wave は引き続き必須です。

### 修正版 checkpoint の採否

独立 round 2 は、先行する表現・schema・所有の指摘が解決し、人間による追加 architecture exception は不要と確認しました。admission 時の物理参照による不要な遅延リスクは、上記のとおり消費時の再検証へ変更します。admission は in-memory な正確な descriptor だけを照合し、物理再検証が失敗した場合は sample を保持せず native 権限も作りません。queued は検証成功の保証ではありません。旧 daemon に対する outcome は推測した unsupported ではなく invalid です。

現在は initial SaveLaunch の完了前に run が map に入るため、成功後だけ設定する owner 専用の published marker を追加します。admission は marker を要求し、owned run のすべての uncertainty 経路で marker と sample を消去します。SaveLaunch を保留した状態と、その後の成功/失敗を実テストで検証します。marker と sample は永続化せず、保存 run の adoption に使いません。

hook の導出 table は canonical client codex/claude/antigravity/grok に限定し、prompt は prompt_boundary、audit は tool_boundary、transcript は turn_boundary、session start/end は session_boundary、session stop は turn_boundary とします。対応しない command/action/client（gemini/kimi/muse を含む）は既存 capture を変えず optional 配送だけを省略します。host leaf が証明済みの対応を追加できますが、telemetry の境界を process exit の事実とは扱いません。raw の CHANGES_REQUIRED report と明示 triage を保持し、修正済み source 契約は主担当が採否を所有します。第三回の自動設計レビュー往復や実装承認は行わず、実装後の独立 review は必須のままです。

## 現在の実装証跡の範囲

source 上では、不変 metadata 値と四つの closed kind、owned selection/profile の正確な in-memory admission、SaveLaunch 成功後の publication marker、owner lock 外の物理的な消費時再検証、live run ごとに一つの untrusted sample、Wait/uncertainty/disable/検証失敗・取消時の消去を実装しています。実際に initial journal を保留するテスト、物理再検証が停止している間に owner が終了するテスト、取消を無視して戻る validator のテストを実行します。最後のケースでは古い telemetry の保持を再現し、process/control metadata を変更せずに消去する修正を行いました。

wire/SDK は、closed な observe_selected_v1、正確な field 名と raw 4 KiB 上限、全 arm の request/ACK guard、共有 queue から実 RuntimeFactory への消費、degraded 表示と総 client budget を実装しています。実 socket/有限 PTY で、成功・不一致・旧 peer の invalid response・物理 store 交換・停止した metadata lock・queue 圧力を検証します。

public CLI と optional hook flags/配送は実装済みです。host-native association・lifecycle 統合認定・leaf 全体の delivery は未完了です。owner capability だけで production transport 配送が利用可能になるわけではありません。元の leaf 全体の scope と、残る独立実装レビュー・CI/security・wave gate は維持します。

既存の lowercase key 検査では Go の case folding による Unicode long-s alias が受理されることを再現しました。新 selected request variant は正確な field 名を検査し、bare queued ACK は正確な canonical header を要求します。共通の version/null/重複検査と永続 metadata codec は変更しません。これは protocol 検査であり、native origin の認証ではありません。

## CLI と opt-in hook の実装

`traceary runtime observe` は明示的な絶対 `--runtime-dir` と epoch/run/generation/worktree/profile/host/host-version/event-id/kind を必須とし、native-session-id/native-turn-id は任意です。stdin・位置引数・環境や既定 root を読みません。正しい optional 配送は schema_version/outcome/binding/authority の JSON 一つを出し、binding は常に unbound、authority は admission_only です。不正な operator flag と出力失敗は error にします。別 process の実 main/daemon と有限 fixture による queued、HOME/core 非作成、元の PTY 出力・終了の維持を検証します。

hidden hook に observe-runtime-dir/epoch/run/generation/worktree/profile/host-version の明示 flag を追加します。既存 command に引数を追加するだけで、packaged/global/per-run 設定、auth/model/PATH を変更せず二つ目の handler を作りません。native host の合成・伝播は未認定です。source の引数合成 fixture は同じ既存 handler が一度だけ capture し、stdout/exit・spool が維持されることを確認するもので、host 設定の合成を認定しません。

配送全体は primary 成功と current spool clear の後、backlog drain の前です。失敗・defer・clear 失敗・replay は配送せず、残時間が 250 ms + drain reserve 以下なら省略します。spool 保存失敗の既存 fallback も primary 成功後だけ配送します。単一の process-wide slot が導出と client の実終了まで保持され、取消を無視する callback/client に置換 worker を増やしません。panic/error/timeout は元の結果・出力を変更しません。payload は取得済みの不変文字列から参照し、stdin を再消費しません。event ID は取得済み spool receipt です。native ID は codex の thread_id、他の対応 host の session_id と任意 turn_id を untrusted な文字列としてのみ参照し、不正値は省略します。native association の証拠としては扱いません。
