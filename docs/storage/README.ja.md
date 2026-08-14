# ストレージモデル

[English](./README.md)

Traceary は、ローカル状態を 1 つの SQLite DB ファイルに保存します。
このガイドでは、何がどこに保存されるのか、現在の schema がどう構成されているのか、`gc` / backup の既定動作が実際に何を意味するのかを整理します。

## ローカルファーストの配置

- 既定の DB path: `~/.config/traceary/traceary.db`
- 上書き方法: `--db-path` または `TRACEARY_DB_PATH`
- file permission: parent directory は `0700`、DB file は `0600` で作成
- 外部のホスト型サービスは使わない: CLI / hooks は同じローカル SQLite ファイルを読み書きする

`traceary store init` は任意です。ストアが必要なコマンドは、必要に応じて DB を作成し、migration を自動適用します。

## 現在の schema

現在の Traceary は次の table を作成します。

### `events`

追記専用の event stream です。note、session 境界、review、prompt、compact summary、command audit の元イベントはすべてここに入ります。

主な column:

- `id`: event identifier
- `kind`: `note`、`command_executed`、`session_started`、`session_ended`、`prompt`、`compact_summary` などの event kind
- `agent`: `codex`、`claude`、`gemini`、`manual` などの論理的な actor
- `session_id`: session grouping identifier
- `body`: audit 以外の kind 向けの人が読む event メッセージ。新規の `command_executed` では空です。新規書き込みの実行記録の正本は `command_audits` ですが、アップグレード前に書かれた行には履歴の body が残ります。履歴の reclaim は #1853 に延期されています。
- `created_at`: RFC3339 timestamp
- `client`: `cli`、`claude`、`codex`、`gemini`、`mcp` などの ingestion path
- `workspace`: 利用可能な場合の補助的な work-context identifier

主な index:

- `idx_events_session_created_at` on `(session_id, created_at)`
- `idx_events_session_created_at_id_desc` on `(session_id, created_at DESC, id DESC)`
- `idx_events_created_at` on `(created_at DESC, id DESC)`
- `idx_events_workspace_created_at` on `(workspace, created_at)`

### `command_audits`

`command_executed` event に紐づく構造化 audit detail です。新規書き込みではこれが保持される実行記録ですが、アップグレード前に書かれた行には reclaim（#1853）が実施されるまで command/input/output の合成コピーが `events.body` に残る場合があります。

主な column:

- `event_id`: primary key かつ `events.id` への foreign key
- `command_text`: 記録した command line
- `input_text`: 保存した command input payload
- `output_text`: 保存した command output payload
- `input_truncated`: input を切り詰めたかどうか
- `output_truncated`: output を切り詰めたかどうか
- `input_original_bytes`: `input_truncated` が true で既知の場合の元 input byte 数
- `output_original_bytes`: `output_truncated` が true で既知の場合の元 output byte 数
- `command_wrapper`: Traceary が確認済みとして認識する wrapper の basename（現時点では `rtk`）。直接実行では空
- `command_name`: report 集計に使う正規化済み executable basename。`rtk git ...` と `git ...` はどちらも `git`
- `exit_code`: 取得できた場合の終了コード
- `failed`: 構造化された実行結果から導出する互換用の失敗フラグ
- `failure_reason`: `none`、`exit_code`、`signal`、`timeout`、`hook_denied`、`host_error`、`unknown` のいずれか

新規書き込みの `failed` は `failure_reason.IsFailure()` から立てます。exit code のない構造化 hook 失敗は `unknown` ではなく `host_error` です。`failed=1` かつ `failure_reason=unknown` は分類器以前の履歴（2026-07-22 より前の schema default）で、restore は残し、新規書き込みは作れません。意味は [failed-flag の意味](../research/failed-flag-meaning.ja.md) を見てください。

Traceary は確認済みのコマンド構造だけを正規化します。直接実行は先頭 token の basename を使い、wrapper として展開するのは観測済みの `rtk <command>` / `rtk proxy <command>` だけです。shell 文字列を実行したり完全評価したりはしません。取得済みの終了コード `0` は常に成功であり、input/output に `failed` のような文字列が含まれていても report の失敗には数えません。report 集計は payload 本文を解析しません。この schema より前の履歴行は、取得していない根拠を推測せず、`command_name=unknown` と `failure_reason=unknown` を明示します。

`input_truncated` または `output_truncated` が true の場合、保存済み payload はすでに上限内の head/tail projection であり、新規 row では対応する `*_original_bytes` column に元の byte 数を記録します。検索 index は `command_text` / `input_text` / `output_text` を `events.body` と独立に持つため、合成 body を消しても audit テキストの検索性は失われません。切り詰められた byte は過去 row から復元できません。

`command_audits.event_id` は `ON DELETE CASCADE` なので、`gc` で event を削除すると対応する audit payload も同時に消えます。

### `sessions`

start/end event から導かれ、session 系コマンドでも更新される session 集約です。

主な column:

- `session_id`: session identifier
- `started_at`: session 開始時刻
- `ended_at`: session が終了済みならその時刻
- `runtime_mode`: 明示的なライフサイクル契約。`interactive`、`one_shot`、`resumed`、`background` のいずれか。履歴行は安全側の `interactive` へ移行する
- `terminal_reason`: 1 つだけ有効な終了理由。`success`、`failure`、`timeout`、`signal`、`aborted_stream`、`legacy_unknown` のいずれか。active 中、または旧バイナリが終了時刻だけを書いた場合は空になる
- `client`: session の client attribution
- `agent`: session の agent attribution
- `workspace`: 補助的な work-context identifier
- `label`: 任意の運用ラベル
- `summary`: 任意の session summary
- `parent_session_id`: 任意の親 session link

Traceary は空の値を `one_shot` と解釈しません。最初の終了理由は不変です。同じ理由の再配送は冪等な no-op になり、異なる理由は fail closed して、保存済みの時刻・summary・理由を変更しません。v0.30 より前の終了済み行には、成功や失敗を作り上げず `legacy_unknown` を使います。追加型のスキーマなので旧バイナリからも読み書きできます。旧バイナリが理由なしで `ended_at` を書いた場合、現行バイナリはその行を `legacy_unknown` として復元します。

主な index:

- `idx_sessions_started_at`
- `idx_sessions_repo_started_at`
- `idx_sessions_parent`

### `memories`

`v0.5.0` で導入した durable memory の集約です。

主な column:

- `id`: durable memory identifier
- `type`: `decision`、`constraint`、`preference`、`lesson`、`artifact` などの taxonomy
- `scope_kind` / `scope_value`: persistence 用に平坦化した typed scope（`workspace`、`agent`、`session_family`）
- `fact`: 抽出・保持する durable-memory の本文
- `status`: `candidate`、`accepted`、`rejected`、`superseded`、`expired` などの lifecycle status
- `confidence`: `low`、`medium`、`high`、`verified` などの confidence
- `source`: `manual` や `extracted` などの source attribution
- `supersedes_memory_id`: 置き換え元 memory がある場合の参照
- `expires_at`: expiry timestamp
- `created_at` / `updated_at`: lifecycle timestamp

主な index:

- `idx_memories_scope_status_updated`
- `idx_memories_type_status_updated`
- `idx_memories_supersedes_memory_id`

### `memory_evidence_refs`

Durable memory に紐づく evidence ref です。

主な column:

- `memory_id`: `memories.id` への foreign key
- `ordinal`: memory 内での安定した順序
- `ref_kind`: `event`、`session`、`url`、`file`、`issue`、`pr` などの参照種別
- `ref_value`: 参照先の値

### `memory_artifact_refs`

Durable memory に紐づく artifact ref です。

主な column:

- `memory_id`: `memories.id` への foreign key
- `ordinal`: memory 内での安定した順序
- `ref_kind`: `file`、`url`、`command` などの artifact 種別
- `ref_value`: artifact の値

## Traceary が保存しないもの

現在の non-goals:

- SQLite file 以外に置く background daemon metadata
- hidden な cloud sync や hosted history service
- line-oriented export format を主 persistence layer にすること
- `schema/sqlite/migrations` に埋め込まれた SQL 以外の migration registry

## migration と互換性

- migration は `schema/sqlite/migrations` からバイナリに埋め込みます
- 通常コマンドの実行前に store initialization が走るため、upgrade 時も migration は自動適用されます
- backup restore では、まず SQLite file をコピーし、その後に store initialization を再実行して newer migration を適用します
- migration `000028` は不変な `run_lineages` と `usage_observation_runs` table を追加します。v27 usage row は書き換えず、attribution 欠落は unknown のままです

任意の手動 schema edit との後方互換は保証しません。持ち運べるコピーが必要な場合は、DB を直接編集する代わりに `traceary store backup create` を使ってください。

## `compact` の既定動作

`traceary store compact` はストアファイルを書き換えます。copy の途中で退役済み
検索インデックス、非 canonical な重複本文、破棄可能な被覆済み transcript 本文を
落とし、残本文を符号化してから vacuum します。

- 既定 retention: `90` 日 (`--keep-days 90`)
- 物理容量回収は書き換えそのもの。in-place `VACUUM` ではない
- 未 refine の破棄対象は、`--force` で機械要約を書くまで残る
- `store search-projection` と `store retention files` は独立コマンドのまま

`--force` のあと、compact は **orphan range** を機械要約します。orphan とは `session_refinements.covers_to` より先にあり、エージェントがもう畳めないイベント範囲です（セッション終了、24h 無活動の stale 扱い、または post-compact での前倒し記録）。session start/end 以外を含む、まだ畳まれていない各範囲に対し、`degraded=1` の refinement（`produced_by=gc:orphan-consolidation`）を書きます。内容はいつ・どの kind が何回・どのコマンドかだけで、エージェントの判断理由（なぜ）は復元しません。lifecycle だけの tail（正しく fold したあとに着く `session_ended` が典型）は `covers_to` だけ進め、機械的な脚注は付けません。`degraded` は「合成テキストを含む」意味のまま、wake 適格性のビットにはしません。wake injection は `has_agent_reasoning` を読むため、正しく fold されたセッションは reduction のあとも注入対象に残ります。この機械 refinement も破棄にとって**有効な被覆**です。破棄が失うのはテキストだけであり、残すと約束している bytes・timestamps・counts はまさにこの要約が保持するためです。出力は orphan 機械要約件数と整理件数の両方を報告します。`--dry-run` は両方を数え、どちらも書きません。この処理に専用コマンドや `--target` はありません。

**その run で機械要約したものは、その run では破棄しません。** 破棄は機械要約より先に走るため、run 開始時点の被覆だけを見ます。dry-run は書き込まずに機械要約するので同じ被覆を見ています。もし apply が先に要約してから破棄すると、preview が数えられなかった本文を失うことになり、それはまさに `--dry-run` が可視化するために存在する損失です。この順序にすることで、preview は構造上そのまま正確になります。ある run が要約した分は次の run で破棄対象になり、その run の preview が先に件数を示します。被覆は増えるだけなので、取り残しは生じません。

target ごとの policy:

- `events`: event row は削除しません。終了済み session で refinement に被覆された古い `transcript` 本文だけを不可逆に retention marker へ置換します。ここでの被覆とは、同一 session の refinement であり、その境界 event も同じ session に属し、範囲が対象 event に届いていることを指します。`created_at` が parse できない event は、年齢を判定できないため破棄しません。event skeleton、`prompt` 本文、他の kind、`command_audits.command_text` / `input_text` は常に残ります。この経路は retention ledger を書きません。review 可能かつ archive から復元可能な経路は `store retention` です。
- `sessions`: `COALESCE(ended_at, started_at) < cutoff` かつ surviving event から参照されていない終了済み session を削除します。active session (`ended_at IS NULL`) は常に保護されます。
- `memories`: `updated_at < cutoff` の `expired` / `superseded` / `rejected` memory を物理削除します。`accepted` と `candidate` は age 削除しません。**例外:** 未レビューの auto-extracted candidate (`source IN (extracted, extracted-hidden, compact-summary)`) は 14 日超で **hard delete ではなく `expired` へ decay** し、keep-days の物理 GC まで restore 可能です（#1368）。物理削除時は evidence/artifact ref が cascade され、削除または decay 直前の行を指す `supersedes_memory_id` は先に NULL へ更新されます。
- `memory_edges`: `valid_to < cutoff` の終了済み edge を削除します。endpoint の memory が削除される場合も edge は自動 cascade されます。
- `all`: events、sessions、memories、memory_edges の順に依存関係を保って適用します。event row が残るため、`delete_empty_sessions.sql` は event 削除によって候補を得なくなります。

fold schema より前の store には被覆の証跡が無いため、破棄候補も存在しません。store を read-only・未 migration で読む `--dry-run` は、そのような store の `events` target について失敗ではなく `0` を報告します。他の target は通常どおり数えます。

将来の破棄理由は additive な sidecar column で表し、`body_availability` の値を増やしてはいけません。CHECK を広げるには `events` の再構築が必要で、additive-migration rollback 契約に反するためです。

実務上の意味:

- `gc` は opt-in であり、Traceary が background で自動削除することはありません
- 被覆済みの transcript 本文だけを破棄したい場合は `--target events` を使ってください
- 長期の監査履歴を残したい場合は、強めの cleanup の前に backup を取ってください
- cold 行の export と **verify-before-delete** は [Archive-before-GC](./archive-before-gc.ja.md)（#1309）を参照。フルファイル backup は [バックアップガイド](../backup/README.ja.md)

## 履歴 content の可逆的な dedupe

**要件。** 初期の hook 発火で、同じ prompt/transcript が二重に書き込まれることがありました。現在の hook 書き込みが抑止するのは、ホスト由来の安定した delivery ID で証明できる完全な再送だけです。その証拠がない同一内容は正当な別イベントとして保持します。履歴上の推定 duplicate group は残り、`doctor` の `content-event-reliability` 警告や context size を膨らませます。クリーンアップは **明示的かつ可逆** でなければなりません。通常の upgrade/migration が `events` 行を移動・削除・書き換えることは決してなく、復元可能な証跡なしに hard delete することもありません（#1227）。

**コマンド。** `traceary store dedupe content-events`

- 既定は **dry-run** で、候補グループを報告するだけで何も変更しません。
- `--apply` で duplicate を隔離します（`events` から移動）。
- `--restore <run-id>` で apply を取り消します。
- `--purge <run-id>` は run の復元可能期間を終了し、隔離された行を破棄してバイトを実際に回収します。SQLite はページを free list へ返すだけなので、ファイルシステムへ返すにはそのあと `VACUUM` を実行してください。
- `--list-runs` は archive に残っている quarantine run を新しい順に一覧します。
- `--client codex`（既定）は Codex に、`--client kimi` は Kimi に限定し、`--client all` はすべての agent を対象にします。hook 由来の duplicate は `client=hook` で書かれるため、セレクタは `agent` で絞り込みます。
- `--strict` は時間差に関係なく完全一致する duplicate group をすべて報告します。
- `--json` は dry-run / apply / restore / purge / run 一覧で利用できます。

**概念モデル。** duplicate group は identity tuple `kind, client, agent, session_id, workspace, source_hook, TrimSpace(body)` です。これは `content-event-reliability` 診断が使う identity と同じですが（診断は後述の retention 除外を適用しないため、診断が報告する duplicate 件数は本コマンドが実際に処理する件数より多くなり得ます）、履歴クリーンアップ用の推定であり、実行時の delivery identity ではありません。書き込み時に再送を抑止するには、ホスト由来の安定した delivery ID と semantic fingerprint の一致が必要で、本文の一致だけでは同一性を証明しません。対象は `client='hook'` かつ `kind in (prompt, transcript)` の行のみで、**command audit は対象外** です。既定では、メンバーがほぼ同時（診断と同様に連続レコードをペアで cluster する 10s の近接 window 内）に書かれた group のみが対象になり、離れた意図的な再送は除外されます。`--strict` はこの window を外します。group ごとに残す **canonical** 行は、parse した `created_at` が最も早いもの（同値は event id が小さい方）です。`created_at` は Go 側で RFC3339Nano として parse します（`formatTimestamp` は可変幅の小数秒を出力するため、辞書順では並びません）。malformed な timestamp を含む group は **スキップして報告** し、変更しません。

**責務。** CLI（`presentation/cli/store_dedupe.go`）が flag を解析し text/JSON を整形します。usecase（`StoreManagementUsecase.DedupeContentEvents` / `RestoreContentEventDedupeRun`）が apply 時に run id と `archived_at` を採番し、入力を検証します。SQLite datasource（`StoreManagementDatasource`）が transaction 内での read/group/move と restore を担います。

**隔離テーブル。** migration `000019` が `event_content_dedupe_archive` を追加します（additive のみで `events` には触れません）。隔離された各行は、元の `events` 行をそのまま復元できる情報を保持します。`id, kind, client, agent, session_id, workspace, body`（正規化前の original）、`created_at, source_hook`、加えて来歴の `kept_event_id`（duplicate_of）、`dedupe_run_id`、`archived_at`、`group_key`、`reason` です。

**apply / restore のセマンティクス。**

- apply は **バッチ単位で commit** され、**冪等** です。2 回目の apply は、すでにクリーンアップ済みの group について `events` に duplicate が残っていないため、何も移動しません。
- バッチが duplicate cluster の一部だけを含むことはありません。近接 cluster は *生き残っている連続行* の間隔を測るため、cluster を途中まで隔離すると内部の間隔が広がり、1 つだった cluster が複数の単独行へ分裂して、再実行しても二度と畳めなくなります。cluster 単位で commit することで、中断しても各 cluster は「完全に隔離済み」か「未着手」のどちらかになり、再実行はクリーンな実行とまったく同じ判断を再現します。checkpoint の状態は不要です。1 バッチ（1000 行）より duplicate が多い cluster は分割せず 1 transaction にします。
- retention pruner が本文を空にした行は **対象外** です。pruning は client / kind を問わずすべての行の本文を同じ固定マーカー文字列へ置き換えるため、空になった時点で、もともと互いに duplicate ではなかった prompt 同士が同一 identity になってしまいます。
- retention の **ledger** 行を持つ行は **アーカイブしません** が、grouping には参加させます。`raw_body_retention_entries.event_id` は `ON DELETE RESTRICT` なので削除すると batch が中断します。かといって scan から外すと identity group から消えてしまい、近接クラスタリングは見えている行同士の間隔を測るため、cluster の中央にある ledger 行を隠すとその前後の間隔が広がって cluster が分裂し、retention とは無関係な通常の duplicate が取り残されます。したがって cluster の member としては残し、duplicates からのみ除外します。
- restore は **all-or-nothing** で上書きを拒否します。元の event id がすでに `events` に存在する場合、restore 全体が失敗し何も変更しません。
- duplicate は `events` の *外* へ移動されるため、通常の `list`、`sessions --snapshot`、`doctor`、`context` の read surface からは自動的に見えなくなります。

**rollback。** apply を取り消すには `traceary store dedupe content-events --restore <run-id>` を実行します（run id は `--apply` が出力し、隔離した各行にも記録されます）。run id が出力される前に apply が中断した場合は `--list-runs` で見つけられます。バッチは run id が報告される前に commit されるため、これがないとその行は restore / purge のどちらからも到達できません。念のためのコピーが欲しい場合は、`--apply` の前に `traceary store backup create` を取得してください。

**バイトの回収。** 隔離は duplicate を移動するだけで、領域は解放しません。隔離された行を破棄するのは `--purge <run-id>` だけで、解放されたページをファイルシステムへ返すのはそのあとの `VACUUM` だけです。

**振る舞いテスト。** dry-run の報告と非変更、apply ＋冪等性、restore ＋上書き拒否、malformed timestamp のスキップ、command-audit / 非 hook の除外、retention で空になった行の除外、retention ledger 行が cluster を保つこと、strict と近接スコープ、batch size に依存しないこと、部分 apply からの再開、run 一覧、purge、read surface の除外は `infrastructure/sqlite/content_event_dedupe_test.go` で、cluster を分割しない不変条件は `infrastructure/sqlite/content_event_dedupe_batch_internal_test.go` の純粋関数上で直接、flag 配線と JSON/text 出力は `presentation/cli/store_dedupe_test.go` で、run id の採番は `application/usecase/store_dedupe_test.go` でカバーしています。

## 本文を含まないワークスペース識別診断

migration `000023` は `hook_delivery_attempts` を追加します。各行が保持するのは配送レコード ID、試行イベント ID、結果（`accepted`、`conflict`、`exact_redelivery`）、由来（`runtime` または `backfill`）、時刻だけです。既存の `hook_deliveries` 各行から accepted/conflict 試行を 1 件ずつ作りますが、`backfill` と明記し、イベント本文はコピーしません。リリース品質の率は runtime 試行だけを使うため、seed 行だけで未測定の rollout が合格することはありません。実行時の配送と試行は同じ transaction で書き込みます。

試行イベント ID は、Traceary がフックコールバックごとに作る repository identity です。後続のホストコールバックには新しい event ID が割り当てられるため、新しい試行行になります。`INSERT OR IGNORE` が抑止するのは transaction race 後に同じ event object を内部 retry する場合だけで、repository retry の仕組みがホスト配送率を水増しすることを防ぎます。

`session_workspace_aliases` は運用者が明示的に確認した情報を保持します。別名は `sessions.workspace`、`events.workspace`、観測時点の関係を一切書き換えません。読み取り projection だけが、確認情報と一致する保存済み競合を `explicit_alias` に変更するため、別名の削除で完全に元へ戻せます。

`traceary report workspace-identity` は読み取り専用で、migration や provenance catch-up を実行しません。先に `traceary doctor` で store を初期化または migrate してください。未準備の store では案内付きで失敗します。既定経路はイベント本文を読み込みません。observation 行の関係件数は volume のまま残し、`conflict_pair_count` は現行 conflict の distinct `(session_id, workspace)`、conflict sample は pair あたり最新 1 行で `workspace` を含みます。`--include-heuristic` を指定した場合だけ、正の `--heuristic-limit` を `MaxScanRows` として既存の dedupe 計画を `Apply=false` で呼び出します。本文を含まない件数取得により、上限付きの `partial` サンプルと `complete` 測定を区別します。上限付き apply は拒否されるため、クリーンアップは引き続き別の全件対象・明示的・可逆なコマンドです。意味は [workspace-conflict の意味](../research/workspace-conflict-meaning.ja.md) を見てください。

## ペイロード codec バックフィル

既存の `events.body` は、writer を凍結せずにバージョン付き zstd codec で
その場書き換えできます。詳細は [`payload-backfill.ja.md`](payload-backfill.ja.md)。
物理的なファイル縮小は `store compact` の後にだけ現れ、検索 projection は
`drifted`/`stale` で終わるため rebuild が必要です。

live writer（bundle import、archive restore、dedupe restore、raw-body recovery）
は native hook insert と同じ canonical encoder（縮むとき zstd）を使います。
bundle / archive ファイルは plaintext のままです。retention marker は identity
のまま（apply/verify が stored TEXT を sentinel と比較するため）。
[`../research/payload-codec-call-sites.ja.md`](../research/payload-codec-call-sites.ja.md)
を参照。

## backup の既定動作

サポートする backup 導線は意図的にシンプルです。

- `traceary store backup create` で compact な SQLite backup file を出力
- `traceary store backup restore` で destination DB path へその file をコピー
- restore 後に、現在の binary がより新しい schema version を知っていれば migration を再適用

マシン移行や破壊的 restore の注意点は専用ガイドを参照してください。
[`../backup/README.ja.md`](../backup/README.ja.md)

## 運用透明性のチェックリスト

ローカルで Traceary が何をしているか確認したいときは、次の順で見ると把握しやすいです。

1. `traceary doctor` で解決された DB path と書き込み可否を確認する
2. 正確な SQL が必要なら `schema/sqlite/migrations/` を見る
3. 手動調査や危険な cleanup の前に `traceary store backup create` を実行する
