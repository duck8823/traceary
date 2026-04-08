# Changelog

[日本語](./CHANGELOG.ja.md)

This file summarizes what changed in each Traceary release in chronological order.
It mirrors the same level of detail as the GitHub release notes, but keeps the history in the repository.

## [v0.1.8] - 2026-04-08

### Added
- `traceary doctor` / `traceary status` for DB and hooks diagnostics
- public `SECURITY.md` / `SECURITY.ja.md`
- `traceary backup create` / `traceary backup restore`
- dedicated backup / transfer guides under `docs/backup/`
- MCP session lifecycle tools: `start_session`, `end_session`, `latest_session`, and `active_session`

### Changed
- `hooks install` now merges Traceary-managed hooks into supported existing client config files by default instead of forcing full replacement
- portable hook scripts no longer require `python3` at runtime
- `traceary audit` now redacts common secret-like values before persistence and reports redaction in CLI / MCP output
- public README / hooks / MCP docs now align command surface and platform support expectations
- `traceary list` and `traceary search` now support stable offset pagination with `--offset`

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
- added the MIT `LICENSE`
- added public `CONTRIBUTING.md` / `CONTRIBUTING.ja.md`
- added public MCP integration guides under `docs/mcp/`

### Changed
- `traceary session end` now inherits `client`, `agent`, and `repo` from the matching `session_started` event when those flags are omitted
- added public install/release distribution docs plus GitHub Actions release automation
- switched default operator-facing CLI messaging to English, with `TRACEARY_LANG=ja` as the Japanese opt-in
- changed hooks installation to materialize portable scripts outside the source checkout by default

### Included issues
- #72 public release readiness
- #73 add a project license
- #74 preserve start-time attribution on session end
- #75 make public CLI messaging viable in English
- #76 add public install and release distribution flow
- #77 make hooks install portable outside the source tree
- #78 add CONTRIBUTING guide
- #79 document public MCP server integration

## [v0.1.6] - 2026-04-08

### Changed
- clarified in help/docs that `traceary init` is an optional explicit bootstrap
- changed `traceary session end` to return the recorded event ID instead of the session ID
- improved `hooks --client` to accept `claude-code`, `codex-cli`, and `gemini-cli` as aliases
- localized Cobra-based positional-argument errors into Japanese user-facing messages

### Included issues
- #60 clarify the role of `traceary init` and lazy DB creation
- #61 normalize the `traceary session end` output contract
- #62 improve `hooks print --client` discoverability
- #63 localize CLI argument errors

## [v0.1.5] - 2026-04-08

### Changed
- improved `search --kind` discoverability
- added `TRACEARY_DB_PATH` support across all CLI commands
- standardized CLI failure stderr output to plain `Error: ...`

### Included issues
- #53 improve `search --kind` discoverability
- #54 support `TRACEARY_DB_PATH`
- #55 plain CLI error output

## [v0.1.4] - 2026-04-08

### Added
- Quick Start in `README.md` / `README.ja.md`
- `traceary hooks install`
- `traceary context` / `traceary handoff`

### Changed
- added structured filters to `search`
- added stale-session handling for active sessions
- made audit truncation configurable

### Included issues
- #40 Quick Start
- #41 hooks install
- #42 structured search filters
- #43 context handoff
- #44 stale session handling
- #45 audit truncation configuration

## [v0.1.3] - 2026-04-08

### Fixed
- removed double-wrapped no-rows errors in `session latest` / `session active`
- normalized session lookup not-found handling around a sentinel error

### Included issues
- #37 fix no-rows errors in `session latest/active`

## [v0.1.2] - 2026-04-08

### Added
- `--json` support for `traceary list`, `traceary search`, and `traceary show`
- `traceary session active`

### Changed
- fixed the no-rows behavior in `session latest`
- organized dependency injection around `RootCLIOptions`

### Included issues
- #28 fix no-rows handling in `session latest`
- #29 JSON output for major read commands
- #30 active-session retrieval flow
- #31 reconfirm command audit output search behavior
- #32 `RootCLIOptions` dependency injection cleanup

## [v0.1.1] - 2026-04-08

### Added
- `traceary show <event-id>`
- `traceary session latest`
- `traceary hooks print --client <...>`

### Changed
- made hook config examples directly available from the CLI, shortening dogfood setup steps
- changed the default binary resolution in `hooks print` to the stable `traceary` command name

### Included issues
- #19 dogfood usability improvements
- #20 `traceary show <event-id>`
- #21 `traceary session latest`
- #22 `traceary hooks print --client`
- #26 `hooks print` follow-up fix

## [v0.1] - 2026-04-07

### Added
- SQLite-based local store
- `traceary init`, `log`, `audit`, `list`, `search`, `session start/end`, and `gc`
- MCP server (`add_log`, `add_audit`, `search`, `get_context`)
- hooks integration for Claude Code, Codex CLI, and Gemini CLI

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
