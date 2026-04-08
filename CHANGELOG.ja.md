# Changelog

[English](./CHANGELOG.md)

このファイルは、Traceary の各リリースで何が入ったかを時系列で追いやすくするための changelog です。  
release note と同じ粒度で、版ごとの要点だけをまとめています。

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
