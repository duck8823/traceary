# CLI 安定性と非推奨ポリシー

[English](./cli-stability.md)

このドキュメントは、Traceary の CLI サーフェスについて運用者・統合者向けに公開する契約です。
どのコマンドが「公開サーフェス（public）」で、どれが「admin / メンテナンス」、どれが「plumbing / hidden / deprecated」かを定義します。
v0.15 系と続く v1.0 でコミットする「非推奨通知の出し方」「1 マイナー分の互換ウィンドウ」「v0 と v1 の削除ポリシー」もここで定めます。

コマンド単位のフラグや挙動の詳細は [CLI リファレンス](./cli/README.ja.md) にまとめています。
本ページは意図的にポリシー層に絞ってあるので、外部ツールや AI integration、SKILL パックがリファレンスを毎回読まずに安定性を判断できるリンク先として使えます。

## 目的

- v1.0 を見据え、v0.15 のコマンドサーフェスを「どこが公開で、どこがメンテで、どこが deprecation シムか」を明示する。
- 日常用途のコマンド（公開サーフェス）はマイナーリリースを跨いでバイト一致で安定させる。
- admin / メンテナンス系コマンドは公開サーフェスを壊さない範囲でマイナー境界で進化させる。
- スクリプト・AI SKILL・古いドキュメントの例文に対し、削除前に必ず 1 マイナー以上の移行猶予を与える。

## 安定性ティア

Traceary の各サブコマンドは必ず以下のいずれか 1 ティアに属します。
ティアにより「変更しても良い範囲」「変更してよい時期」「外部 caller への通知方法」が決まります。

| ティア | 表示 | 安定範囲 | リリースごとに許される変更 |
|---|---|---|---|
| **公開（public）** | `traceary --help` と `docs/cli/README.md` に掲載。 | コマンドパス、フラグ名、終了コード、stdout のテキスト形状、`--json` / `--id-only` / NDJSON のバイト形状、エラーメッセージ構造。 | マイナー間は **追加のみ**（新規フラグ、新規 JSON optional フィールド、新規サブコマンド）。破壊的変更は後述の deprecation フローを通り、最低 1 マイナーの重複期間を取る。 |
| **admin / メンテナンス** | `--help` の名前空間（`store`、`memory admin` など）と `docs/cli/README.md` に掲載。 | 文書化されたコマンドパス・フラグ集合、`--json` / `--dry-run` / `--apply` のセマンティクス（該当する場合）。 | マイナー間は追加可。破壊的変更は公開と同じ deprecation フローだが、運用者だけが触る範囲では「N で stderr 通知 → N+1 で削除」と速く進めてよい。 |
| **plumbing / hidden / deprecated** | `--help` から非表示 (`Hidden: true`)。CLI リファレンスでは「deprecated alias」または「cleanup-only」と明記。 | 置き換え先 canonical の引数・フラグ形状、stderr 非推奨通知のフォーマット。 | 通知中に名指しした次マイナーで削除可。新しい plumbing コマンドは進行中の移行をブリッジするためでない限り追加しない。 |

### 公開コマンド（現在）

公開サーフェスは運用者の日常用途サーフェスです。コマンドパス・フラグ名・stdout テキスト形状・`--json` / `--id-only` / NDJSON のバイト形状をマイナーリリースを跨いで安定に保ちます。

v0.15 以降に追加された互換 alias も含む現在の公開コマンド（用途別）。可視の public / admin leaf の出荷分類は `presentation/cli/pillar_inventory.go`（#1692）です。

- **イベント記録** — `traceary log`、`traceary audit`
- **読み取り / 観察** — `traceary list`（`--follow` と `--blocks` を含む）、`traceary search`、`traceary show`、`traceary context`（`--handoff` と `--compact-only` を含む）
- **セッション** — `traceary session start`、`traceary session end`、`traceary session run`、`traceary session refine`
- **durable memory 日常 read** — `traceary memory search`（`--all` を含む）、`traceary memory show`
- **durable memory inbox** — `traceary memory inbox list`、`traceary memory inbox show`、`traceary memory inbox accept`、`traceary memory inbox reject`、`traceary memory inbox attach`、`traceary memory inbox cleanup`、`traceary memory inbox restore`、`traceary memory inbox review`（TTY のみ）
- **durable memory store** — `traceary memory store propose`、`traceary memory store distill`
- **durable memory decay** — `traceary memory decay`
- **hooks** — `traceary hooks install`（`--dry-run` を含む）、`traceary hooks guide`、`traceary completion`（`bash` / `zsh` / `fish` / `powershell`）
- **診断** — `traceary doctor`（alias `traceary status`、additive な `store-capacity` check を含む）、`traceary report`
- **bundle import / export** — `traceary bundle export`、`traceary bundle import`

`traceary doctor` の JSON envelope（`sections` / `summary` / `exit_code` / 各 check のフィールド）、`traceary list --blocks --json`（`workspace_breakdown`。旧 `traceary timeline --json`）、`traceary context --handoff` の構造化テキストのフィールドラベル（旧 `traceary session handoff`）はいずれも公開契約の一部です。これらは `presentation/cli/testdata/` で golden test により固定されています。詳細は [JSON / snapshot contract test](./operations/json-contract-tests.ja.md) を参照してください。

`traceary store compact --projection-rebuild` の stdout は 2 つの JSON 形です。分岐は additive な `result_kind`（start/置き換えは `generation`、hash 一致 resume は `run`）で行い、フィールド推測はしません。`--projection-abort` は別の abandon オブジェクトで、`result_kind` はありません。

`traceary doctor` は既定で、全 check が pass なら exit code `0`、1 件でも fail があれば `1`、warning-only report なら `2` で終了します。warning を operator-visible な drift として見たいが、壊れた install だけを失敗扱いにしたい automation では `--warnings-ok` を指定してください。この場合 warning-only report は `0`、failure は引き続き `1` で終了し、JSON の `summary` と各 check の severity は変わりません。

> TTY 必須の公開コマンド（現状は `traceary memory inbox review`）は TTY 要件を明示し、stdin/stdout が TTY でないときは非ゼロ終了コードでスクリプト用フォールバックを案内します。新しい TTY-only 公開コマンドを追加するときも、必ず batch / scripted フォールバック経路を文書化してください。

### admin / メンテナンスコマンド (v0.15)

admin コマンドは運用者向けのメンテサーフェスです。`--help` には引き続き掲載され CLI リファレンスでも文書化されますが、日常 read 経路ではありません。対象が運用者だけのときは公開コマンドより速いペースで進化してよいですが、後述の非推奨通知ルールには従います。

v0.35 時点の admin コマンド：

- **ストア管理** — `traceary store backup create`、`traceary store backup restore`、`traceary store compact`（`--archive`、`--archive-verify`、`--archive-restore`、`--retention-plan`、`--retention-apply`、`--projection-rebuild`、`--projection-abort` を含む）、`traceary store compact rollback`
- **durable memory admin** — `traceary memory admin extract`、`traceary memory admin import codex`、`traceary memory admin import instructions`、`traceary memory admin export`、`traceary memory admin activate`、`traceary memory admin hygiene scan`、`traceary memory admin hygiene apply`、`traceary memory admin supersede`、`traceary memory admin expire`、`traceary memory admin set-validity`
- **レポート管理** — `traceary report workspace-identity`

### plumbing / hidden / deprecated コマンド (v0.15)

これらは `traceary --help` から非表示です。v0.15 の hidden surface は 2 種類あります。

- **削除済み名（stub なし）** — 旧 top-level alias（v0.14.0）、flat memory alias（v0.15.0）、および `traceary integration` サブツリー全体（v0.25.0 で完全削除、#1266）は migration stub を登録しません。Cobra の unknown-command / unknown-subcommand エラーで非ゼロ終了します。詳細は下の Historical removal log を参照してください。
- **hook runtime 入口** — 同梱の Traceary hook スクリプトから呼び出される内部コマンドです。

同梱の Traceary hook スクリプトから呼び出される hidden ランタイム入口（`Hidden: true` で登録、stderr 非推奨通知は出さない）：

- `traceary hook session`、`traceary hook audit`、`traceary hook compact`、`traceary hook subagent-start`、`traceary hook subagent-stop`、`traceary hook prompt`、`traceary hook transcript` — `traceary hooks install` が出力する hook スクリプトから呼び出される。
- `traceary hooks helper json-get`、`traceary hooks helper build-failure-output`、`traceary hooks helper normalize-git-remote` — 同じ同梱 hook スクリプトが使う内部ヘルパー。

これら runtime 入口の安定性 / 非推奨ポリシー：

- これらは Traceary バイナリと、そのバイナリが生成する hook 設定との間の内部契約として扱う。運用者や外部スクリプトが直接呼び出す対象ではなく、canonical な運用者入口は `traceary hooks install`（プレビューは `--dry-run`）で、再インストールするとインストール済みバージョンに合った hook 設定が再生成される。
- コマンドパスと引数形状は patch リリース (`v0.N.x`) では安定。
- マイナー境界 (`v0.N.0` → `v0.(N+1).0`)、および v1.0 以降の `v1.x` マイナー間でも、新マイナーの `traceary hooks install` が互換 script を再生成し、CHANGELOG で「hook を再インストールする必要がある」旨を案内することを前提に、改名・削除・引数形状変更を公開の stderr 非推奨フローを通さずに行ってよい。
- 新しい hidden ランタイム入口の追加も同じルール。マイナー境界で追加してよく、同じバージョンの `traceary hooks install` 更新と組で出荷する。

現在非推奨：

- なし。

### 柱ごとの棚卸し（v0.35）

#1692 は可視の public / admin leaf を 2 本の柱（記録 = 捕捉 / 要約 / 圧縮 / 破棄、記憶 = 統合して自動供給）に突き合わせました。hidden な hook 入口は plumbing のままです（後述）。出荷テーブルは `presentation/cli/pillar_inventory.go` で、可視 action を行なしで追加するとテストが失敗します。

削除根拠は空の backing、重複、柱なしだけです。usage 件数は理由にしません。#1870 の 97→29 keep-list に残っているグループは v0.34 の非推奨 registry に無いので、v0.35 では削除しません。`list --follow` は v0.42.0（#2068）で入りました。`list --blocks` は v0.42.0（#2069）で入りました。`hooks install --dry-run` は v0.42.0（#2070）で入りました。`memory search --all` は v0.42.0（#2071）で入りました。`store capacity` は v0.42.0（#2072）で `doctor` に吸収されました。`context --handoff` / `--compact-only` は v0.42.0（#2073）で入りました。`store compact --archive` / `--retention-plan` は v0.42.0（#2074）で入りました。`doctor --alias-add` / `--alias-remove` / `--alias-list` は v0.42.0（#2075）で入りました。`store init` は v0.42.0（#2076）で自動初期化と `doctor --fix` に畳みました。`store search-projection` は v0.42.0（#2077）で `doctor --fix` / `store compact --projection-rebuild` / `--projection-abort` に畳みました。`replay` は v0.42.0（#2078）で削除し、以前の再保持を上書きしました。読み取りは `report` / `context` / `list`、機械可搬 export は `bundle export` です。

過去の削除履歴：

- v0.43.0 で削除（#2122）: `traceary session gc` と `traceary session repair-one-shot`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。admin ティアなので operator 向け案内で足ります。未終了の stale session は hook の opportunistic GC と `traceary doctor --fix`（既定 24h。独自 `--stale-after` は廃止）が終了します。過去の one-shot 行はそのまま残り、evidence-manifest 修復の吸収先はありません。
- v0.42.0 で削除（#2077）: `traceary store search-projection`（start/resume/status/abort/probe）。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。parked 復旧は `traceary doctor --fix`。新しい世代または進行中 rebuild の resume は `traceary store compact --projection-rebuild`（同じ budget flag）。abort は `traceary store compact --projection-abort`。lifecycle / 予算の確認は `traceary doctor` のままです。
- v0.42.0 で削除（#2078）: `traceary replay`。呼び出しは unknown command として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。単一ファイル HTML export として残すとした以前の記述を上書きします。期間の読み取りは `traceary report` / `traceary context` / `traceary list`、機械可搬コピーは `traceary bundle export` です。

- v0.42.0 で削除（#2076）: `traceary store init`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。空ストアは first write または `traceary doctor` で自動初期化します。データ依存 offline migration は `traceary doctor --fix` で適用します（数分かかることがあります）。2 GiB 以上の既定 doctor は filesystem metadata のみです。
- v0.42.0 で削除（#2075）: `traceary store workspace-alias`（add/list/remove）。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary doctor --alias-add` / `--alias-remove` / `--alias-list` を使います（同じ review 済み alias 行。add は `--session`、`--workspace`、`--reviewed-by` が必要）。`doctor --fix` は alias を自動作成しません。既存 alias 行と `report workspace-identity` の grouping は変わりません。
- v0.42.0 で削除（#2074）: `traceary store archive`（create/verify/restore、`--delete-after-verify` を含む）と `traceary store retention`（`files plan` / `files apply`）。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary store compact --archive` / `--archive-verify` / `--archive-restore`（同じ verify-before-delete）と `traceary store compact --retention-plan` / `--retention-apply`（同じ immutable plan と `--confirm-plan-id`）を使います。既定の compact rewrite は変わりません。hook の `archive_then_gc` は内部で usecase を呼びます。
- v0.42.0 で削除（#2073）: `traceary session handoff`（`--compact-only` を含む）。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary context --handoff`（同じ TRACEARY HANDOFF フィールドラベル）または `traceary context --compact-only`（同じ再開サマリー。`--recent` 未指定時は 3）を使います。既定の `context` は生イベント + `--json` のままです。内部の `ContextUsecase.Handoff` と hook の `printCompactSummaryWithOptions` は残します。
- v0.42.0 で削除（#2072）: `traceary store capacity`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary doctor` の additive な `store-capacity` check を使います（同じ bounded InspectCapacity 経路）。2 GiB 以上の既定 doctor は metadata-only のままで、SQLite を開かず dbstat も歩きません。
- v0.42.0 で削除（#2071）: `traceary memory list`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary memory search --all` を使います（同じ List バックエンド、filter、既定 workspace scope、並び順、`--json`）。`--all` は query と同時に使えません。
- v0.42.0 で削除（#2070）: `traceary hooks print`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary hooks install --dry-run` を使います（同じ生成 config バイト列。`--client` / `--traceary-bin` / `--matcher`）。
- v0.42.0 で削除（#2069）: `traceary timeline`。呼び出しは unknown command として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary list --blocks` を使います（同じギャップ検出ブロック、#2033 scan-cap 開示、`workspace_breakdown` JSON。`--gap` は `list` に移しました）。
- v0.42.0 で削除（#2068）: `traceary tail`。呼び出しは unknown command として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `traceary list --follow` を使います（同じストリーム・フィルタ・描画。`--follow-session` は `list` に移しました）。
- v0.42.0 で削除（#2061）: `traceary sessions`（`--snapshot` / `--snapshot --json` を含む）。呼び出しは unknown command として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。代わりに `list` / `search` / `context` / `report` / `session handoff` を使います。hook の workspace 正規化向け `Session.List` と `list_sessions.sql` は残します。
- v0.42.0 で削除（#2057）: `traceary session latest`（`--active` を含む）と `traceary session list`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。one-minor deprecation window の明示的なポリシー例外です（owner 決定 2026-08-17）。open session の ID は hook メッセージ（`[Traceary] Session <id>`）で渡り、直近の作業は `list` / `search` / `context`、期間サマリーは `report` です。内部の `Active` / `Latest` / `List` クエリは handoff / hooks / context / memory extract のために残します。
- v0.36.0 で削除（v0.35 の非推奨のあと。#1692 / #1870）: `traceary memory store remember`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。代わりに `traceary memory store propose`（`status=candidate`）を使います。skill `traceary-memory-remember` はすでに `propose` に着地します。
- v0.36.0 で削除（#1704）: `traceary session active`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。代わりに `traceary session latest --active` を使います（stale の既定は同じ: 24h、`--stale-after`、`--allow-stale`）。`--active` なしの `--stale-after` / `--allow-stale` は拒否します。振る舞いは同じで、綴りだけを既存コマンドに畳みました。
- v0.35.0 で削除（#1872）: ストアサイズ削減コマンド一式を `traceary store compact` に畳みました。呼び出しは unknown command として非ゼロ終了し、`DEPRECATED` 通知は出しません。削除: `traceary store gc`、`traceary store dedupe` / `content-events`、`traceary store retention plan|apply|restore`（本文 retention。`store retention files` は残します）、`traceary store payload-rehearsal`（`preview|run|resume|scrub|rollback`）、`traceary store payload-backfill`（`preview|run|resume|status`）、`traceary store search-retire`、`traceary store compact plan|apply|resume|status`。代わりに `traceary store compact`（任意の `--force`、`--keep-days`）と `traceary store compact rollback RUN_ID` を使ってください。`traceary store search-projection` は変更しません。旧ファイルは rollback を捨てるまで archive です。
- v0.35.0 で削除（#1871）: `traceary mcp-server`、`presentation/mcpserver` パッケージ、9 個の MCP tool、および出荷しているすべての host package の MCP server 宣言（Claude / Codex / Gemini / Grok / Kimi / Antigravity）。呼び出しは unknown command（`unknown command "mcp-server"`）として非ゼロ終了し、`DEPRECATED` 通知は出しません。これは one-minor deprecation window の明示的なポリシー例外です。MCP は公開コマンドで、v0.34 の「現在非推奨」registry にも載っていませんでした。削除は #1693 の owner 決定であり、「何も失われない」証拠に基づきます — 歴史的な MCP write は 659,304 イベント中 16 件（0.0024%、最終 write 2026-07-19）、hook capture は shell（`traceary hook …`）のまま、出荷ホストはすべて shell を持ち、skill は CLI 経由です（#1875）。同じ作業には CLI を使ってください（例: `session handoff` / `context`、`search`、`list`、`report`、memory inbox/store/admin）。Claude の `hooks.json` は `matcher: mcp__.*` を残し、*他サーバ*の tool call audit を継続します。
- v0.35.0 で削除（#1869）: `traceary session tree` と `traceary session lineage`。呼び出しは unknown subcommand として非ゼロ終了し、`DEPRECATED` 通知は出しません。script 向けの active-session view には `traceary sessions --snapshot` / `--snapshot --json` を使ってください。
- v0.35.0 で削除（v0.34 の非推奨 #1688 / #1690）: `traceary top`（`traceary top --snapshot` / `--snapshot --json` を含む）。呼び出しは unknown command として非ゼロ終了し、`DEPRECATED` 通知は出しません。`traceary sessions`（または `traceary sessions --snapshot` / `--snapshot --json`）を使ってください。snapshot 契約は変更していません。
- v0.35.0 で削除（v0.34 で予告、#1765 / #1766）: 対話的な `traceary sessions` live dashboard。bare の `traceary sessions` は plain text command になり、どの caller でも `traceary sessions --snapshot` とバイト単位で同一です。`sessions --snapshot` / `--snapshot --json` は変更していません。
- v0.35.0 で削除（v0.34 で予告、#1687 / #1764）: `traceary tui`、`traceary dashboard`、および operator cockpit を開いていた bare 対話 TTY 既定動作。bare `traceary` は TTY / 非 TTY とも常に help を表示します。関連する session データの存続する script 向け view には `traceary sessions --snapshot` を使ってください。孤立した local state ファイル `~/.local/state/traceary/cockpit.json`（または `$XDG_STATE_HOME/traceary/cockpit.json`）は手動で削除して安全です。Traceary はもう読み書きしません。
- v0.35.0 で削除（v0.34 の非推奨 #1689 / #1691）: `traceary memory admin graph add` と `traceary memory admin graph list`（置き換え先なし。reference store の `memory_edges` は 0 行でした）。`memory_edges` テーブル自体は gc と bundle export/import のため残しています。
- v0.35.0 で削除（v0.34 の非推奨 #1689 / #1691）: `traceary session label`、`traceary session list --label`、`session list` テキスト出力の `LABEL` 列、`session list` JSON の `label` フィールド（置き換え先なし。reference store の label 付き session は 0 件でした）。ストア schema の `sessions.label` 列は残しています。
- v0.35.0 で置換（v0.34 で予告、#1717 / #1775）: `traceary search --json` のトップレベル配列 → `{"events": [...], "sessions": [...]}` オブジェクト。どちらのキーも常に存在し、ヒットがない tier は空配列です。
- v0.14.0 で削除: `traceary init` → `traceary store init`、`traceary backup` → `traceary store backup ...`、`traceary gc` → `traceary store gc`、`traceary handoff` → `traceary session handoff`、`traceary compact-summary` → `traceary session handoff --compact-only`、廃止済み `traceary integration codex install` helper → Codex 公式 `/plugins` flow。
- v0.15.0 で削除: `traceary memory accept`、`traceary memory reject`、`traceary memory remember`、`traceary memory propose`、`traceary memory distill`、`traceary memory extract`、`traceary memory supersede`、`traceary memory expire`、`traceary memory set-validity`、`traceary memory import codex`、`traceary memory import instructions`、`traceary memory export`、`traceary memory activate`、`traceary memory hygiene scan`、`traceary memory hygiene apply`、`traceary memory graph add`、`traceary memory graph list`。canonical な `memory inbox` / `memory store` / `memory admin` path を CLI リファレンスに従って使ってください。
- v0.15.0 で削除: `traceary integration codex uninstall` → Codex 公式 `/plugins` flow と `docs/integrations/codex-plugin.md` の手動 cleanup 手順。
- v0.20.0 で非表示化・v0.25.0 で完全削除 (#1266): `traceary integration` コマンド subtree（`integration` 親、`codex` group、および install/uninstall の旧 migration stub）。呼び出しは unknown command として失敗します。Codex CLI 公式の `/plugins` flow を使ってください。

## 非推奨通知の出し方

公開・admin の command path、フラグ、JSON フィールド名、出力形状を caller に影響する形で変更する必要が生じたとき、Traceary は以下の単一フローに従います。同じ単一の通知形式は default behaviour の変更にも適用します。この場合、通知で示す subject は command path ではなく、その動作です。

### stderr 通知

非推奨コマンドは実行のたびに stderr に **必ず 1 行** 以下の形式を出します。

```
DEPRECATED: this command is deprecated, use `<canonical replacement>` instead. Removal target: v<X.Y>.
```

`TRACEARY_LANG=ja` のときは同じ構造の日本語版が出ます。

```
DEPRECATED: このコマンドは非推奨です。代わりに `<canonical replacement>` を使用してください。削除予定: v<X.Y>。
```

通知ルール：

- 通知文には canonical 置き換え先のコマンド（サブコマンドのフルパス、たとえば `traceary memory admin hygiene scan`）を含める。親グループ名だけで省略しない。
- 後継なしで削除するサーフェスでは、置き換え先を記載せず、置き換え先がないことを通知する。非推奨項目には、何も失われないことの根拠を記載する。
- 通知文に削除予定バージョン（`v0.15`、`v1.0` など）を含める。
- 通知は **stderr** に出す。これにより stdout / `--json` / NDJSON の出力は canonical コマンドとバイト一致を保てる。Cobra 組み込みの `Deprecated` フィールドは stdout に出すため、Traceary は自前で stderr に書く。
- 1 回の実行で通知は 1 行のみ。非推奨コマンドが親グループでサブコマンドが実エントリーの場合も、実行された leaf に対して 1 度だけ発火し、canonical leaf の正確なパスを指す。
- 通知はコマンドが実際に実行されたときに出る。そのため pre-run hook ではなく run 段に取り付ける。Cobra は `--help` の解決、引数エラーの判定、必須フラグの検証をコマンド実行より前に行うため、いずれの経路でも通知は出ない。その代わり、非推奨コマンドの `Short` / `Long` には非推奨である旨と削除予定バージョンを必ず含め、`--help` だけでも呼び出し側に伝わるようにする。

### stdout / JSON / NDJSON 互換性

非推奨ウィンドウの間、非推奨コマンドは以下を維持しなければなりません。

- 旧来と同じ stdout テキストバイト
- 旧来と同じ `--json` 出力（フィールド名、契約に明記された並び、NDJSON の 1 行形状）
- 旧来と同じ終了コード
- 旧来と同じ `--id-only` バイト形状

help / usage テキストはこの保証の対象外です。上記の通知ルールは非推奨コマンドの `Short` / `Long` の変更を**要求**しており、それは親コマンドの一覧表示も変えます。help のバイトを固定すると、フロー自体が矛盾します。automation は `--help` を parse しないでください。help は、非推奨を維持するのではなく告知するための唯一の出力です。

非推奨 alias に新しい optional フラグを足してよいのは、canonical 置き換え先にも同じフラグがある場合のみ（caller が引数を書き換えずに移行できる範囲）。

コマンドではなくフラグを非推奨にする場合も、同じ形式の stderr 通知を使います。フラグは非推奨ウィンドウの間は旧来挙動を維持し、通知文には置き換え先フラグを含め、`CHANGELOG.md` の "Deprecated" にエントリを追加します。

### ドキュメント要件

すべての非推奨化は同じ変更で 3 箇所を更新します。

1. CLI リファレンス（`docs/cli/README.md` と `docs/cli/README.ja.md`）— 非推奨パスに置き換え先と削除予定を明記。
2. CHANGELOG（`CHANGELOG.md` と `CHANGELOG.ja.md`）— "Deprecated" または "Changed" の項に、パス・置き換え先・削除予定バージョンを書く。
3. 大きなサーフェス整理の一部のときは関連する operations / 計画ドキュメント（例: memory tree 再編なら [memory コマンド体系の整理計画](./operations/memory-command-surface.ja.md)）。

## 互換性ウィンドウ

### 1 マイナー互換ウィンドウ（既定）

非推奨ウィンドウの既定は **1 マイナー** です。v0.N.0 で非推奨にしたコマンド・フラグ・JSON 形状は、v0.N.x の patch を通して通知付きで動作し続け、v0.(N+1).0 で削除されます。

この既定に従う例：

- v0.14.0 で導入した memory tree のグループ化（`memory inbox` / `memory store` / `memory admin`）。フラットな verb（`memory remember`、`memory propose`、`memory accept` など）は v0.14.x 全体で hidden な deprecated alias として動作し、v0.15.0 で削除されました。詳細は [memory コマンド体系の整理計画](./operations/memory-command-surface.ja.md)。
- 廃止された Codex install ヘルパー。v0.14.0 では片付け用の uninstall を hidden cleanup-only として残し、v0.15.0 で削除しました。

### 出力に影響する破壊的変更は窓を延ばすことがある

公開 `--json` envelope、構造化テキスト契約（`traceary context --handoff` など）、AI SKILL が直接ワイヤしている公開コマンドパスといった「重く scripted されている」サーフェスの破壊的変更については、メンテナの裁量で 1 マイナーより長い窓を取ることがあります。決定は元イシューと CHANGELOG エントリに記録します。これは例外的な扱いで既定ではありません。

この規定で予告済みのもの:

- **`traceary search --json` は v0.35.0 でオブジェクトになりました（#1717 / #1775）。** v0.34.0 は、session tier ヒットをイベント行と混在させずに載せられるようにトップレベル配列を `{"events": [...], "sessions": [...]}` に替えることを予告しました。v0.34.x では配列を維持し、省略したセッションヒットを stderr に通知し、v0.35.0 で置換を完了しました。どちらのキーも常に存在し、ヒットがない tier は空配列です。

### 非推奨ウィンドウが不要なケース

純粋に追加だけの変更には非推奨ウィンドウは不要です。

- 新しい公開サブコマンドの追加
- 新しい optional フラグの追加
- JSON オブジェクトの末尾に新しい optional フィールドを追加（consumer は未知フィールドを許容すること）
- `traceary doctor` の新セクションの追加

これらを **削除・改名** すると破壊的変更になり、deprecation フローを通します。

## v0 と v1 での削除ポリシー

### v0.x 系列

Traceary は現状 `v0.x` 系列です。v0.x の意図は「v1.0 までに、予告された決まったリズムでサーフェスを安定化させる」ことです。

- **公開コマンド**: 破壊的変更はマイナー境界 (`v0.N.0` → `v0.(N+1).0`) で許容、上記の 1 マイナーウィンドウを使う。patch (`v0.N.x`) は非破壊。
- **admin コマンド**: 既定は公開と同じ。対象が運用者だけのときは「v0.N で非推奨 → v0.(N+1) で削除」のより速いペースをメンテナが選択してよい。
- **plumbing / hidden / deprecated コマンド**: 通知文に名指しされたマイナーで削除する。

v0.14.0 で除去された旧 top-level alias（`traceary init`、`traceary backup`、`traceary gc`、`traceary handoff`、`traceary compact-summary`）はこのモデルに従いました。v0.9.0 で非推奨、v0.14.0 で削除、その間は通知と置き換え先案内を継続出力。

### v1.0 以降

v1.0 リリース以降：

- **公開コマンド**: `v1.x` 系列全体で安定。破壊的変更はメジャー境界 (`v1.x` → `v2.0`) のみ。マイナー (`v1.0.0` → `v1.1.0`) は後方互換を保ち、既存の公開コマンドパス・フラグ名・終了コード・stdout 形状・文書化された JSON フィールド名は次マイナーでもバイト一致で動く。
- **admin コマンド**: `v1.x` 内のマイナー間でも後方互換だが、admin 専用フラグの追加・改名はマイナー境界で許容（上記 deprecation フローを通せば、最低 1 マイナーは stderr 通知付きで動く）。
- **plumbing / hidden / deprecated コマンド**: v0.x と同じく、通知に名指しされたマイナーで削除。
- **メジャー移行**: 将来 `v2.0` を計画するとき、`v1.x` 系列の最後のマイナー (`v1.last`) で `v2.0` で変える項目すべてに stderr 通知を出す。`v2.0` リリースノートには同じ集合を再掲し、外部 caller が見るべき移行リストが 1 箇所に揃う形にする。

要約: v0.x はマイナー境界で 1 マイナー overlap を取りながらサーフェスを動かす。v1.x は公開サーフェスをメジャー全体で凍結する。v2.0（あるとすれば）が次の公開サーフェス更新タイミング。

## 本ポリシーの対象外

このポリシーは CLI サーフェスを対象とします。以下は別ドキュメントで扱います。

- hook capture の安定性 — [hook contract](./hooks/contract.ja.md) と [host coverage matrix](./hooks/host-coverage.ja.md)。
- ストレージ / SQLite スキーママイグレーション — [ストレージモデル](./storage/README.ja.md)。
- host-native memory activation marker の互換性 — [host-native memory activation contract](./architecture/host-native-memory-activation.ja.md)。

## 関連ドキュメント

- [CLI リファレンス](./cli/README.ja.md)
- [memory コマンド体系の整理計画](./operations/memory-command-surface.ja.md)
- [JSON / snapshot contract test](./operations/json-contract-tests.ja.md)
- [リリースガイド](./release/README.ja.md)
- [README](../README.ja.md)
