# CLI リファレンス

[English](./README.md)

このページでは、公開 CLI の挙動をコマンド別にまとめています。
導入直後は `README.ja.md` のクイックスタートと合わせて参照してください。

## 共通ルール

- DB path の解決順: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- 更新系コマンドは既定で読みやすいテキスト形式を出力します
- イベント / セッションの識別子を返すコマンドは、スクリプト向けに `--id-only` をサポートします
- 構造化出力を持つコマンドは `--json` をサポートします
- CLI 出力の JSON/NDJSON contract test は [`../operations/json-contract-tests.ja.md`](../operations/json-contract-tests.ja.md) にまとめています。

## イベント記録コマンド

### `traceary log <message>`

note event を追記します。

既定値:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / 検出した workspace
- `--session-id`: flag → `TRACEARY_SESSION_ID` → 解決した workspace の最新 non-stale active session → `default`

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--exit-code`
- `--failure-reason`（`none`、`exit_code`、`signal`、`timeout`、`hook_denied`、`host_error`、`unknown`）
- `--id-only`
- `--json`

session 解決ルール:

- 明示 `--session-id` または `TRACEARY_SESSION_ID` を最優先
- それ以外では、解決できた workspace に対応する最新の non-stale active session を再利用
- `remote.origin.url` が無い Git worktree でも、work-context key として worktree ルートパスを使います
- workspace を解決できない、または一致する active session が無い場合は、従来どおり `default` session ID を使います

> **注意:** `log` と `audit` は `--session-id` の値をそのまま受け入れ、存在確認は行いません。これは意図的な設計です。hook では高頻度にイベントを書き込むため、毎回 DB ルックアップを挟むとオーバーヘッドが大きくなります。存在しない session ID を渡した場合でもイベント自体は記録されますが、session 単位のクエリには現れません。

### `traceary audit <command> [<input>] [<output>]`

コマンド実行の監査イベントを記録します。

入力方法:

- command だけの位置引数: `traceary audit "go test ./..."`
- 位置引数: `traceary audit "go test ./..." '{}' '{}'`
- named flags: `traceary audit --command "go test ./..." --input '{}' --output '{}'`

主な flag:

- `--command`
- `--input`
- `--output`
- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--id-only`
- `--json`
- `--allow-secrets`
- `--max-input-bytes`
- `--max-output-bytes`

command audit の input/output payload は、解決後の上限を超えると保存前に切り詰められます。上限の優先順位は `--max-*-bytes` flag、`TRACEARY_MAX_AUDIT_*_BYTES`、`~/.config/traceary/config.json` の `audit.max_*_bytes`、組み込み既定値です。切り詰め後も head / tail の文脈、構造化された `input_truncated` / `output_truncated` metadata、`original_bytes` marker は残ります。省略された byte は `traceary show` でも復元できません。

**read 面**（`list`、`search`、`context`、handoff の recent-command ペイン）では、大きい host-tool payload（`Edit` / `Write` / `Read` / shell）を tool-aware な compact summary（tool 名、path、rune 数、content hash、head/tail、`traceary show <event_id>` の retrieval hint）に投影します。これは presentation 時の投影のみで、永続化された raw 本体と `traceary show` はフル fidelity のままです。

command string も、保存前に組み込みの best-effort secret redactor を通ります。`input_redacted` / `output_redacted` は input/output payload の redaction だけを表し、command redaction 専用 flag はまだ出しません。

新規 audit は raw command に加えて、wrapper / executable を分離した正規化済み識別子と構造化された実行結果を保存します。取得済みの `--exit-code 0` は常に成功で、引用された失敗文言が結果を変えることはありません。構造化された根拠がない場合、payload 本文から推測せず `failure_reason=unknown` のまま保存します。

session 解決ルールは `traceary log` と同じです。

## 参照・検索コマンド

### bare `traceary`

subcommand なしの `traceary` は TTY / 非 TTY とも常に help を表示します。旧 operator cockpit（`traceary tui` / `traceary dashboard` と、それを開いていた bare TTY 既定動作）は v0.34 の非推奨期間を経て v0.35.0 で削除されました。`traceary list`、`traceary search`、`traceary doctor --json`、`traceary session handoff`、`traceary memory inbox list` などの明示的な read command を使ってください。root は `traceary --db-path … <subcommand>` が有効なままになるよう `--db-path` を受け付けます。cockpit 専用だった `--reset-state` は削除済みです。孤立した local state ファイル `~/.local/state/traceary/cockpit.json`（または `$XDG_STATE_HOME/traceary/cockpit.json`）は手動で削除して安全です。

### `traceary list`

最近の event を一覧表示します。

`list` は直近履歴を素早く絞るためのコマンドです。kind / client / agent / session / workspace が決まっているときはこちらを使い、キーワード検索や期間条件が必要なときは `search` を使います。

`list` と `search` は、開始を含み終了を含まない 1 つの期間を使います。RFC3339 の `--from` は含み、`--to` は正確な時刻として含みません。日付だけの `--to YYYY-MM-DD` は、その日を含めるため翌日の現地午前 0 時へ解決します。日付だけの値には `--timezone <IANA名>` を使い、既定値は明示的に UTC です。端末のローカルタイムゾーンを暗黙に使いません。ホストごとに意味が変わる Go の特殊なゾーン名 `Local` は拒否します。終了を省略した場合は、コマンド開始時の 1 つのスナップショット時刻に固定します。

デフォルトのテキスト出力はコンパクトな 1 行形式 (`HH:MM:SS  kind  agent=<agent>  sess=<先頭8文字>  ws=<basename>  message`、ヘッダ無し、現地時刻) です。`--wide` で従来のタブ区切り表、`--utc` でテキスト出力を UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前の出力を完全再現します。明示的な `--fields` がない JSON は従来の event key を維持します。JSON で `--fields` を明示すると選択した key だけを出力し、`message` を含まない `list` / `search` は本文を読まないメタデータクエリを使用します。`--fields ts,kind,message` でフィールドを選択できます（テキストでの優先順位: `--fields` > preset fields > `~/.config/traceary/config.json` の `read.fields` > 組み込み既定値）。`--fields` は `--wide` と併用できません。利用可能フィールド: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`, `source_hook`。`--preset <name>` で保存済みビューを適用できます。built-in は `failures` / `prompts-only` / `compact-summaries`、`read.presets` に定義したユーザー preset が同名 built-in を上書きします。明示した `--kind` / `--failures` / `--workspace` などのフラグは常に preset より優先されます。`--wide` / `--json` のときは preset の fields 指定は無視されますが、filter は有効です。`--color=auto|always|never` でコンパクト行の ANSI ハイライトを切り替えられます（既定は `auto`、`NO_COLOR` 環境変数でも無効化可、`--wide` / `--json` では適用されません）。ハイライトが有効な場合、失敗した `command_executed` は赤+太字、`prompt` と `transcript` は cyan、`compact_summary` は magenta、`session_started` / `session_ended` は dim で表示されます。

主な flag:

- `--kind`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--from` / `--since`
- `--to` / `--until`
- `--timezone`
- `--failures` — この 記録 フィルタは残す。`command_audits.failed = 1` または取得済みの非ゼロ `exit_code` に一致する。現行の書き込みは構造化された host のツール失敗を `unknown` ではなく `host_error` として保存する。分類器以前の `unknown`+`failed=1` はフラグ側で一致する。意味は [failed-flag の意味](../research/failed-flag-meaning.ja.md) を見てください。
- `--follow` — 新しい一致イベントを追跡表示する（旧 `traceary tail`）。`--limit 0` は新規のみ。`--json` はスナップショット配列ではなく NDJSON。`--offset`、`--from`/`--since`/`--to`/`--until`、`--sensitive`、`--source-hook` とは同時に使えない。
- `--follow-session <prefix>` — `--follow` 時に 1 つの session へ先頭一致（最低 8 文字）。

### `traceary search [<query>]`

全文検索と構造フィルタで event を検索します。

`search` は `events` を新しい順に走査し、上限付きの候補を復号して本文一致を判定します。世代が complete になると、その literal fingerprint による pre-filter で一致しない候補の復号を省略でき、session tier によって別グループ **SESSIONS** も利用できます。SESSIONS 行は要約またはキーワードの本文が query に一致した session です。古い event のグループでも、一致した event 行でもありません。

セッション行は、その trail のどこかに検索条件に一致する活動があることを示します。`--from` / `--to` では session summary クエリと同じくセッションの開始時刻で選び、`--failures` はそのセッション内に失敗したコマンドが1つでもあれば満たします。セッション行に対する filter は単一の event ではなくセッション全体に適用されるため、query、期間、`--failures` がそれぞれセッション内の別の活動によって満たされても、そのセッションは表示されます。すべての filter が1行の event だけを絞り込むのは event 階層です。

complete な世代はスナップショットなので、その後に記録された event は `events` から直接読み、同じ結果に統合します。再構築の合間に検索結果が古くなることはありません。世代が complete になる前も、候補を直接復号して本文一致は正しく返します。速度は落ち、また session tier はそれまで参照を拒否されるため SESSIONS グループは空になります（#1844）。stderr の通知は `traceary store search-projection status` を案内します。state ごとに必要な操作は `docs/search-projection-rebuild.ja.md` にまとめています。準備状態を確認できない場合も、同じ status command で空のグループの意味を確認できます。候補予算を使い切った場合は部分的な結果を返さず `index_incomplete` を報告します。projection が complete になると fingerprint pre-filter と session tier が利用可能になります。どの state にどのコマンドが必要かは `docs/search-projection-rebuild.ja.md` を参照してください。世代が rebuilding の間、`start` は拒否されます。

この command をかつて支えていた全文コーパス版 migration-032 索引は v0.34 で退役しました。読み書きはされず、`traceary store compact` が書き換え時に落とします。詳細は [検索インデックスの退役](../operations/search-retirement.ja.md) を参照してください。

本文一致だけの結果では、テキスト出力は `list` と同じコンパクト 1 行形式 (デフォルトで現地時刻) です。両方のグループがあるときは `EVENTS (literal matches)` と `SESSIONS (summary or keyword matches)` のラベル付きグループになります。日本語表示では `EVENTS（本文一致）` と `SESSIONS（要約・キーワード一致）` です。`--wide` で従来のタブ区切り表、`--utc` で UTC に切り替えられます。`--wide --utc` を組み合わせると v0.6.1 以前の event 行形状を再現します。`--json` は `{"events": [...], "sessions": [...]}` を出力します。どちらのキーも常に存在し、ヒットがない tier は空配列です。明示的な `--fields` は `.events` 内の event フィールドだけを選び、session オブジェクトは固定形状（`session_id` / `summary` / `event_count` / `started_at`）のままです。`--fields ts,kind,message` でコンパクトカラムの順序を上書きできます (優先順位: `--fields` > preset fields > config.json の `read.fields` > 組み込み既定値)。`--fields` は `--wide` と併用できません。利用可能フィールドは `traceary list` の説明を参照してください。`--preset <name>` で保存済みビューを適用できます。filter を持つ preset なら free-text query なしでも検索条件が揃うので、preset-only な検索も成立します。

期間 filter は `traceary list` と同じ要求値・実効値の規則を使います。日付だけの終了日は指定した暦日を含み、`--timezone` は明示的（既定は UTC）、RFC3339 の終了は正確な排他時刻のままです。

主な flag:

- `--kind`
- `--client`
- `--agent`
- `--workspace`
- `--session-id`
- `--since`
- `--until`
- `--timezone`
- `--limit`
- `--offset`
- `--json`
- `--wide`
- `--utc`
- `--fields`
- `--preset`
- `--color`

### `traceary timeline`

ギャップ検出による作業タイムラインを、ワークスペース単位のアクティビティ要約付きで表示します。

`timeline` は直近のイベントをアイドルギャップ（デフォルト 15 分）で区切って連続する作業ブロックに分け、各ブロック内で workspace ごとに整列された 1 行を表示します。ワークスペース単位のアクティビティ要約は **`compact_summary` → 最初の `prompt` → kind counts** のフォールバック順で選ばれ、そのブロック内でそのワークスペースに存在するシグナルが 1 行に展開されます。デフォルトのテキスト出力は現地時刻 (local time) で、`--utc` で UTC に切り替えられます。`--json` は UTC RFC3339Nano の `start` / `end`、数値の `duration_sec`、および `workspace_breakdown` 配列 (`{workspace, event_count, kind_counts, agents, summary, summary_source}`) を出力します。

主な flag:

- `--workspace`
- `--from`
- `--to`
- `--gap` (アイドルギャップ閾値/分)
- `--limit`
- `--json`
- `--utc`

### `traceary replay`

最近のセッション・イベント・durable memory を single-file HTML で書き出します。外部スクリプト・フォント・CDN に依存しない自己完結ファイルなので、オフラインでも閲覧可能です。インシデントレビュー・週次 retrospective・CLI を持たないチームメンバーへの共有に使います。

主な flag:

- `--out` (必須) — 書き出す HTML のパス
- `--sessions` (既定 10) — 含める直近セッション数
- `--events-per-session` (既定 20) — 1 セッションに含めるイベント数
- `--memories` (既定 20) — 含める accepted memory 数
- `--timeline-blocks` (既定 20) — timeline パネルに描画するブロック数。0 以下でパネル自体を省く
- `--hotspots` (既定 10) — failure hotspot パネルに描画するクラスタ数。0 以下でパネル自体を省く

replay HTML は sessions / timeline blocks / failure hotspots / durable memories の 4 パネル + generated-at footer の構成です。timeline と hotspot パネルは `traceary timeline` / `traceary list --failures-only` と同一意味を持つので、両方の描画を相互参照できます。

例: `traceary replay --out /tmp/replay.html`

### `traceary show <event-id>`

1 件の event を詳細表示します。

主な flag:

- `--json`

### `traceary context`

別 session や別 tool に渡すために、直近の生イベント列を表示します。

主な flag:

- `--session-id`
- `--workspace`
- `--limit`
- `--json`

### `traceary session handoff`

session metadata、recent commands、compact summary、accepted durable memories から組み立てた handoff summary を表示します。`--compact-only` を付けると、prompt injection 向けの短い summary を出力します。`--compact-only` 指定時は `--recent` 未指定なら 3 に自動設定されます。

構造化テキストは互換用の `RECENT_COMMANDS` 文字列一覧を維持し、兄弟セクション `RECENT_COMMAND_ITEMS` を追加します。各項目は event ID、応答・保存・元データの byte 数、応答時の切り詰め有無、明示的な詳細取得コマンド `traceary show <event-id>` を示します。handoff は本文の上限付き先頭部分だけを読み、command payload 全文をメモリに展開しません。

主な flag:

- `--session-id`
- `--workspace`
- `--recent`
- `--memories`
- `--preset` (任意): durable memory に built-in preset (`resume` / `review` / `incident`) を適用
- `--as-of` (任意): durable memory の validity を指定時刻 (YYYY-MM-DD または RFC3339) で評価する。既定は「現在」
- `--compact-only` (任意): prompt injection 向けの短い summary を出力 (`compact-summary` の代替)。`--recent` 未指定時は 3 に自動設定

> **v0.14 移行**: 旧 top-level の `traceary handoff` / `traceary compact-summary` alias は v0.14.0 で削除されました。実行すると Cobra の generic unknown-command で終了します（具体的な migration-error stub は v0.20.0 で撤去）。`traceary session handoff`（必要に応じて `--compact-only`）を使ってください。v0.14 で削除された alias 一覧は [CLI 安定性と非推奨ポリシー](../cli-stability.ja.md) を参照してください。

## Durable memory コマンド

`traceary memory ...` は intent 別に namespace を分けています。日常 read 用途のコマンドは top-level に残し、それ以外の verb は 3 つの namespace に整理しています。flat な verb (`memory remember` / `memory accept` / `memory hygiene scan` ...) は v0.14 の 1 マイナー互換ウィンドウを経て v0.15.0 で削除されました。スクリプトやドキュメントは下記の canonical な `memory inbox` / `memory store` / `memory admin` path を使ってください。歴史的な対応表は [memory コマンド面の整理計画](../operations/memory-command-surface.ja.md) を、deprecation のルールは [CLI 安定性と非推奨ポリシー](../cli-stability.ja.md) を参照してください。

```
memory
├── search           # 日常 read（top-level）
├── show             # 日常 read（top-level）
├── list             # 日常 read（top-level）
├── inbox            # candidate review surface
│   ├── list
│   ├── accept
│   ├── reject
│   └── review       # 対話 TUI ウォークスルー
├── store            # deliberate write/store workflows
│   ├── remember
│   ├── propose
│   └── distill
└── admin            # extraction + host 連携 + maintenance + lifecycle
    ├── extract
    ├── import { codex | instructions }
    ├── export
    ├── activate
    ├── hygiene { scan | apply }
    ├── graph { add | list }
    ├── supersede
    ├── expire
    └── set-validity
```

### 日常 read コマンド

#### `traceary memory list`

durable memory を一覧表示します。scope flag を明示しない場合は、解決した workspace scope を既定で使います。

主な flag:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--source`
- `--include-hidden`
- `--limit`
- `--offset`
- `--as-of`
- `--include-expired`
- `--preset`
- `--json`

#### `traceary memory search [<query>]`

全文検索と構造フィルタで durable memory を検索します。query か filter のどちらか 1 つ以上が必要です。

主な flag:

- `--workspace`
- `--agent`
- `--session-family`
- `--status`
- `--type`
- `--source`
- `--include-hidden`
- `--limit`
- `--offset`
- `--as-of`
- `--include-expired`
- `--preset`
- `--json`

#### `traceary memory show <memory-id>`

1 件の durable memory を詳細表示します。evidence ref と artifact ref も含みます。

主な flag:

- `--json`

### `traceary memory inbox` — candidate review surface

メモリ候補の確認キューをレビューします。`list` はメモリ候補を confidence / review-readiness state と evidence / artifact ref 件数付きで一覧し、レビュアが provenance を確認してから accept できるようにします。`show` は単一候補の evidence-first decision card を表示します。`accept` / `reject` は positional id（対話的に打ち込む典型ケース）か、batch script / その他の caller 向けの `--ids id1,id2,...` のどちらでも受け付けます。partial batch でも id 単位の成功/失敗を返すので、どの id が transition したかが必ず分かります。`--id-only` を指定すると memory id だけが stdout に出力されます (`--json` と排他)。canonical な memory inbox surface は v0.13.x の positional-id 形式の strict superset です。

#### `traceary memory inbox list`

主な flag: `--workspace`, `--agent`, `--session-family`, `--type`, `--source` (manual / extracted / extracted-hidden / imported / remember-intent / compact-summary), `--include-hidden`, `--limit`, `--offset`, `--json`。

テキスト出力の末尾にプール件数行（`showing N of M candidates (source split)`）を出します。`--limit` のページの裏に 3 万件が隠れないようにするためです。`--source` 未指定なら `extracted-hidden` も含みます。`--json` は従来どおり item の配列です。

`--source` は extraction / import 経路との相性が良い filter です。

- `--source imported` は host-native source (Codex 等、`memory admin import codex` 参照) から取り込まれた memory に絞ります。
- `--source extracted` は `traceary memory admin extract` が session signal から起こした memory に絞ります。
- `--source extracted-hidden` は audit 用に保存された低品質自動抽出を表示します（既定では除外）。

#### `traceary memory inbox show <memory-id>`

単一のメモリ候補を evidence-first decision card として表示します。text view には candidate fact、source context、confidence / review-readiness state、evidence refs、artifact refs、利用可能な duplicate / supersede hint、accept-as-is checklist が含まれます。`memory inbox list` の `REVIEW` column が `needs-confirmation` または `blocked:no-evidence` の候補を accept する前に使ってください。

主な flag:

- 確認対象の positional `<memory-id>`
- `--json`

#### `traceary memory inbox accept <memory-id>`

メモリ候補を accept します。

主な flag:

- 単一 id 用の positional `<memory-id>`
- batch / その他の caller 向けの `--ids id1,id2,...`（複数指定可）
- `--confidence`
- `--id-only`（`--json` と排他）
- `--json`

#### `traceary memory inbox reject <memory-id>`

メモリ候補を reject します。

主な flag:

- 単一 id 用の positional `<memory-id>`
- batch / その他の caller 向けの `--ids id1,id2,...`（複数指定可）
- `--id-only`（`--json` と排他）
- `--json`

#### `traceary memory inbox attach <memory-id>`

既存のメモリ候補に evidence refs（任意で artifact refs）を追加します。review status は変更しません。accepted memory に evidence が必要なため、まだ accept / distill できない有用な候補向けの script-friendly path です。artifact のみの追加は、その候補がすでに evidence を持っている場合だけ受け付けます。

主な flag:

- 更新対象の positional `<memory-id>`
- `--evidence kind:value`（複数指定可。1つ以上必須）
- `--artifact kind:value`（複数指定可）
- `--id-only`（`--json` と排他）
- `--json`

#### `traceary memory inbox review`

共通 Bubble Tea TUI 基盤の上に乗った TTY 専用のメモリ候補確認ウォークスルーです。フィルタは `memory inbox list` と完全に同じなので、snapshot 表示と対話的レビューをフラグ調整なしで往復できます。

画面内のキー操作:

- `a` フォーカス中のメモリ候補を accept
- `x` フォーカス中のメモリ候補を reject
- `s` skip（状態は変えずカーソルだけ進める）
- `e` edit/distill — operator 自身に新しい fact を入力させ、`traceary memory store distill --replace=supersede` 経由で記録します。LLM が書いた candidate text を自動 accept することはありません
- `r` フォーカス中のメモリ候補に1件以上の evidence ref と任意の `artifact:kind:value` ref を追加。決定は保留した順に適用されるため、accept / distill の前に attach を保留してください
- `v` evidence / artifact ref を確認
- `?` ヘルプ overlay 切替
- `q` / Ctrl-C / Esc 安全に終了

非 TTY で起動した場合はエラー終了し、exit code は `2`。fallback guidance として `memory inbox list` と `memory inbox accept|reject` を案内するため、scripted shell では deterministic に分岐します。Accept / reject / evidence attach は batch 系コマンドと同じ memory usecase を呼ぶため、dedupe / status 遷移の意味づけは従来と変わりません。TUI 終了後に queued decision の一部が失敗した場合、サマリは従来通り stdout に各 `FAILED` 行を出力しつつ、コマンドは非ゼロ error を返すため、partial failure を shell が成功扱いしません。

主な flag: `--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`。

#### `traceary memory inbox cleanup`

古い / 低品質のメモリ候補を一括でプレビューまたは reject します。既定は dry-run で、`--apply` を付けると一致した候補を reject します。フィルタ: `--quality {low|normal|any}`（既定 `low`。`--quality any` はキュー全体の reject を避けるため `--older-than` が必須）、`--source`、`--type`、`--workspace`、`--agent`、`--session-family`、`--older-than` / `--newer-than`、`--include-hidden`、`--limit`。text と `--json` の出力には composition `summary`（`total` と `by_source` / `by_type` の内訳）が含まれ、`--apply` 前に batch の構成が分かります。cleanup は候補を reject するだけで、accept は evidence-first の rails を保つため上記の個別 review 系に委ねます。

### `traceary memory store` — deliberate writes

`memory store` 配下の verb はすべて durable memory row を書き込みます。row が `accepted` で着地するか `candidate` で着地するかは問いません。`memory store remember` は v0.36.0 で削除されました。`propose` を使ってください。

#### `traceary memory store propose`

candidate 状態の durable memory を記録します。あとで review できます。

主な flag:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--source`
- `--evidence`
- `--artifact`
- `--id-only`
- `--json`

#### `traceary memory store distill`

既存のメモリ候補 ID を 1 件以上指定し、operator が指定した fact で新しい accepted durable memory を作成します。source メモリ候補の evidence ref / artifact ref は accepted memory に union されます。Traceary が内容を書き換えたり、自動で accept したりすることはありません。

主な flag:

- `--from` — source メモリ候補 ID をカンマ区切りで指定 (複数指定可)
- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family` (いずれか必須)
- `--confidence`
- `--source`
- `--replace=keep|reject|supersede`
- `--id-only`
- `--json`

### `traceary memory admin` — extraction / host 連携 / maintenance / lifecycle

operator 向けの管理コマンドが集まる namespace です。extraction（既存 session からメモリ候補を起こす）、host 連携 I/O (`import` / `export` / `activate`)、maintenance (`hygiene` / `graph`)、accepted row を直接更新する lifecycle verb (`supersede` / `expire` / `set-validity`) をまとめてあります。

#### `traceary memory admin extract`

対象 session の session summary、compact summary、prompt event、note / review signal からメモリ候補を抽出します。抽出結果は candidate のみで、Traceary が自動で accept することはありません。prompt event は任意で、prompt や compact-summary event が無い場合も、利用できる signal の範囲で動作します。`--session-id` を省略した場合は、まず active session を解決し、見つからなければ workspace 内の latest session を使います。`Feedback:` / `Correction:` ラベルは、現在の最小 durable-memory taxonomy では `preference` candidate として保持されます。保存される candidate は、他の durable memory と同じ sanitization / redaction 経路を通ってから永続化されます。

主な flag:

- `--session-id`
- `--workspace`
- `--event-limit`
- `--candidate-limit`
- `--debug-signals`
- `--json`

#### `traceary memory admin import codex`

ローカルの Codex memory layout（既定値は `~/.codex/memories` 配下の `*.md`）からメモリ候補を取り込みます。legacy `MEMORY.md` は handbook allow-list (`## User preferences` / `## Reusable knowledge` / `## Failures and how to do differently`) を維持し、それ以外の Markdown shard は任意の見出し配下の bullet/list item を取り込みます。各 bullet が `source=imported` + `status=candidate` で記録され、evidence/artifact ref として元ファイル・行範囲が付与されます。scope は Codex の `applies_to: cwd=...` から解決し、ヒントが無い場合は `--workspace` flag の値を fallback に使います。取り込み時は既存の redaction rule を必ず通し、auto-accept は行いません。再実行は冪等で、同じ scope/fact の memory が既に存在する場合（rejected/superseded/expired を含むすべての状態）は duplicate として skip するため、一度 reject した memory が自動的に resurrect することはありません。

主な flag:

- `--root` — Codex memory root (既定値は `~/.codex/memories`)
- `--workspace` — source 側に `applies_to` ヒントがない場合の fallback scope
- `--watch` — 1回で終了せず定期的に再 import を続ける
- `--interval` — `--watch` 時の polling interval（最低 1s）
- `--json`

#### `traceary memory admin import instructions`

ホスト別 instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) を読み、Traceary が書いた管理ブロック外の bullet を `candidate` として取り込みます。管理ブロック内はすでに store に存在するため意図的に skip します。

主な flag:

- `--source` — ファイルを書いたホスト (`claude` / `codex` / `gemini`)
- `--in` — instruction file のパス
- `--workspace` — 取り込む candidate に割り当てる workspace scope (未指定時は env/検出 workspace)
- `--json` — JSON で出力

#### `traceary memory admin export`

accepted な durable memory をホスト別 instruction file (CLAUDE.md / AGENTS.md / GEMINI.md) に書き出します。出力は決定論的かつ冪等で、memory が変わらない限り同じバイト列を生成します。Traceary が書き出すブロックは `<!-- traceary-memories:begin:v1 -->` / `<!-- traceary-memories:end -->` で囲まれており、`memory admin import instructions` で同じファイルを読み込む際に重複メモリ候補を作らないようになっています。

主な flag:

- `--target` — `claude` / `codex` / `gemini` のいずれか
- `--workspace` — 書き出し対象の workspace scope (未指定時は env/検出 workspace)。workspace export は host-level ルールも反映されるよう、既定で `global` memory も含めます。
- `--include-global` — workspace scope と一緒に `global` memory を含める (default `true`)
- `--no-global` — opt out して明示した workspace scope のみを書き出す
- `--out` — 書き出し先パス。`-` (または未指定) で stdout へ
- `--json` — 書き出しサマリを JSON で出力

#### `traceary memory admin activate`

accepted memory を host の native context surface へ、安全な明示書き込みで activation します。`--target` の値に依らず flag セットは共通で、**Codex** / **Claude** / **Gemini** の 3 host を同じインターフェースで扱えます。host ごとに解決される target path や管理 region のレイアウトが異なります（[host-native memory activation ADR](../architecture/host-native-memory-activation.ja.md) と [durable memory ガイド](../memory/README.ja.md#ホスト別-activation-strategy)を参照）。

mode は排他で、`--status` / `--dry-run` / `--apply` のうち 1 つだけを指定します。`--diff` は `--dry-run` 時のみ有効です。

| Mode | 動作 |
| --- | --- |
| `--status` | read-only。`missing` / `stale` / `in_sync` / `invalid` を表示し、two-file target では component ごとの内訳も表示。refresh が必要なら `next_dry_run` / `next_apply` の remediation command を出力 |
| `--dry-run [--diff]` | read-only。書き込まれる予定の content を表示。`--diff` を付けると既存 target file との差分を表示。two-file target では `external memory plan` / `host context plan`（または対応する diff）でラベル付き出力 |
| `--apply` | 変更を適用。安全な writer で書き込み（lstat → symlink/directory を拒否 → 同 directory 内の temp file へ書いて atomic rename。書き込む file の parent directory のみを作成）。idempotent で再実行は noop に収束。新 marker version は拒否 |

既定 target:

- `codex` — Traceary 管理 file `~/.codex/memories/traceary.md`。single-file target で、ファイル全体が Traceary の所有
- `claude` — host context `<root>/CLAUDE.md` + external file `<root>/.traceary/memories/claude.md`。activation root は直近の `.git` 祖先、無ければ cwd
- `gemini` — host context `<root>/GEMINI.md` + external file `<root>/.traceary/memories/gemini.md`。root 解決は Claude と同じ。Gemini の `## Gemini Added Memories` セクションは byte-for-byte で保持され、managed import stub はその後ろに append される

主な flag:

- `--target` — `codex` / `claude` / `gemini`（必須）
- `--dry-run` — ファイルを作成・更新せず activation plan を表示
- `--apply` — activation target file（two-file target なら external memory file も）に書き込む
- `--status` — accepted memories と target file を書き込みなしで比較
- `--root` — activation root の上書き（Codex: memory root、Claude/Gemini: host context file を含む project root）
- `--path` — activation target file を明示上書き。Claude / Gemini では host context file を指し、external memory file は `<path の directory>/.traceary/memories/<target>.md` として導出
- `--workspace` / `--include-global` / `--no-global` — activation scope control
- `--diff` — target file が存在する場合に diff を含める (dry-run のみ)
- `--json` — activation plan / status / apply result を JSON で出力。two-file target では `host_context` / `external_memory` component の `path` / `state` / `action` / `existing`、plan 時は `markdown` / `diff` も出力

##### 例

status（read-only、Claude / Gemini は project 内で実行して、`.git` を含む最も近い ancestor へ activation root を解決させる）:

```sh
traceary memory admin activate --target codex --status
traceary memory admin activate --target claude --status --json
traceary memory admin activate --target gemini --status
```

既存ファイルとの diff 込み dry-run:

```sh
traceary memory admin activate --target codex --dry-run --diff
traceary memory admin activate --target claude --dry-run --diff
traceary memory admin activate --target gemini --dry-run --diff
```

apply（idempotent — 再実行は安全）:

```sh
traceary memory admin activate --target codex --apply
traceary memory admin activate --target claude --apply
traceary memory admin activate --target gemini --apply
```

activation root や host context file path を明示する例:

```sh
# cwd に依らず Claude activation を特定 project に固定
traceary memory admin activate --target claude --root /path/to/project --status

# Gemini の host context file を明示指定（external file は導出）
traceary memory admin activate --target gemini --path /path/to/GEMINI.md --apply
```

##### `invalid` からの復旧

`--status` が `invalid` を返した場合は、`--apply` を盲目的に再実行しないでください（apply path は同じ理由で拒否されます）。`--json` で component 単位の state を見て根本原因を直してから `--status` を再実行します。

| 原因 | 復旧手順 |
| --- | --- |
| target が symlink または directory | regular file に置き換える（または削除） |
| 管理マーカーが重複・孤立・不正 | 元のマーカーを復元、もしくは管理 region を削除 |
| 新しい marker version | ローカル Traceary を upgrade、もしくは管理ブロックを削除して再 apply |
| Traceary stub の外で expected `.traceary/memories/<host>.md` を指す unmanaged import line がある | unmanaged line を削除してから再 apply |
| host context file は `invalid` だが external file は正常（または逆） | JSON の `host_context.state` / `external_memory.state` で該当ファイルを特定してから編集 |

#### `traceary memory admin hygiene scan`

`accepted` な durable memory を走査し、store を変更せずに hygiene 候補を報告します。

- `redaction_hit` — 現在の redaction ルールで mask される内容が stored fact にまだ含まれているケース (例: `~/.config/traceary/config.json` に後から追加した extra pattern にヒット)。候補には `sanitized_fact` が付くため、続く `memory admin supersede` の置換テキストとしてそのまま使えます
- `expiry_candidate` — `--expiry-days` で指定した日数以上更新が無い memory。operator が expire を検討すべき候補
- `duplicate` — 同じ scope / fact を持つ accepted memory が 2 件以上ある場合のペア。どちらかを supersede / expire して整理する候補
- `supersede_candidate` — 同じ scope で fact の単語 Jaccard 類似度が `--similarity` (既定 0.6) 以上だが fact 自体は異なるペア。古い memory が supersede 対象、新しい memory の fact が提案される置換テキスト (`replacement_memory_id` / `replacement_fact` / `similarity`)
- `validity_overlap_supersede` — `(scope, type)` が一致し、明示 validity 窓 `[valid_from, valid_to)` がオーバーラップするペア。両方該当する場合はこちらが優先されます

主な flag:

- `--workspace` — scope filter (未指定時は env/検出 workspace。空なら全 scope)
- `--expiry-days` — staleness 閾値 (既定 90 日)
- `--similarity` — supersede_candidate 検出の word-Jaccard 閾値 (0.0-1.0、0 は既定値 0.6)
- `--max-scan-rows` / `--max-scan-bytes` / `--max-result-bytes` / `--max-comparisons` / `--max-duration` — 1 回の実行に対する有限の処理量・応答量上限
- `--json` — JSON 形式で suggestion のメタデータ付きに出力

すべての結果は `complete` / `partial` / `stop_reason` / `consistency` と実際の
`usage` を返します。CLI の partial result は `rerun_guidance` も返します。
`--workspace` を狭めるか、必要な有限上限だけを引き上げてください。store が変化しなければ `consistency=consistent` です。
実行中の memory write により global revision が変わっても、scan は保持済みの
phase / keyset を破棄しません。現在の revision に束縛し直し、
`consistency=best_effort` / `consistency_reason=revision_changed` へ恒久的に
downgrade して、同じ source page を残りの実行予算内で再試行します。再試行が
成功した場合、後で実際に停止させた上限を `stop_reason` で返します。page が前進
する前に revision 変更が繰り返されて duration を使い切った場合だけ、
`stop_reason=revision_changed` を返します。

hygiene cursor は、発行プロセスだけが保持する AES-GCM key で暗号化・認証されます。
改変済み cursor、旧 checksum cursor、以前のプロセスが発行した cursor は、新しい
scan が必要であることを明示する error になります。standalone CLI は `--cursor` を
受け付けず、`next_cursor` も出力しません。各 command は 1 回の上限付き scan です。
かつての MCP `query_memory(action="scan_hygiene")` による複数 page cursor 経路は
v0.35.0 (#1871) で MCP server と一緒に削除されました。

#### `traceary memory admin hygiene apply`

`--ids` に指定した memory id について、該当する suggestion の lifecycle transition を適用します。最初の mutation より前に、usecase は全 target と同一 scope の peer を 1 つの revision で完全に再検証します。再検証が partial、または revision が不一致なら request 全体を fail-closed にするため、scan の best-effort continuation が apply の安全性を弱めることはありません。適用される transition:

- `redaction_hit` → `supersede`（sanitized fact に差し替え、scope / type / refs は継承）
- `expiry_candidate` → `expire`（現在時刻で失効）
- `duplicate` → `reject`（残したい方と対になる id を指定）
- `supersede_candidate` / `validity_overlap_supersede` → `supersede`（新しい memory の fact に差し替え、scope / type / refs は元 memory から継承）

主な flag:

- `--ids` — 適用対象の memory id をカンマ区切りで指定 (複数指定可)
- `--expiry-days` — 内部 scan の staleness 閾値 (既定 90 日)
- `--json` — JSON 形式で id 別 transition メタデータを出力

#### `traceary memory admin supersede <memory-id>`

accepted durable memory を新しい accepted memory で置き換えます。`--type` と scope flag を省略すると現在の memory を継承します。

主な flag:

- `--type`
- `--fact`
- `--workspace` / `--agent` / `--session-family`
- `--confidence`
- `--source`
- `--evidence`
- `--artifact`
- `--from` / `--to` — 置き換え後の content validity 窓
- `--id-only`
- `--json`

#### `traceary memory admin expire <memory-id>`

active な durable memory を expire します。

主な flag:

- `--at`
- `--id-only`
- `--json`

#### `traceary memory admin set-validity <memory-id>`

durable memory の content validity 窓 (`valid_from` / `valid_to`) を設定または更新します。validity 窓は fact が真として主張される期間で、`memory admin expire` が記録する lifecycle `expires_at` とは別の軸です。

主な flag:

- `--from` — 開始 (`YYYY-MM-DD` または RFC3339)
- `--to` — 終了
- `--clear-to` — 既存の `valid_to` を外して open-ended に戻す（`--to` と排他）
- `--id-only`
- `--json`

### 削除済み flat alias (v0.15)

旧 release の flat memory verb は v0.14.x の間 hidden deprecated alias として残していましたが、v0.15.0 で削除されました。以下は歴史的な移行メモです。新しいスクリプトやドキュメントでは使用しないでください。

| 削除済み alias (v0.15) | Canonical 置き換え |
| --- | --- |
| `memory accept <id>` | `memory inbox accept <id>` |
| `memory reject <id>` | `memory inbox reject <id>` |
| `memory remember` | `memory store propose`（`memory store remember` は v0.36.0 で削除） |
| `memory propose` | `memory store propose` |
| `memory distill` | `memory store distill` |
| `memory extract` | `memory admin extract` |
| `memory import codex` | `memory admin import codex` |
| `memory import instructions` | `memory admin import instructions` |
| `memory export` | `memory admin export` |
| `memory activate` | `memory admin activate` |
| `memory hygiene scan` | `memory admin hygiene scan` |
| `memory hygiene apply` | `memory admin hygiene apply` |
| `memory graph add` | `memory admin graph add`（v0.35.0 で削除。置き換え先なし） |
| `memory graph list` | `memory admin graph list`（v0.35.0 で削除。置き換え先なし） |
| `memory supersede` | `memory admin supersede` |
| `memory expire` | `memory admin expire` |
| `memory set-validity` | `memory admin set-validity` |

## Session コマンド

### `traceary session start`

session start 境界を記録し、session ID を出力します。

既定値:

- `--client` / `--agent` / `--workspace`: flag → `TRACEARY_CLIENT` / `TRACEARY_AGENT` / `TRACEARY_WORKSPACE` → `cli` / `manual` / 検出した workspace
- `--session-id`: 省略時は新しい ID を採番

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--parent-session-id`
- `--id-only`
- `--json`

### `traceary session end`

session end 境界を記録し、生成された event ID を出力します。

既定値:

- `--session-id`: flag → `TRACEARY_SESSION_ID`
- `--client` / `--agent` / `--workspace` の不足分は、対応する `session start` から補完できる場合は補完

主な flag:

- `--client`
- `--agent`
- `--session-id`
- `--workspace`
- `--summary`
- `--id-only`
- `--json`

### `traceary session run`

`traceary session run` は、単発セッションを開始し、1つの子プロセスを監視して、
その usage を取得し、プロセスの終了時にセッションを確定します。terminal
transition を所有するのは wrapper だけであり、型付きの terminal reason を
書き込みます。wrapper 配下で実行されるネストしたホストの `SessionEnd` hook は
何も行わないため、wrapper より先にセッションを確定したり、wrapper の reason を
置き換えたりすることはできません。

| Terminal reason | プロセスの結果 | Wrapper の exit code |
| --- | --- | ---: |
| `success` | 子プロセスが正常終了 | `0` |
| `failure` | 子プロセスが異常終了 | 子プロセスの exit code |
| `failure` | 子プロセスを起動できない | `127` |
| `timeout` | deadline が経過 | `124` |
| `signal` | Unix で子プロセスが signal `N` により終了 | `128 + N` |
| `aborted_stream` | 実行がキャンセルされたか、stream が中断された | `74` |
| `legacy_unknown` | 型付き reason のない従来のセッション | 新しい単発実行では割り当てられない |

分類では、子プロセスの終了と supervisor のキャンセルが競合する場合を考慮します。
子プロセスが正常終了した場合は、deadline またはキャンセルが同時に ready に
なっても `success` です。Unix では、子プロセスが自発的に終了したことを確認
できる場合、非ゼロの exit code を維持し、`failure` に分類します。それ以外では、
deadline の経過がキャンセルより優先され、続いて signal による終了、その他の
子プロセス終了エラーの順に扱われます。

非 Unix のフォールバックでは、supervisor による終了として報告された exit code 1
と、子プロセス自身が code 1 で終了した場合を区別できません。その結果が context
と競合する場合は、保守的に context の結果を使用し、実行を `timeout` または
`aborted_stream` に分類します。

セッションの確定処理には5秒の制限があります。それ以外は正常に終了した
子プロセスについて確定処理が失敗した場合、wrapper は exit code を `0` から
`1` に引き上げます。既存の非ゼロの子プロセスまたは supervisor の exit code は
維持されます。usage の取得に失敗した場合は code `1` で終了します。

上記の terminal-reason taxonomy は、コマンド監査の `--failure-reason` enum とは
異なります。

古い単発セッションの調査と修復には、
[`traceary session repair-one-shot`](../operations/one-shot-repair.ja.md) を使用します。
このコマンドはデフォルトでは dry-run です。修復を適用するには、バックアップと
検証済みの evidence manifest が必要です。

### `traceary session refine <session-id>`

エージェントが書いたセッション要約（L2 refinement）を保存します。

Traceary は要約テキストを合成しません。渡された内容を保存し、generation / coverage の管理だけを所有します。同じ `--covers-to` 範囲の再実行は no-op です（行は 1 つのまま、generation もテキストも変わりません）。被覆が進んだときだけ既存行を `generation + 1` で置き換え、`covers-from` は earlier 側を保持します。

`covers-from` は常に導出されます（初回はセッション最古イベント、supersede 時は既存の earlier を保持）。degraded 要約は `store compact --force` が use case 経由で書くため、この CLI では指定しません。

必須 flag:

- `--summary`
- `--covers-to`

主な flag:

- `--keywords` — カンマ区切りのキーワード（自由形式）
- `--produced-by` — 要約の作成者（既定: `cli`）
- `--json` — 機械可読な outcome（`created` / `superseded` / `unchanged`）と generation / coverage
- `--db-path`

### Session status の値

内部 session 行（hooks、handoff、context）は次の status 値を使います。

| Status | 意味 |
|--------|------|
| `active` | end marker がなく stale window 内。 |
| `stale` | end marker がないが stale window（default 24h）より前に開始。 |
| `ended` | end marker があり、その後にイベントがない。 |
| `ended_with_late_events` | end marker があるが、同じ session で後続イベントが到着した。end marker は `session_ended` イベント由来、または `session gc` が `ended_at` を直接書き込んだものの場合がある。 |

これらの値を出していた公開 `sessions --snapshot` は v0.42.0 で削除されました（#2061）。`ended_with_late_events` は、host が session を早期に close したあとでも後続 workspace イベントがあるとき、hook / handoff 解決が session を見失わないための値です（例: Codex）。

## Hooks と診断

### `traceary completion <bash|zsh|fish|powershell>`

interactive 利用向けの shell completion script を生成します。

### `traceary hooks print`

対応クライアント向けの生成済み hook 設定を出力します。

対応 client: `claude`, `codex`, `gemini`
alias: `claude-code`, `codex-cli`, `gemini-cli`

主な flag:

- `--client`
- `--traceary-bin`

### `traceary hooks install`

対応クライアントの標準設定パスに生成済み hook 設定を書き出します。

主な flag:

- `--client`
- `--project-dir`
- `--traceary-bin`
- `--output`
- `--global` (user-level 設定へ書き込む。`--output` とは排他)
- `--force`

### `traceary hooks guide`

対応クライアントごとの install / check / verify 手順を出力します。

主な flag:

- `--client`
- `--project-dir`
- `--output`

### `traceary doctor`

DB アクセス、生成済み hook 設定の有無、plugin version の整合性、クライアント設定のつながりを診断します。

text 出力は `Environment`、`Database`、`Plugins`、`Hooks` の安定した section に分かれます。
各 check は `PASS` / `WARN` / `FAIL` の severity を持ちます。`WARN` は hooks 未導入などの初回状態や未設定状態、`PATH` 上に複数の `traceary` がある状態、plugin version が実行中の `traceary` と一致しない状態を表します。`FAIL` は DB アクセス不良、unreadable / invalid config、`PATH` 上に `traceary` がない状態のような壊れた状態を表します。

追加の doctor check:

- `path`: `PATH` 上の `traceary` 解決先と directory を確認します。見つからない場合は `FAIL`、複数見つかる場合は `WARN` です。
- `<client>-plugin-version`: 検出した plugin manifest / cache の version と実行中 binary version を比較し、不一致なら plugin の reinstall / update を促します。
- `claude-hook-cancellations`: 対応が必要な SessionEnd cancellation marker と、参照先 session が後から終了した marker を分けて表示します。`doctor --fix --dry-run` は解決済み marker の削除を preview し、`doctor --fix` は終了済みと確認できた marker だけを削除します。active、session 不明、unreadable な証跡は削除しません。
- `codex-memory-activation` / `claude-memory-activation` / `gemini-memory-activation`: accepted durable memory が host の native activation target で `missing` / `stale` / `in_sync` / `invalid` のどれかを確認します。`missing` / `stale` は `WARN` で、正確な `memory admin activate --dry-run --diff`（preview）と `memory admin activate --apply`（refresh）の remediation command を表示します。`invalid` は `FAIL` で、host file を確認してから apply するよう hint を出します。`--client <claude|codex|gemini>` で対象を絞り、`--project-dir <dir>` で Claude/Gemini の activation root を doctor process の cwd ではなく特定 repository に固定できます。

終了コード:

- `0`: すべての check が `PASS`
- `1`: 1 件以上の check が `FAIL`
- `2`: `FAIL` はないが、1 件以上の check が `WARN`

既定では warning-only でも非ゼロ終了にして、対話利用や厳密な automation で drift を見逃さないようにしています。
壊れた状態だけを失敗扱いにしたい CI / smoke check では `traceary doctor --json --warnings-ok` を使ってください。
JSON report には warning 件数と各 check の `WARN` status が残り、`FAIL` がある場合は引き続き exit code `1` で終了します。

`--json` は legacy top-level `checks` を維持しつつ、sectioned structure を追加します。

```json
{
  "sections": [
    {
      "name": "Environment",
      "checks": [
        {"name": "config", "severity": "PASS", "section": "Environment", "message": "...", "hint": "", "fix_command": ""}
      ]
    }
  ],
  "summary": {"pass": 3, "warn": 1, "fail": 0},
  "exit_code": 2
}
```

alias:

- `traceary status`

主な flag:

- `--client`
- `--project-dir`
- `--json`
- `--fix` — 利用可能な安全な修復を適用する
- `--dry-run` — 書き込まずに `--fix` を preview する
- `--warnings-ok` — warning-only report は exit code `0` にし、failure は exit code `1` のままにする
- `--strict` — audit-reliability: 時間に関係なく完全一致する duplicate group をすべて報告する（near-simultaneous な書き込みだけに限定しない）

## Store 管理 (`traceary store ...`)

store 管理コマンドは `store` namespace に集約されています。旧 top-level の `traceary init` / `traceary backup` / `traceary gc` alias は v0.14.0 で削除されました。実行すると Cobra の unknown-command エラーになります（`traceary store init` / `traceary store backup ...` / `traceary store compact` を使ってください）。これらの alias は v0.9.0 から v0.13.x まで deprecation 通知付きで動作していました。詳細は [CLI 安定性と非推奨ポリシー](../cli-stability.ja.md) を参照してください。

### `traceary store init`

DB 作成と migration 適用を明示的に先行実行します。通常コマンドでも必要に応じて初期化されるため、必須ではありません。

### `traceary store backup create`

コンパクトな SQLite バックアップファイルを作成します。

主な flag:

- `--output`
- `--db-path`
- `--force`

### `traceary store backup restore`

バックアップファイルから DB を復元します。

主な flag:

- `--input`
- `--db-path`
- `--force`
- `--yes`

### `traceary store compact`

ストアファイルを書き換えます。実行した瞬間が同意です。Traceary はストアをコピーし、そのコピーを filter し、新しいファイルへ VACUUM INTO したあと atomic exchange します。旧 inode は rollback ファイルとして残ります。

コピー中に、非 canonical な hook 重複本文、`--keep-days`（既定 90）を過ぎた covered 本文、退役済み search index family を落とします。残った本文は encode します。`traceary store search-projection` は別コマンドのままです。

破棄対象の session がすべて未 refine なら compact は拒否し、`traceary-session-refine` を案内します。部分 fold は進み、その session が許可した分だけ回収します。`--force` は先に機械要約を書きます。エージェントの判断理由（なぜ）は復元しません。

preview ではなく、in-place `VACUUM` でもありません。成功後は `traceary store compact rollback RUN_ID` で直前のファイルに戻せます。

主な flag:

- `--force`
- `--keep-days`
- `--db-path`
- `--json`

### `traceary store compact rollback RUN_ID`

成功した書き換えが残した rollback inode から、compact 前のストアを戻します。

### `traceary store archive create|restore|verify`

オフライン archive segment の作成・復元・検証です。公開されている store 管理コマンドであり、`store compact` の代替ではありません。

### `traceary store capacity`

メタデータのみの容量レポートです。大きいストアでは検査が bounded で、`evidence=cached` / `evidence=bounded` になることがあります。

### `traceary store retention files plan|apply`

ホスト側 artifact の file-retention を計画または適用します。`apply` は operator 同意が必要で、既定 hook 経路には入りません。

### `traceary store search-projection start|resume|status|abort|probe`

search-projection rebuild を管理します。`status` は読み取り専用、`start` / `resume` / `abort` が状態を変えます。大きいストアの catch-up は page され、進まないときは park します。

### `traceary session gc`

stale な未終了 session を閉じます。`session` 名前空間配下の admin 向け入口です。

### `traceary session repair-one-shot`

レビュー済みの one-shot session-repair 証拠ファイル（`--evidence-file`）を適用します。`--apply` で書き込み、無いときは dry preview です。

### `traceary bundle export|import`

portable な session/memory bundle を export / import します。契約は `docs/cli-stability.md` を参照してください。

### `traceary memory decay`

古い durable memory を expire / supersede します。まず preview し、scan を確認してから apply フラグを使います。

## Integration コマンド

> `integration` コマンド subtree 全体（`integration` 親と `codex` group）は v0.20.0 時点で `traceary --help` から非表示になり、v0.21.0 で完全削除予定です。以下は移行メモとしてのみ掲載しています。非表示の stub は引き続き非ゼロで終了し、Codex 公式の `/plugins` flow を案内します。

### `traceary integration codex install` (廃止・非表示)

v0.14.0 で廃止されており、**サポート対象の install 面ではありません**。コマンドは非表示扱いとなり、実行しても install は行われず、Codex 公式の `/plugins` flow を案内するヒントのみを返します。新規 install は必ず Codex 公式の `/plugins` flow（リポジトリ内で `codex` を起動 → `/plugins` → `Traceary Plugins` → `Traceary`）を経由してください。詳細は [Codex plugin ガイド](../integrations/codex-plugin.ja.md) を参照してください。

### `traceary integration codex uninstall` (v0.15 で削除)

v0.15.0 で削除されており、**サポート対象の uninstall 面ではありません**。この名前は歴史的な移行メモとしてのみ掲載しています。今後の uninstall は Codex 公式の `/plugins` flow を使い、v0.14 以前の旧 install 経路が残した state だけ [Codex plugin ガイドの手動 cleanup 手順](../integrations/codex-plugin.ja.md) で片付けてください。


### `traceary report`

期間指定の振り返りダイジェストを表示します。コマンド件数は正規化済み executable 単位で集計するため、`git ...` と確認済み wrapper 経由の `rtk git ...` は同じ `git` 行になります。失敗判定には `exit_code` と `failure_reason` だけを使い、command の input/output 本文は解析しません。JSON は `failures.by_reason` を含み、構造化された根拠のない履歴行は `unknown` のままです。

日付だけの `--to` は明示した `--timezone`（既定は UTC）の指定日を含み、RFC3339 の `--to` は正確な排他時刻のままです。ホスト依存の特殊なゾーン名 `Local` は拒否します。テキストには要求した暦日の終了をそのまま表示します。JSON は `requested_from` / `requested_to` と `effective_from_inclusive` / `effective_to_exclusive` を分け、`timezone` と共通の `snapshot_at` も返します。両方の境界を省略した場合、要求値は空のままにし、実効値には `snapshot_at` で終わる既定の 7 日間を格納します。互換用の `period.from` / `period.to` は引き続き実効境界を v0.30.0 より前と同じ秒精度で返します。

既定では全件を集計します。`--page-size` は 1 以上 100,000 以下で、本文を含まない SQLite 内部読み取りのページサイズだけを制御し、集計件数の上限にはなりません。正の `--result-cap` を指定した場合だけ、データ源ごとの部分集計を明示的に要求します。その場合、JSON は `aggregation.coverage=partial`、session/event/command/usage ごとの観測件数と時刻範囲、`truncation_reason=result_cap` を返し、不完全な分母から割合を計算せず該当フィールドを省略します。非推奨の `--limit` は `--page-size` の別名としてだけ動作し、併用できません。

`usage` オブジェクトは、現在有効な確定済み観測を provider、engine、model、repository、ticket、pull request、batch ごとに集計します。token フィールドは既知の観測件数と取得不能な観測件数を分けるため、既知の 0 と証拠不足を混同しません。`accounted_observations` は加算対象外の代替証拠を除き、`excluded_observations` はその証拠の存在を可視化します。`unavailable_observations` はデータ源をまったく読めなかった観測を数えるため、取得不能を収集成功の 0 と見なしません。cost 行は `origin` ごとに分離し、`estimated` を `provider_reported` として表示しません。run の packet bytes と tool output bytes は run identity で重複排除し、`usage.runs` に出力します。role、round、wall time は正式な値が永続化されていないため、現在は `unavailable` と表示します。

主な flag: `--from`、`--to`、`--timezone`、`--workspace`、`--client`、`--page-size`、`--result-cap`、`--json`。

#### レポートの系譜

レポートは、4つのソースファミリーを読み込みます。sessions セクションは
`sessions` をソースとし、セッションごとのイベント数は `events` から取得します。
events セクションは直近のイベントメタデータをソースとし、クライアント別に
プロンプト、トランスクリプト、コマンドのカバレッジを要約します。commands
セクションはコマンド監査レコードをソースとし、クライアント別に失敗率を
要約します。比率と失敗率は、ソースのカバレッジが完全な場合にのみ報告されます。

usage セクションは、確定済みの `usage_observations` をソースとし、実行の識別情報を
取得するために `usage_observation_runs` と、リポジトリ、チケット、プルリクエスト、
バッチの帰属情報を取得するために `run_lineages` と結合します。置き換えられた
observation は除外され、各 observation の最新かつ置き換えられていない
スナップショットだけが集計されます。

パケットとツール出力のバイト数は、`run_lineages` に記録された不変の実行情報です。
これらは実行の識別情報によって重複排除され、`usage.runs` 配下に報告されます。
そのため、同一実行に対して複数の usage observation が存在しても、バイト数が
重複して加算されることはありません。

sessions、events、commands、usage は、1つの読み取り専用トランザクション内で
読み込まれます。各ファミリーには、観測件数と時間範囲を含む独立した
カバレッジ範囲があります。カバレッジは `complete` または `partial` です。
結果件数の上限によってファミリーが切り詰められた場合、その
`truncation_reason` は `result_cap` になります。

各 usage 集計には、記録された usage の terminal classification から、それに
該当する observation 数へのマップである `terminal_classifications` が含まれます。
テキストレポートでは、このマップを `terminal=...` として表示します。
`unavailable_observations` は、usage カウンターがすべて利用できない observation の
件数です。これには、カウンターの合計から除外された observation も含まれます。

### `traceary report workspace-identity`

本文を含めずに、ワークスペース帰属の網羅率、関係と競合率、安定 ID による完全再送率を表示します。observation 行数は volume のまま残し、`conflict_pair_count` が actionable な単位（現行 conflict projection の distinct `(session_id, workspace)`）です。sample は pair あたり最新 1 行で `workspace` を含むので、report から `store workspace-alias add` を実行できます。履歴上の本文・時間窓候補は既定で測定しません。`--include-heuristic` を使うと上限付き dry-run を明示的に要求でき、必要に応じて `--heuristic-limit`（既定 5,000 件）または `--strict` を指定できます。JSON のヒューリスティック `measurement_state` は `not_requested`、`partial`、`complete`、`failed` のいずれかで、exact delivery とは独立しています。自動化と QA では `--json` と `--conflict-sample-limit` を利用できます。
store の初期化または migration が必要な場合は、先に `traceary doctor` を実行してください。report 自体は読み取り専用です。意味は [workspace-conflict の意味](../research/workspace-conflict-meaning.ja.md) を見てください。

### `traceary store workspace-alias add|list|remove`

現在の診断 projection で使う、運用者確認済みのセッション/ワークスペース別名を管理します。review 済み alias を追加・撤回・一覧する唯一の public 経路であり、非推奨化しません。add には `--session`、`--workspace`、`--reviewed-by` が必要で、正規 provenance は書き換えません。

## 関連ドキュメント

- 導入ガイド / クイックスタート: [`../../README.ja.md`](../../README.ja.md)
- 環境変数と runtime 前提: [`../environment/README.ja.md`](../environment/README.ja.md)
- Hooks ガイド: [`../hooks/README.ja.md`](../hooks/README.ja.md)
- バックアップガイド: [`../backup/README.ja.md`](../backup/README.ja.md)
