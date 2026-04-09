# Changelog

[日本語](./CHANGELOG.ja.md)

This file summarizes what changed in each Traceary release in chronological order.
It mirrors the same level of detail as the GitHub release notes, but keeps the history in the repository.

## [v0.1.15] - 2026-04-09

This release closes the last dogfood follow-ups from `v0.1.14` by making local-only git repositories behave like stable work contexts and by making `traceary doctor` clearer on first run.

### Fixed
- local-only git repositories now fall back to the git worktree root when `remote.origin.url` is missing, so `traceary log` / `traceary audit` reuse the active session instead of dropping back to `default`
- packaged hook scripts now use the same local-only git fallback, keeping Claude / Codex / Gemini integrations aligned with the CLI
- `traceary doctor` now reports first-run host config states as `warn` instead of generic failures and keeps JSON output machine-readable
- hook-script materialization problems that block setup guidance but do not necessarily mean a broken install are now surfaced as `warn` with clearer messages

### Changed
- documented the local-only git worktree fallback and the `warn` vs `fail` `traceary doctor` semantics across the root README and the CLI / hooks / environment docs
- updated release-facing integration manifests to version `0.1.15`
- refreshed the release guide examples to point at `v0.1.15`

### Included issues
- #165 Make doctor clearer on first-run integration states
- #166 Improve work-context detection for local-only git repositories

## [v0.1.14] - 2026-04-09

This release packages the integration/runtime fixes merged after `v0.1.13` together with the release-metadata alignment needed to publish them cleanly.

### Fixed
- made shared `SessionEnd` handling idempotent so duplicate Gemini session-end hook invocations record only one `session_ended` event
- fixed the Codex local install helper so it installs the active plugin cache, enables `codex_hooks`, and merges the Traceary-managed hooks into `~/.codex/hooks.json`
- fixed the Codex uninstall helper so nested plugin subtables are removed cleanly from `config.toml`
- corrected the GoReleaser Homebrew formula test to use `traceary --version`

### Changed
- reorganized the root README and host integration docs around plugin / extension installation before manual CLI workflows
- updated release-facing integration manifests to version `0.1.14`
- filled in the missing `v0.1.12` and `v0.1.13` changelog entries and removed stale pinned release examples from the release guide

### Included issues
- #159 Codex local install does not activate the Traceary plugin runtime
- #160 Gemini extension records duplicate `session_ended` events
- #161 Root README should prioritize plugin and extension install flows
- #163 Align release metadata with the current release line
- #164 Use --version in the generated Homebrew formula test

## [v0.1.13] - 2026-04-09

### Added
- `--json` support for `traceary log`, `traceary audit`, and `traceary session {start,end,latest,active}`
- structured filters for `traceary list`: `--kind`, `--client`, `--agent`, `--session-id`, and `--repo`

### Changed
- redefined `traceary session latest` to prefer the newest lifecycle boundary while keeping lookups scoped to the same session context
- improved manual command ergonomics around defaults, JSON output, and hooks guidance in CLI help and docs

### Fixed
- preferred the newest `session_started` event when the same session is started more than once
- ignored lifecycle boundaries from other repos or agents that reuse the same `session_id`
- added regression coverage for cross-context latest-session and active-session lookups

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
- a shared native integration contract for Claude Code, Codex, and Gemini CLI
- a Claude Code plugin package with the Traceary MCP server, hooks, commands, and skill surfaces
- a Codex plugin package with the Traceary MCP server, hooks, commands, and skill surfaces
- a Gemini CLI extension package with the Traceary MCP server, hooks, commands, and skill surfaces
- integration validation / packaging coverage plus install, update, uninstall, and smoke-test guidance

### Included issues
- #140 native agent integrations
- #141 define the shared integration contract
- #142 publish a Claude Code plugin
- #143 publish a Codex plugin
- #144 publish a Gemini CLI extension
- #145 add install/update/uninstall/doctor guidance and smoke tests

## [v0.1.11] - 2026-04-09

### Added
- a minimal Traceary mark for README and release surfaces

### Changed
- simplified the top-level README into a shorter landing page and moved the detailed navigation into the docs index
- reorganized `docs/README.md` / `docs/README.ja.md` as the central detailed documentation map
- rewrote the main Japanese docs and high-traffic guide pages into more natural Japanese
- moved private security-reporting guidance into `CONTRIBUTING.md` / `CONTRIBUTING.ja.md` and removed the standalone `SECURITY.md` files

### Included issues
- #133 public surface polish
- #134 rewrite Japanese docs into natural Japanese
- #135 simplify README and reduce link sprawl
- #137 reorganize docs landing pages and cross-links
- #138 reassess and minimize the security-policy footprint
- #139 add a minimal visual identity

## [v0.1.10] - 2026-04-09

### Fixed
- corrected the GoReleaser Homebrew configuration to reference the generated archive ID, allowing tagged releases to publish archives and the tap formula successfully again

## [v0.1.9] - 2026-04-09

### Added
- safer backup restore flow with interactive confirmation and `--yes`
- script-friendly `--id-only` output for mutating commands
- named `--command`, `--input`, and `--output` flags for `traceary audit`
- dedicated CLI/environment/storage/operations/interactive docs
- Homebrew distribution flow backed by GoReleaser formula automation
- `list_events` as a read-only MCP tool for recent-event parity
- `traceary completion` for Bash, Zsh, Fish, and PowerShell

### Changed
- onboarding and hooks docs now point users at guided setup and failure-mode checks earlier
- `traceary log` and `traceary audit` now reuse the latest non-stale active session for the resolved repo/work context before falling back to `default`
- the public README now includes CI/release badges plus explicit privacy / no-telemetry / support posture
- hooks, storage, and operations docs now document runtime assumptions more explicitly

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
[v0.1.9]: https://github.com/duck8823/traceary/releases/tag/v0.1.9

[v0.1.10]: https://github.com/duck8823/traceary/releases/tag/v0.1.10
[v0.1.11]: https://github.com/duck8823/traceary/releases/tag/v0.1.11
[v0.1.12]: https://github.com/duck8823/traceary/releases/tag/v0.1.12
[v0.1.13]: https://github.com/duck8823/traceary/releases/tag/v0.1.13
[v0.1.14]: https://github.com/duck8823/traceary/releases/tag/v0.1.14
