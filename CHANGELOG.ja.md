# Changelog

[English](./CHANGELOG.md)

このファイルは、Traceary の各リリースで何が入ったかを時系列で追いやすくするための changelog です。  
release note と同じ粒度で、版ごとの要点だけをまとめています。

## [v0.1.18] - 2026-04-10

セッションメタデータの充実とデータ品質の向上を行いました。

### 追加
- `sessions` テーブル新設 + 既存 events からのバックフィルマイグレーション
- `traceary session label <text> --session-id <id>` コマンド — セッションにラベルを付与
- `session list --label` フィルタ
- `traceary session end --summary` フラグ — セッション終了時にサマリーを記録
- `traceary session start --parent-session-id` フラグ — サブエージェントの親子関係を記録
- Claude Code hooks で MCP ツール呼び出し（`mcp__.*`）を記録
- Gemini CLI ワンコマンドインストールスクリプト

### 修正
- audit hooks でセッション開始時の repo を永続化し、CWD ベースの repo drift を防止
- MCP ツール呼び出し時に `tool_name` へ fallback するよう audit スクリプトを改善
- 日付バリデーションを queryservice 層に集約（infra 層の冗長なバリデーションを除去）
- doctor 設定チェックの `localizef` 引数過剰を修正

### 変更
- `session list` クエリを sessions テーブル主体に書き換え（events JOIN で集計）
- `SessionSummary` DTO に `label`/`summary`/`parent_session_id` を追加
- 統合マニフェストのバージョンを `0.1.18` に更新

### 含まれるイシュー
- #196 日付バリデーションの queryservice 層集約
- #200 hooks による MCP ツール呼び出し記録
- #201 セッションにラベル/タスク名を付与
- #202 セッション間の親子関係を記録
- #203 repo フィールドの正規化
- #204 セッション終了時にサマリーを記録
- #206 セッションメタデータモデルの導入
- #207 Gemini CLI インストール体験の改善

## [v0.1.17] - 2026-04-09

マルチエージェントワークフローの改善と CLI の使い勝手向上を行いました。

### 追加
- `traceary session list` コマンド — セッションごとの集約情報（ステータス、所要時間、イベント/コマンド数、エージェント内訳）を表示
- Claude Code のサブエージェント識別 — hooks が payload の `agent_type` を読み取り、サブエージェント内実行時に階層的なエージェント名（例: `claude/Explore`）を記録
- `session list` の `--from`/`--to` 日付フィルタ（YYYY-MM-DD バリデーション付き）

### 修正
- `list`/`search` のテーブル出力で MESSAGE カラムを 80 文字に切り詰め、改行を正規化 — 長いコマンドボディによるターミナル表示崩れを防止
- DB 初期化時の `chmod(0600)` エラーを best-effort に変更 — read-only ファイルシステムや他ユーザー所有の DB でも読み取り系コマンドが動作

### 変更
- CLAUDE.md / AGENTS.md / GEMINI.md に「1 issue = 1 branch = 1 PR（例外なし）」ルールを追加
- 統合マニフェストのバージョンを `0.1.17` に更新

### 含まれるイシュー
- #185 list/search 出力の MESSAGE カラム切り詰め
- #186 セッションサマリーコマンド (`session list`) の追加
- #187 Claude Code のサブエージェント識別対応
- #188 read-only ファイルシステムでの読み取りコマンド安全化
- #194 1-issue-1-PR ルールのガードレール追加

## [v0.1.16] - 2026-04-09

この release では、コード品質の改善、ユーザー設定可能な監査リダクションパターンの追加、CLI / MCP サーバー全体での debug レベル診断の充実を行いました。

### Added
- `~/.config/traceary/config.json` による監査リダクションパターンのユーザー設定 — 追加の正規表現パターンが CLI（`traceary audit`）と MCP サーバー（`add_audit`）の両方で組み込みルールの後に適用される
- `infrastructure/sqlite/` 全体で、抑制されていたクリーンアップエラーの debug レベルログ出力
- セッションおよびリポジトリコンテキスト解決の各フォールバック段階での debug レベルログ出力
- AI エージェントの動作統一のための `CLAUDE.md`、`AGENTS.md`、`GEMINI.md` プロジェクト規約ファイル
- `LoadConfig`、`compileExtraRedactPatterns`、`setupLogger` のテスト追加

### Fixed
- `init()` 内の `log.Fatalf` を `run()` からの graceful error return に置換 — 不正な `LOG_LEVEL` でスタックトレースではなくクリーンなエラーメッセージを出力
- MCP サーバーがリクエストごとではなく起動時に一度だけ config を読み込むよう修正

### Changed
- エージェント設定ファイルにイシュークローズポリシーを追加（実装 PR はサブイシューのみクローズ、親イシューはリリース PR でクローズ）
- AI エージェント設定ファイルを docs i18n チェックから除外
- 環境リファレンスドキュメントに config.json と追加リダクションパターンの説明を追記
- release 向け integration manifest version を `0.1.16` に更新

### Included issues
- #170 Replace panic calls in CLI initialization with graceful errors
- #171 Make audit redaction patterns user-configurable
- #172 Add debug logging for suppressed cleanup errors
- #173 Clarify error propagation in session resolution logic

## [v0.1.15] - 2026-04-09

この release では、`v0.1.14` の dogfood で残っていた follow-up を閉じ、ローカル専用 Git repository の扱いと `traceary doctor` の first-run 表示を実用的に整えました。

### Fixed
- `remote.origin.url` が無いローカル専用 Git repository でも、Git worktree ルートへ fallback するようにし、`traceary log` / `traceary audit` が active session を再利用できるようにした
- packaged hook script でも同じ fallback を使うようにし、Claude / Codex / Gemini integration と CLI の挙動をそろえた
- `traceary doctor` が host config 未作成などの first-run 状態を generic failure ではなく `warn` として返すようにした
- setup ガイダンスを妨げる hook script materialize 問題は、必ずしも壊れた install を意味しないため、より明確な `warn` メッセージで出すようにした

### Changed
- root README と CLI / hooks / environment docs に、local-only Git worktree fallback と `traceary doctor` の `warn` / `fail` の意味を追記した
- release 向け integration manifest version を `0.1.15` に更新した
- release guide の固定 tag 例を `v0.1.15` に更新した

### Included issues
- #165 Make doctor clearer on first-run integration states
- #166 Improve work-context detection for local-only git repositories

## [v0.1.14] - 2026-04-09

この release では、`v0.1.13` 以降に main へ入っていた integration / runtime 修正と、それを公開するための release metadata 整備をまとめて収録しています。

### Fixed
- 共通 `SessionEnd` hook を冪等化し、Gemini が session-end hook を重複で呼んでも `session_ended` を 1 回だけ記録するようにした
- Codex の local install helper が active plugin cache の配置、`codex_hooks` の有効化、`~/.codex/hooks.json` への Traceary hook マージまで行うよう修正した
- Codex の uninstall helper が `config.toml` 内の nested plugin subtable までまとめて消すよう修正した
- GoReleaser が生成する Homebrew formula の test を `traceary --version` に修正した

### Changed
- root README と host integration docs を、手動 CLI より plugin / extension 導入を先に案内する構成へ整理した
- release 向け integration manifest version を `0.1.14` に更新した
- 抜けていた `v0.1.12` / `v0.1.13` の changelog を補完し、release guide の古い固定 tag 例を解消した

### Included issues
- #159 Codex local install does not activate the Traceary plugin runtime
- #160 Gemini extension records duplicate `session_ended` events
- #161 Root README should prioritize plugin and extension install flows
- #163 Align release metadata with the current release line
- #164 Use --version in the generated Homebrew formula test

## [v0.1.13] - 2026-04-09

### Added
- `traceary log`, `traceary audit`, `traceary session {start,end,latest,active}` に `--json` を追加
- `traceary list` に `--kind`, `--client`, `--agent`, `--session-id`, `--repo` の構造化 filter を追加

### Changed
- `traceary session latest` を、同一 session context 内で最新の lifecycle boundary を優先する意味に再定義した
- manual command の default / JSON 出力 / hooks guidance を CLI help と docs で分かりやすく整理した

### Fixed
- 同じ session が複数回 start された場合でも、最新の `session_started` を優先するよう修正
- 同じ `session_id` を別 repo / agent が再利用している場合、他 context の lifecycle boundary を拾わないよう修正
- cross-context の latest-session / active-session lookup に対する regression coverage を追加

### Included issues
- #146 dogfood ergonomics follow-up
- #147 fix session latest semantics for ended sessions
- #148 align machine-readable output for mutating and session helper commands
- #149 improve `traceary audit` ergonomics
- #150 add structured filters to `traceary list`
- #151 surface environment variables and defaults in CLI help
- #152 improve `hooks print` discoverability
- #153 clarify and standardize manual CLI defaults

## [v0.1.12] - 2026-04-09

### Added
- Claude Code / Codex / Gemini CLI 向けの共通 native integration contract
- Traceary の MCP / hooks / commands / skill surface を含む Claude Code plugin package
- Traceary の MCP / hooks / commands / skill surface を含む Codex plugin package
- Traceary の MCP / hooks / commands / skill surface を含む Gemini CLI extension package
- integration validation / packaging coverage と install / update / uninstall / smoke-test guidance

### Included issues
- #140 native agent integrations
- #141 define the shared integration contract
- #142 publish a Claude Code plugin
- #143 publish a Codex plugin
- #144 publish a Gemini CLI extension
- #145 add install/update/uninstall/doctor guidance and smoke tests

## [v0.1.11] - 2026-04-09

### Added
- README やリリース画面で使える最小限の Traceary マーク

### Changed
- トップレベル README を短い導入ページに整理し、詳細な導線を docs 索引へ集約
- `docs/README.md` / `docs/README.ja.md` を詳細ドキュメントの中心として再構成
- README / CONTRIBUTING / 主要ガイドの日本語表現を見直し、より自然な文体に調整
- 脆弱性の連絡先を `CONTRIBUTING.md` / `CONTRIBUTING.ja.md` に集約し、独立した `SECURITY.md` / `SECURITY.ja.md` を廃止

### Included issues
- #133 public surface polish
- #134 rewrite Japanese docs into natural Japanese
- #135 simplify README and reduce link sprawl
- #137 reorganize docs landing pages and cross-links
- #138 reassess and minimize the security-policy footprint
- #139 add a minimal visual identity

## [v0.1.10] - 2026-04-09

### Fixed
- GoReleaser の Homebrew 設定が生成された archive ID を参照するよう修正し、tagged release で archive と tap formula を再び正常に公開できるようにした

## [v0.1.9] - 2026-04-09

### Added
- `--yes` 付きの安全な backup restore フロー
- mutating command 向けの script-friendly な `--id-only`
- `traceary audit` の named flags (`--command`, `--input`, `--output`)
- CLI / environment / storage / operations / interactive の専用 docs
- GoReleaser formula automation による Homebrew 配布導線
- recent events parity のための read-only MCP tool `list_events`
- Bash / Zsh / Fish / PowerShell 向け `traceary completion`

### Changed
- onboarding と hooks docs は guided setup と failure-mode check をより早く辿れるよう改善
- `traceary log` / `traceary audit` は、まず解決済み repo / work context に対する最新 non-stale active session を再利用し、見つからない場合だけ `default` に fallback するよう変更
- 公開向け README に CI / release badge と、privacy / no-telemetry / support posture を追加
- hooks / storage / operations docs で runtime assumption をより明示的に文書化

### Included issues
- #106 onboarding and daily-use ergonomics
- #107 safer backup restore flow
- #108 script-friendly mutating command output
- #109 named audit flags
- #110 CLI / env reference docs
- #111 onboarding / first-run docs
- #112 Homebrew distribution flow
- #113 guided setup for supported clients
- #114 storage model / schema / gc docs
- #115 active session defaults for manual log / audit
- #116 hook edge cases and failure-mode docs
- #117 MCP read workflow parity
- #118 public OSS trust and polish
- #119 concurrency / hook-state assumptions
- #120 interactive inspection ergonomics

## [v0.1.8] - 2026-04-08

### Added
- DB と hooks 設定を診断する `traceary doctor` / `traceary status`
- 公開向け `SECURITY.md` / `SECURITY.ja.md`
- `traceary backup create` / `traceary backup restore`
- `docs/backup/` 配下のバックアップ / マシン移行ガイド
- MCP の session lifecycle tools: `start_session`, `end_session`, `latest_session`, `active_session`

### Changed
- `hooks install` は既存の対応 client config に対して、既定で Traceary 管理下 hook を merge するよう改善
- portable hook scripts は runtime で `python3` を不要化
- `traceary audit` は common な secret っぽい値を保存前に伏せ字化し、CLI / MCP 出力で redaction を通知するよう変更
- 公開向け README / hooks / MCP 文書で command surface と platform support の説明を整合
- `traceary list` / `traceary search` は `--offset` による安定した pagination をサポート

### Included issues
- #88 operational safety and public usability
- #89 safe hooks config merge
- #90 doctor / status diagnostics
- #91 audit secret persistence hardening
- #92 public security policy
- #93 README / platform support alignment
- #94 list / search pagination
- #95 MCP session ergonomics
- #96 backup / export / import story
- #97 reduce hook runtime dependency friction

## [v0.1.7] - 2026-04-08

### Added
- MIT の `LICENSE` を追加
- 公開向け `CONTRIBUTING.md` / `CONTRIBUTING.ja.md` を追加
- `docs/mcp/` に公開向け MCP integration ガイドを追加

### Changed
- `traceary session end` で flag を省略した場合、対応する `session_started` から `client` / `agent` / `repo` を継承するように変更
- 公開向け install / release 導線と GitHub Actions の release 自動化を追加
- CLI の operator-facing message は既定で英語とし、日本語は `TRACEARY_LANG=ja` で opt-in に変更
- hooks install は既定で source checkout 外の portable script を materialize するように変更

### Included issues
- #72 public release readiness
- #73 project license の追加
- #74 session end の attribution 継承
- #75 公開向け CLI 英語化
- #76 公開 install / release distribution flow
- #77 source checkout 非依存の hooks install
- #78 CONTRIBUTING guide の追加
- #79 公開向け MCP server integration 文書

## [v0.1.6] - 2026-04-08

### Changed
- `traceary init` が「任意の明示 bootstrap」であることを help / docs で明確化
- `traceary session end` が session ID ではなく記録した event ID を返すように変更
- `hooks --client` が `claude-code`, `codex-cli`, `gemini-cli` を alias として受け付けるように改善
- Cobra 由来の positional-argument エラーを日本語化

### Included issues
- #60 `traceary init` の役割と暗黙 DB 作成の整理
- #61 `traceary session end` の出力契約整理
- #62 `hooks print --client` の discoverability 改善
- #63 CLI 引数エラーの日本語化

## [v0.1.5] - 2026-04-08

### Changed
- `search --kind` の discoverability を改善
- すべての CLI コマンドで `TRACEARY_DB_PATH` をサポート
- CLI 失敗時の stderr を plain `Error: ...` に統一

### Included issues
- #53 `search --kind` の discoverability 改善
- #54 `TRACEARY_DB_PATH` サポート
- #55 plain CLI error output

## [v0.1.4] - 2026-04-08

### Added
- README / README.ja.md に Quick Start
- `traceary hooks install`
- `traceary context` / `traceary handoff`

### Changed
- `search` に構造化フィルタを追加
- active session の stale 判定を追加
- audit truncation を設定可能に改善

### Included issues
- #40 Quick Start
- #41 hooks install
- #42 structured search filters
- #43 context handoff
- #44 stale session handling
- #45 audit truncation configuration

## [v0.1.3] - 2026-04-08

### Fixed
- `session latest` / `session active` の no rows エラー二重化を解消
- session lookup の not-found 判定を sentinel error ベースに整理

### Included issues
- #37 `session latest/active` の no rows エラー修正

## [v0.1.2] - 2026-04-08

### Added
- `traceary list`, `traceary search`, `traceary show` に `--json`
- `traceary session active`

### Changed
- `session latest` の no-rows 判定を修正
- `RootCLI` の依存注入を `RootCLIOptions` に整理

### Included issues
- #28 `session latest` の no rows 判定修正
- #29 主要読み取りコマンドの JSON 出力
- #30 active session 取得導線
- #31 command audit output 検索の再確認
- #32 `RootCLIOptions` による依存注入整理

## [v0.1.1] - 2026-04-08

### Added
- `traceary show <event-id>`
- `traceary session latest`
- `traceary hooks print --client <...>`

### Changed
- hooks 設定例の出力が CLI から直接行えるようになり、dogfood の手順を短縮
- `hooks print` の既定 binary 解決を安定した `traceary` コマンド名に修正

### Included issues
- #19 dogfood usability improvements
- #20 `traceary show <event-id>`
- #21 `traceary session latest`
- #22 `traceary hooks print --client`
- #26 `hooks print` の follow-up 修正

## [v0.1] - 2026-04-07

### Added
- SQLite ベースの local store
- `traceary init`, `log`, `audit`, `list`, `search`, `session start/end`, `gc`
- MCP server (`add_log`, `add_audit`, `search`, `get_context`)
- Claude Code / Codex CLI / Gemini CLI 向け hooks integration

### Included issues
- #11 bootstrap CLI and SQLite store
- #12 log / list
- #13 session start / end
- #14 audit log
- #15 gc / retention
- #16 search / work context
- #17 MCP server
- #18 hooks integration

[v0.1]: https://github.com/duck8823/traceary/releases/tag/v0.1
[v0.1.1]: https://github.com/duck8823/traceary/releases/tag/v0.1.1
[v0.1.2]: https://github.com/duck8823/traceary/releases/tag/v0.1.2
[v0.1.3]: https://github.com/duck8823/traceary/releases/tag/v0.1.3
[v0.1.4]: https://github.com/duck8823/traceary/releases/tag/v0.1.4
[v0.1.5]: https://github.com/duck8823/traceary/releases/tag/v0.1.5
[v0.1.6]: https://github.com/duck8823/traceary/releases/tag/v0.1.6
[v0.1.7]: https://github.com/duck8823/traceary/releases/tag/v0.1.7
[v0.1.8]: https://github.com/duck8823/traceary/releases/tag/v0.1.8
[v0.1.9]: https://github.com/duck8823/traceary/releases/tag/v0.1.9

[v0.1.10]: https://github.com/duck8823/traceary/releases/tag/v0.1.10
[v0.1.11]: https://github.com/duck8823/traceary/releases/tag/v0.1.11
[v0.1.12]: https://github.com/duck8823/traceary/releases/tag/v0.1.12
[v0.1.13]: https://github.com/duck8823/traceary/releases/tag/v0.1.13
[v0.1.14]: https://github.com/duck8823/traceary/releases/tag/v0.1.14
