# Changelog

[日本語](./CHANGELOG.ja.md)

This file summarizes what changed in each Traceary release in chronological order.
It mirrors the same level of detail as the GitHub release notes, but keeps the history in the repository.

## [Unreleased]

### Added
- **Manual archive-before-GC (#1371)** — `traceary store archive create|verify|restore` exports GC-eligible cold rows to a versioned package (gzip tar + manifest digests, optional passphrase via env), verifies integrity, and can `--delete-after-verify` only the exact archived identities. Restore is primary-key idempotent.
- **Grok marketplace publication package (#1301)** — marketplace catalog entry template + generator, clean-home install/update/uninstall smoke, and bilingual submission/install docs; local-source install remains the deterministic fallback.

### Fixed
- **Stale-active-sessions backlog under opportunistic GC (#1363)** — session GC no longer races the hook soft deadline (detached 30s timeout), runs on session end as well as start, interval tightened to 1h, and `doctor --fix` can apply `session gc`.
- **Host plugin version drift after binary upgrade (#1361)** — clearer per-host refresh hints, post-upgrade checklist docs, Grok install path detection, and Antigravity dual-path incomplete-twin soft-skip when another path already matches.

### Changed
- **Stale extracted candidates decay instead of hard DELETE in store gc (#1368)** — `store gc --target memories` (and `all`) no longer hard-deletes unreviewed `extracted` / `extracted-hidden` / `compact-summary` candidates after the 14-day window. Those rows transition to `status=expired` so they remain restorable until keep-days physical GC.
- **Rejected memories honor store gc keep-days (#1369)** — `store gc --target memories` (and `all`) now physically deletes `status=rejected` rows older than `--keep-days`, same as `expired`/`superseded`. Accepted and candidate rows stay untouched. Dry-run continues to report the candidate count.

## [v0.27.0] - 2026-07-17

### Fixed
- **Hook spool replay (#1342, #1353, #1355)** — timeout-killed hooks leave durable spool records; later hooks and `traceary doctor --fix` drain a bounded oldest-first batch. Session start and subagent start/stop treat already-recorded boundaries as success so partial commits clear the backlog.
- **Memory-extract queue drain (#1343)** — pending extraction jobs are relaunched across sessions (not only the same session key), capped by total attempts, and terminal jobs are GC'd after retention.
- **Stale managed hook generation (#1345)** — doctor compares installed Traceary-managed hook timeouts to the current generation and can refresh via `doctor --fix` (classic Gemini `timeout: 5000` vs packaged `10000` class).

### Changed
- **Hook soft deadline (#1344)** — host-facing hook processes default to an 8s soft deadline (below packaged 10s host budgets; override with `TRACEARY_HOOK_SOFT_DEADLINE`). Detached workers stay signal-only. Doctor reports `store-size` WARN above ~1 GiB.
- **Memory/bundle file splits (#1346)** — behavior-preserving extraction of hotspot files under the 800-line guidance before lifecycle work.

### Notes
- v0.27.0 has no destructive SQLite migration and adds no MCP tools.
- **Deferred to v0.28.0 (explicit on #1341):** #1264 memory inbox decay/restore, #1309 archive-before-GC, #1301 public Grok marketplace. Local Grok install remains supported from a matching release tag.

## [v0.26.1] - 2026-07-16

### Fixed
- **Dotenv false positives** — sensitive-path classification requires a leading `.` (`.env` / `.env.*`); bare `env` and shell “env vars” boilerplate no longer flood `list --sensitive`.
- **Replay timeline bloat** — timeline activity in HTML/Markdown replay uses the same 72-rune bounded projection as `traceary timeline`, so multi-MB prompt/transcript summaries are not embedded raw.

## [v0.26.0] - 2026-07-16

### Added
- **Sensitive-path audit classification (#1257)** — command audits are classified for dotenv / SSH / cloud credential / browser profile / key material intent as a claim separate from secret redaction and host capture coverage. `traceary list --sensitive`, `show --json` `command_audit.sensitive`, and doctor `sensitive-access-audit` surface matches without blocking.
- **Host-reported session model (#1265)** — additive `sessions.model` stores opaque host-provided model strings when present (Claude/Codex hooks). Empty when the host omits model; never fabricated. JSON sessions include `model,omitempty`.
- **Markdown replay (#1262)** — `traceary replay --format markdown` writes a GitHub-renderable digest with truncated, fence-safe plain bodies. Default HTML path is unchanged.
- **Period-scoped retrospective report (#1263)** — `traceary report` aggregates sessions, capture coverage, failures, failure_loops, and top commands over `[from,to)` (default last 7 days). Exit code stays 0 on successful reads.

### Documentation
- **OTel GenAI export decision (#1258)** — documented no-go for network GenAI export while conventions remain experimental; local-first default retained (`docs/research/otel-genai-export.md`).
- **Next-host evaluation (#1256)** — Copilot CLI preferred candidate; opencode secondary watch; no host package in this release (`docs/research/next-host-evaluation.md`).

### Notes
- v0.26.0 has no destructive SQLite migration beyond additive `sessions.model`.
- No MCP tools added. No default network telemetry.

## [v0.25.0] - 2026-07-16

### Fixed
- **Retired `integration` command stubs (#1266)** — the leftover `traceary integration` subtree promised for removal after v0.20.x is gone. Unknown-command behavior is enforced in tests and the integrations verifier.

### Changed
- **Doctor and hook_runtime file splits (#1259)** — oversized `doctor.go` / `hook_runtime.go` hotspots are split along existing responsibilities without intentional behavior change, reducing change amplification for host work.
- **Single-source packaged hook scripts (#1260)** — `scripts/hooks/*.sh` is the canonical source for compatibility wrappers (including prompt, compact, and Grok). Package membership is explicit; `integrations sync-hooks` rewrites copies and `integrations verify` fails on drift.
- **Host coverage matrix source (#1261)** — `application/hostcoverage/matrix.json` is the machine-readable source for the lifecycle × host matrix. Bilingual docs tables are generated/verified from it, and doctor host-capability messages plus event-coverage enrichment expectations load the same embedded matrix.

### Documentation
- **Grok 0.2.101 re-probe (#1299, #1300)** — live re-probes still do not emit standalone `PostToolUseFailure` / `PermissionDenied` / `SessionEnd`, and subagent spawn uses the `spawn_subagent` tool only (no `SubagentStart`/`SubagentStop` parent/child hook contract). Coverage stays versioned in `docs/hooks/host-contract.json` with an explicit unavailable design note.

### Notes
- v0.25.0 has no destructive SQLite migration and adds no MCP tools.
- **Deferred (explicit on #1312):** #1301 public Grok marketplace publication. Local-source install remains supported via `scripts/install-grok-plugin.sh` + `traceary doctor --client grok` from a matching release tag.
- Memory inbox decay/restore (#1264) and archive-before-GC (#1309) remain outside this release (tracked under later milestones).

## [v0.24.0] - 2026-07-16

### Fixed
- **Antigravity empty `workspacePaths` (#1308)** — when `agy` 1.1.x fires hooks with empty `workspacePaths` (common on untrusted/headless runs), Traceary recovers the project workspace from the host process cwd chain instead of the `hooks.json` directory. Events stay visible under the default workspace filter. Doctor also inspects both `~/.gemini/config/plugins/traceary` and `~/.gemini/antigravity-cli/plugins/traceary` for version and MCP registration.
- **Claude print-mode transcripts (#1307)** — `claude -p` Stop can race the JSONL flush so `transcript_path` has no assistant row yet. Transcript capture falls back to `last_assistant_message` on the Stop payload without fabricating replies when that field is empty.
- **Remember skill candidate contract (#1288)** — agent `traceary-memory-remember` skills across Claude/Codex/Gemini/Antigravity/Grok now use `manage_memory action=propose` so explicit remember lands as `status=candidate` (never auto-accepted). Package copies stay identical; `integrations verify` fails if the accepted-status contradiction returns.

### Added
- **AI-safe sessions snapshot profile (#1245)** — `traceary sessions --snapshot --json --profile ai` emits a bounded agent-resume envelope (retrieval hints, counts/hygiene, no large bodies or candidate fact arrays). Default operator snapshot JSON is unchanged.
- **Tool-aware audit compact summaries (#1243)** — on list/snapshot read surfaces, large `Edit`/`Write`/`Read`/shell audit bodies project to path/size/hash/head-tail summaries with a `traceary show` retrieval hint. Persistence and `traceary show` remain full-fidelity.
- **Retry-loop doctor diagnostics (#1244)** — `traceary doctor` includes a read-only `retry-loops` check that clusters recent failed command audits by workspace, agent, command, and error class (EISDIR, missing path, oversized file, sandbox bypass) and reports sample event IDs with preflight hints.

### Notes
- v0.24.0 has no destructive SQLite migration and adds no MCP tools.
- **Deferred (explicit on #1310):** #1264 memory inbox decay/restore and #1309 archive-before-GC remain High-risk multi-PR tracks (later milestones; not shipped in v0.25.0). Automatic archive/GC stays fail-closed / opt-in when that work lands. Do not treat this tag as shipping those features.

## [v0.23.0] - 2026-07-14

### Added
- **Verified Grok Build contract (#1273)** — live, sanitized Grok Build 0.2.99 payloads and versioned fixtures define the supported session, prompt, tool, Stop, and compact fields. Unobserved standalone failure, session-end, and subagent relationships remain explicitly unavailable rather than inferred.
- **Native Grok identity and core runtime (#1274, #1275)** — `grok` is a canonical Traceary host with native hook entrypoints for session, prompt, tool audit, and Stop. Stop transcript capture reads the host-provided `updates.jsonl` path and uses a durable detached retry job when Grok appends the final message after hooks finish.
- **Grok compact lifecycle markers (#1276)** — `PreCompact` and `PostCompact` record distinct phase markers from the verified `source` field. Missing source data is stored as unavailable; no summary body or synthetic subagent relationship is claimed.
- **Native Grok plugin (#1277)** — the repository package contains the exact seven verified hooks, one local Traceary MCP server, and the three shared memory/session skills, with deterministic structural and isolated-install smoke validation.
- **Grok doctor checks (#1278)** — `traceary doctor --client grok` verifies the CLI, plugin enablement and version parity, project-hook trust, the exact installed hook contract, MCP/skill inventory, and recent event coverage without exposing Grok inspect-derived hook target paths or transcript bodies.

### Documentation
- **Install and dogfood guide (#1279)** — bilingual Grok Build documentation covers installation, updates, trust, troubleshooting, the supported coverage matrix, and minimized dogfood evidence.

### Notes
- v0.23.0 has no destructive SQLite migration and adds no MCP tools. The Grok plugin reuses the existing Traceary MCP server. `Stop` is a turn boundary, not a session end. Grok Build 0.2.99 did not live-emit the documented `SessionEnd`, standalone failure, or policy-gated subagent payloads in the verified probes, so Traceary does not claim those capabilities in this release. Follow-ups are tracked in #1299, #1300, and #1301.

## [v0.22.0] - 2026-07-14

### Added
- **Antigravity plugin parity (#1249)** — the packaged Antigravity plugin now includes the Traceary MCP server and memory skills alongside hooks. Doctor verifies MCP registration and packaged/install version alignment, and reports actionable reinstall guidance for stale plugin copies.
- **Current Antigravity capture coverage (#1250)** — Antigravity hooks now correlate current prompt and transcript payloads, record complete turn coverage, and invalidate incomplete turns without discarding legacy transcript data. Doctor reports transcript coverage from bounded metadata rather than exposing transcript bodies.
- **Codex compact and subagent boundaries (#1251)** — the Codex plugin captures `PreCompact`/`PostCompact` boundary markers and correlates subagent stop events with their parent session. Documentation and regression tests define the supported boundary semantics.
- **Codex plugin-hook trust diagnostics (#1252)** — doctor verifies the complete current Codex plugin hook set before reporting trust as healthy. Incomplete or undiscoverable trust state fails closed, and manual fallback hooks are preserved until the full trusted set is confirmed.
- **Activity-aware automatic session GC (#1253)** — hook ingestion runs rate-limited stale-session cleanup using latest activity rather than start time. Cross-process activity leases are published atomically, expire automatically, and protect concurrently active sessions from cleanup races.
- **Durable hook write-path queues (#1269, #1270)** — interrupted hook events are preserved in a durable spool, while per-turn memory extraction runs through a durable asynchronous queue. This keeps host timeout kills and extraction latency out of the synchronous capture path without losing queued work.
- **Duplicate Codex hook remediation (#1271)** — doctor detects duplicate Codex hook registrations and provides lossless, atomic remediation that preserves unrelated hook configuration.

### Fixed
- **Claude SessionEnd cancellation reconciliation (#1254)** — resolved Claude hook cancellation markers are reconciled so doctor surfaces only currently actionable SessionEnd failures.

### Changed
- **Gemini CLI maintenance mode (#1255)** — the Gemini extension remains available for existing installs, but documentation now treats it as maintenance-only after Google's Antigravity CLI transition and directs new installations to the Antigravity plugin.

### Notes
- This release contains no new MCP tools. It adds no destructive data migration; reliability state used by the new queues and cleanup paths is additive and migration-safe.

## [v0.21.4] - 2026-06-20

### Added
- **Antigravity doctor route clarity (#1236)** — `traceary doctor --client antigravity` now reports Antigravity hook installation as independent routes: `antigravity-hooks-workspace`, `antigravity-hooks-user`, and `antigravity-cli-plugin`, plus an aggregate `antigravity-hooks` summary. Missing workspace `.agents/hooks.json` is now `skip` rather than `warn` when a user-level or CLI-plugin route is healthy; doctor warns only when no supported route registers the `traceary` group, and fails when a present route config is malformed enough for Antigravity to reject it.
- **Antigravity capture-level status (#1235)** — doctor now includes `antigravity-capture-levels`, a status-only `pass` check that distinguishes `start_supported`, `tool_audit_supported`, `final_turn_supported`, and `final_turn_unavailable`. This makes the `agy --print` behavior explicit: print mode records session start and `run_command` audits, but the final transcript/turn boundary is unavailable because the host emits no `Stop`/finalization hook in that mode.

### Documentation
- Updated the Antigravity integration, lifecycle, hook README, and hook contract docs with a short `agy --print` capture matrix and clarified that transcripts are captured only when Antigravity emits `Stop` with `transcriptPath`.

This release contains no SQLite schema migration and no new MCP tools.

## [v0.21.3] - 2026-06-20

### Added
- **Reversible historical hook content dedupe (#1227)** — `traceary store dedupe content-events` audits historical hook-originated `prompt`/`transcript` duplicate rows and, with `--apply`, quarantines them into a restore-capable archive instead of hard-deleting. The default is a dry-run that changes nothing; `--restore <run-id>` reverses an apply run (all-or-nothing, refusing to overwrite existing event ids), `--client codex|all` scopes the agent, `--strict` reports every exact duplicate group regardless of time gap, and `--json` is available throughout. Duplicate identity matches the `content-event-reliability` diagnostic (`kind, client, agent, session_id, workspace, source_hook, trimmed body`) — the write-side guard uses exact body, not trimmed; only near-simultaneous groups (10s proximity window, clustering consecutive records pairwise as the diagnostic does) are eligible by default; the canonical row kept per group is the earliest RFC3339Nano `created_at` parsed in Go (tie-broken by event id); malformed-timestamp groups are skipped and reported; command audits are never touched. Apply is transactionally safe and idempotent, and quarantined rows disappear from normal `list`, `sessions --snapshot`, `doctor`, `context`, and MCP read surfaces because they are moved out of `events`. Additive schema migration `000019` adds the `event_content_dedupe_archive` table without moving, deleting, or rewriting any existing `events` row; ordinary upgrade/migration performs no automatic cleanup. The `content-event-reliability` doctor check now points to the new dry-run command and clarifies that current write paths already suppress fresh duplicates. See `docs/storage/README.md` for the design note and rollback path.

### Documentation
- **Antigravity headless `agy --print` capture level (#1225)** — documented that headless `agy --print` runs capture session start (`PreInvocation`) and `run_command` audits (`PreToolUse` + `PostToolUse`) only; print mode emits no `Stop`, so the final transcript/turn boundary is recorded only on interactive runs where the host emits `Stop` with `transcriptPath`. This is the expected capture level, not an install failure — confirmed by dogfooding (Traceary 0.21.2, `agy` 1.0.10). The English/Japanese Antigravity integration docs and the host-coverage matrix now state this precisely, and read-side examples now use `traceary list --agent antigravity` (hook events are recorded with `client=hook`, `agent=antigravity`, so `--client antigravity` returns no rows). Added a regression test pinning the documented print-mode capture level without launching `agy` or reading a real transcript.

## [v0.21.2] - 2026-06-20

### Added
- **Stale Gemini-imported Antigravity CLI plugin detection (#1220)** — `traceary doctor --client antigravity` now runs an `antigravity-cli-plugin` check that reads only the Antigravity CLI plugin config files (`plugin.json`, `hooks.json`, `hooks/hooks.json`) under `~/.gemini/antigravity-cli/plugins/traceary`. It warns when legacy Gemini-shaped plugin files remain after migrating to the supported Antigravity hook runtime, with remediation steps, passes on the supported top-level `traceary` hook group, and skips absent CLI plugin directories so `traceary hooks install --client antigravity` users are not failed.
- **Docs** — the English/Japanese Antigravity integration docs now document the stale Gemini-imported CLI plugin migration path.

This release contains no SQLite schema migration and no new MCP tools.

## [v0.21.1] - 2026-06-20

### Added
- **First-class Antigravity hook and plugin support (#1213)** — Antigravity is now a real Traceary hook client instead of diagnostics-only. `traceary hooks print/install --client antigravity` renders and merges Antigravity's hook-group `hooks.json` shape (workspace `.agents/hooks.json` or user-level `~/.gemini/config/hooks.json`), preserving every non-`traceary` hook group, and the repository ships a packaged `integrations/antigravity-plugin/` with the same `traceary` hook group plus a `plugin.json` manifest following the official Antigravity plugin schema.
- **Antigravity event coverage** — `PreToolUse`/`PostToolUse` are paired across `stepIdx` to record `run_command` audits, `PreInvocation` drives an idempotent session start/refresh keyed by `conversationId`, and `Stop` records the turn transcript and a turn boundary without closing the session.
- **Antigravity doctor checks** — `traceary doctor --client antigravity` reports `antigravity-capability` (CLI/app-bundle detection, no credential reads or browser automation) and `antigravity-config` (whether the resolved `hooks.json` registers the `traceary` group, with an `--upgrade` remediation).
- **Docs** — the integration, hooks, lifecycle, and release docs now describe Antigravity as a supported host, including the `PreToolUse`/`PostToolUse` pairing, the `Stop` transcript/turn boundary, and the limitations of the public hook surface.

This release contains no SQLite schema migration and no new MCP tools.

## [v0.21.0] - 2026-06-20

### Added
- **Automation-friendly doctor warnings (#1175)** — `traceary doctor` now accepts `--warnings-ok`, letting CI and smoke checks treat warning-only reports as exit code `0` while failures still exit `1` and JSON reports keep the warning summary and per-check severities.
- **Bounded command-audit payloads (#1173)** — command-audit input/output payloads now preserve head/tail context when truncated before persistence, expose original byte metadata in CLI/MCP write results, and can be tuned through `audit.max_input_bytes` / `audit.max_output_bytes` config defaults.
- **Sessions snapshot late-event status (#1172)** — `traceary sessions --snapshot` / `--json` now correctly reflect late-arriving events that land after a session's nominal end, and active-session lookup resolves reliably after GC.
- **Duplicate hook write suppression (#1167)** — the hook pipeline deduplicates prompt and transcript writes within the retry window, preventing redundant rows from repeated hook firings.
- **Memory candidate hygiene (#1169)** — `traceary sessions --snapshot --json` (via `reliability.memory.candidate_hygiene`) now reports hygiene counts, and extraction automatically keeps obvious code and diff fragments out of the review queue so operators see only meaningful candidates.
- **Claude event coverage diagnostics (#1174)** — `traceary doctor` detects Claude event coverage gaps and surfaces missed hook cancellation events as a dedicated diagnostic.
- **Gemini coverage warnings (#1171)** — `traceary doctor` warns when Gemini session data shows boundary-only or audit-only coverage, signaling potential instrumentation gaps.
- **Gemini compact instrumentation docs and doctor check (#1176)** — `traceary doctor` verifies Gemini compact event instrumentation and the docs describe how to confirm compact coverage in practice.
- **Antigravity capability detection (#1195)** — `traceary doctor --client antigravity --json` probes for Antigravity installation and reports one of four states: `not_installed` (no app bundle or CLI found), `tool_unavailable` (app/CLI present but no supported public headless/hook/package surface confirmed), `not_authenticated` (future/reserved: installed with a supported surface but not authenticated or configured — detected via a supported CLI/contract check, not by reading credentials), or `available` (future). On the local development machine, `/Applications/Antigravity.app` (version 2.1.4) is installed with no confirmed CLI/hook contract, so the current state is `tool_unavailable`. Antigravity is not included in the default doctor client list (`["claude","codex","gemini"]`); pass `--client antigravity` explicitly.

### Fixed
- **Audit-reliability duplicate diagnostics (#1168)** — the audit-reliability doctor check no longer emits duplicate diagnostic entries for the same candidate group.
- **Claude hook diagnostic cleanup matching (#1174)** — tightened cleanup matching so cancelled Claude hook invocations are captured rather than silently dropped.
- **SQLite timestamp comparisons (#1185)** — normalized timestamp comparisons in SQLite queries to eliminate boundary mismatches caused by fractional-second format differences.

### Changed
- **Codex Stop is a turn boundary (#1170)** — docs and diagnostics now correctly describe `Codex Stop` as a turn boundary event rather than a session end, matching actual Codex host behavior.
- **Clean My Agent compatibility design note (#1177)** — added a design note documenting Traceary's compatibility posture with the Clean My Agent protocol.
- **Gemini CLI marked legacy; Antigravity migration status added (#1197)** — README, integration guides, and the Gemini extension docs now state that Gemini CLI is the legacy Google AI agent host and Antigravity is the active successor. A new [Antigravity migration status page](./docs/integrations/antigravity.md) documents local discovery (app path, bundle ID, version, URL scheme, user data directory) and makes clear that no supported public CLI/hook contract is confirmed in v0.21.0; Antigravity capability detection is implemented in #1195. Per #1196, v0.21.0 intentionally ships no Antigravity hook, package, generated metadata, or release asset because #1195 confirmed no supported public CLI/hook contract exists; `doctor` reports Antigravity as `tool_unavailable` rather than shipping a fake package. The Gemini CLI extension package continues to be available for existing installs.

### Notes
- v0.21.0 has no SQLite schema migration and no new MCP tools. It is a dogfood-reliability and observability release focused on hook diagnostics, memory hygiene, audit correctness, and session snapshot fidelity.
- v0.21.0 dogfood verification was conducted in a live Traceary workspace before release (#1178).

## [v0.20.1] - 2026-05-31

### Added
- **Doctor hook integrity warnings (#1152)** — `traceary doctor` now detects duplicate Traceary-managed hook registrations in host config files, surfaces guided dry-run-first remediation, and the integration verifier rejects packaged hook assets that accidentally ship duplicate managed entries.
- **Audit reliability dogfood signal (#1153)** — `traceary doctor` adds a bounded `audit-reliability` check that reports duplicate command-audit candidate groups and workspace-drift candidates with counts and sampled event IDs only, without dumping command input/output bodies.

### Changed
- **MCP search semantics clarified (#1155)** — the MCP search tool schema and docs now spell out that `query` is a literal full-text search string; boolean-looking text such as `OR` is not an any-match operator.

### Fixed
- **Command-audit workspace attribution and duplicate rows (#1149)** — Codex command audits now prefer explicit workspace/cwd evidence over stale hook session state, and SQLite ingestion suppresses near-identical duplicate command-audit rows in the hook retry window.
- **Piped JSON and snapshot broken-pipe exits (#1154)** — CLI JSON/snapshot commands now treat downstream closed pipes as a normal Unix pipeline condition instead of surfacing noisy broken-pipe errors.
- **Traceary self-inspection audit noise (#1150)** — hook audit ingestion now skips Traceary read-only self-inspection commands, Traceary MCP read tools, and explicit `TRACEARY_NO_AUDIT` opt-outs while preserving normal generic tool audits.

### Notes
- v0.20.1 has no SQLite schema migration and no new MCP tools. It is a patch release focused on audit correctness, hook diagnostics, and dogfood signal quality.

## [v0.20.0] - 2026-05-29

### Added
- **First-class tool-failure capture (#1116, #1117)** — `list --failures` works again. No host exposes a numeric exit code in its post-tool hook payload, so Traceary now records a structural `failed` flag instead: Claude's `PostToolUseFailure` (top-level `error`) and Gemini's spawn-level `tool_response.error` are flagged, while Codex exposes no structured failure signal and is recorded as an unflagged audit. The hook contract docs were corrected to describe what the hosts actually expose.
- **Cockpit memory inbox backlog (#1115)** — the cockpit Memory tab surfaces inbox curation debt (accepted / candidate / new-since-last-review counts) via a cheap `CountByStatus` query — no expensive reliability scan and no `List`. Memory cues stay in the Memory tab; the session-focused Top/Sessions surfaces remain session-only.
- **memory inbox cleanup composition summary (#1114)** — `memory inbox cleanup` reports an aggregate `total / by_source / by_type` breakdown of the matched candidates so an operator sees the batch makeup before `--apply`. It stays reject/preview-only; bulk accept is intentionally excluded to respect the evidence-first review rails.

### Changed
- **True candidate/accepted counts when the reliability scan saturates (#1111)** — the reliability surface reports exact accepted/candidate totals from `CountByStatus` instead of capping at the bounded scan limit.
- **Tighter extraction noise gate (#1113)** — auto-extraction hides review fix-instruction fragments (`修正案:` / `Fix:` / `**Fix**:`) from the inbox, guarded so a line that carries a durable constraint is not hidden.
- **Repository tooling migrated to Go (#1118, #1119, #1120, #1121, #1122)** — maintainer-only repository helpers moved from Python to a single `go run ./cmd/repo-tooling ...` entrypoint: `integrations verify`, `docs verify-i18n`, `release verify-changelog`, `docs verify-landing`, and `release bump-version` replace the four `scripts/verify_*.py` verifiers and `scripts/bump_version.py`, completing the documented repo-tooling migration order. CI, the Makefile, the release workflow, and CONTRIBUTING are wired to the Go commands; the remaining Python scripts (e.g. `verify_release_manifests.py`) are outside that migration plan.
- **Honest MCP / handoff contract framing (#1106, #1107)** — the cross-machine handoff doc is truthed to the shipped five-table bundle, and the MCP 8-tool surface is described as the current frozen contract.
- **Stricter command groups (#1142)** — command groups (`memory`, `store`, `session`, and their sub-namespaces) now reject an unknown subcommand with a usage error and a non-zero exit instead of silently printing help and exiting 0. A bare group invocation (e.g. `traceary memory`) still prints help; scripts that relied on a typo'd subcommand exiting 0 will now fail loudly.

### Removed
- **CLI cleanup (#1108, #1109, #1110)** — removed the unreferenced `session-top` alias, retired the v0.14 / v0.15 migration-error command stubs, and deprecated (hid) the empty integration command subtree.

### Notes
- Schema: additive migration `000017` adds `command_audits.failed` (`NOT NULL DEFAULT 0`); existing rows default to `0`. No MCP tool changes. The `command_executed` JSON output gains an additive, `omitempty` `failed` field that also round-trips through bundle export/import.
- Tests: the CLI test package pins `TRACEARY_LANG=en` via `TestMain` so golden snapshots stay locale-hermetic (#1105).

## [v0.19.0] - 2026-05-26

### Added
- **Tail-first cockpit shell (#1053, #1054)** — `traceary tui` now opens on the Live/Tail stream instead of a Home triage board, with Claude/Codex-like tab navigation, row movement, Enter/Esc drill-in/back behavior, and live auto-follow semantics for recent Traceary events.
- **Dedicated Sessions tab and editable Settings (#1055, #1056, #1083)** — the cockpit separates Tail and Sessions into distinct tabs, and Settings now supports arrow-key form controls for safe language/read/redaction configuration updates without leaving the TUI.
- **Evidence-first Memory review hardening (#1057, #1066)** — Memory review now blocks evidence-less accept/edit-distill decisions until supporting refs are attached, adds `traceary memory inbox attach <id> --evidence kind:value [--artifact kind:value]`, and lets the interactive review TUI queue `r` attach actions before accept/edit-distill.
- **Japanese TUI copy refresh (#1058)** — refreshed Japanese cockpit and Memory-review copy while keeping literal command names copyable in English.
- **Versioned host-native memory activation files (#1050)** — the repository now versions Traceary-managed Claude/Gemini memory import stubs and generated project memory projections so dogfood activation state is shared with future agents.
- **Primary Sessions dashboard command (#1083)** — added `traceary sessions` as the operator-facing dashboard command with the same filters, live TUI, `--snapshot`, and `--json` behavior as the permanent compatibility `traceary top` alias.
- **Evidence-rich Memory inbox inspection (#1091)** — added `traceary memory inbox show <memory-id>` and expanded `memory inbox list` with `CONFIDENCE` / `REVIEW` columns so operators can inspect source context, evidence refs, artifact refs, guidance, and accept-readiness before deciding on a candidate.

### Changed
- **Bare CLI opens the Tail-first TUI in interactive terminals (#1060)** — running `traceary` with no subcommand now opens the same Tail-first cockpit as `traceary tui` when stdin/stdout are TTY-backed. Non-interactive callers keep deterministic help/fallback output and should prefer explicit script-friendly commands such as `traceary list`, `traceary sessions --snapshot [--json]`, and `traceary doctor --json`; `traceary top --snapshot [--json]` remains available as a permanent compatibility alias.
- **Cockpit review invariants codified (#1059)** — historical Codex review findings around the cockpit are now pinned as regression coverage, including weak-memory accept confirmation, 80x24 dogfood smoke, Japanese narrow snapshots, and Settings write safety.
- **Latest-session schema test alignment (#1068)** — latest-session tests now use the same production migration-backed schema path as the runtime database.
- **Cockpit memory-review glossary tightened (#1070)** — the TUI, CLI help, `top --snapshot` empty state, and related memory review error messages now consistently call the candidate queue the “memory review queue” in English and `メモリ候補の確認キュー` in Japanese, while keeping literal `traceary memory inbox ...` command paths, `candidate(inbox)` metrics, and existing `top --snapshot` section headers copyable for scripts.
- **Sessions text snapshots include operator names (#1083)** — `traceary sessions --snapshot` and compatibility `traceary top --snapshot` text rows now include `name="..."` before raw `workspace=` / `agent=` metadata. The JSON snapshot schema remains unchanged; scripts that consume text should parse `key=value` fields instead of relying on positional columns.
- **Cockpit Tail and Sessions surfaces simplified (#1082, #1083, #1092)** — Tail is now a read-only, terminal-height stream with scroll/follow controls and no detail drill-down, while the former Top surface is promoted to a session-only Sessions tab. Cockpit navigation is now `Tail | Sessions | Memory | Settings`; memory candidates, stale-memory cleanup, and reliability scans stay in the Memory tab or standalone `traceary sessions` / `traceary top` compatibility surfaces.
- **Cockpit Settings navigation matches global tab behavior (#1081)** — left/right and tab/shift-tab move between cockpit tabs from Settings instead of mutating the highlighted setting; setting changes are staged with Enter or direct value shortcuts.

### Fixed
- **CLI session-end attribution inheritance (#1078)** — when ending an existing session, `traceary session end` preserves omitted client, agent, and workspace attribution inherited from the matching session start instead of rewriting the end event to default `cli` / `manual` attribution.
- **Cockpit Tail rendering parity (#1089)** — cockpit Tail rows now share the compact `traceary tail` color semantics while hard-capping row width before ANSI wrapping, preventing truncated/wrapped lines from corrupting the TUI viewport.
- **Sessions dashboard scalability (#1090)** — the Sessions surface keeps large session sets usable by sorting dashboard rows by active/recent priority without mutating snapshot output, keeping identity-first rows readable, and caching subtree priorities for the cockpit view.
- **Cockpit Tail truncation marker cleanup (#1097)** — cockpit Tail rows still compact oversized payloads before rendering, but no longer append a literal `[truncated]` suffix that consumed horizontal space in the live stream.
- **Sessions inline detail context (#1098)** — opening a Sessions detail now keeps the Sessions summary, selected-row context, and dashboard visible in the same tab, with height budgeting for long details on larger terminals.

### Notes
- v0.19.0 has no SQLite schema migration and no new MCP tool additions.
- `Formula/traceary.rb` is still generated by the tagged release workflow; it is not edited in the release-preparation PR.

## [v0.18.0] - 2026-05-24

### Added
- **Reference-driven cockpit UX baseline (#1035)** — added bilingual design documentation that audits the v0.17 cockpit against mature terminal UI patterns, defines the v0.18 information architecture, and records the global navigation, state-feedback, and release acceptance rules for the operator console.
- **Persistent cockpit navigation shell (#1040)** — `traceary tui` now has numbered global sections for Home, Live, Doctor, Memory, Sessions, and Settings, with visible section chrome, tab / shift-tab navigation, and `Esc` as an in-cockpit back action while `q` / Ctrl-C remain quit shortcuts.
- **Discoverable contextual actions (#1041)** — the `?` help overlay now renders a contextual action menu for the current cockpit screen, hiding unavailable shortcuts and surfacing the valid next actions for Live details, Doctor remediation, Memory review, Sessions, and Settings.
- **Actionable Home triage board (#1043)** — the cockpit Home screen now prioritizes operator attention cards for problems, new activity, memory inbox state, active sessions, recent failures, and health, with explicit next-action targets instead of raw count dumps.
- **Cockpit dogfood snapshots and release checklist (#1044)** — added golden snapshots, keyboard-path tests, terminal-size smoke coverage, and bilingual manual dogfood checklists so cockpit regressions are caught before release.
- **Japanese cockpit UI (#1045)** — localized cockpit navigation, Home cards, footer/help copy, Doctor/Live/Sessions surfaces, and Memory review decision guidance while keeping command names copyable in English.
- **Cockpit Settings section (#1046)** — added a `6 Settings` cockpit section that shows config path/status, environment override diagnostics, read-only preset/rule summaries, and safe staged updates for `ui.language`, `read.color`, `read.fields`, and validated `redact.extra_patterns` writes.

### Changed
- **Memory review decisions are evidence-first (#1042)** — the cockpit Memory review flow now shows source, confidence, quality signals, evidence/artifact counts, candidate age, duplicate/supersede hints, and an accept-as-is checklist before accepting candidate memories. Low-confidence and `extracted-hidden` candidates require an extra accept confirmation.
- **CLI language precedence is configurable (#1046)** — operator-facing CLI/TUI language selection now follows `TRACEARY_LANG` > config `ui.language` > built-in English, and saving Settings refreshes the current cockpit language when no environment override is set.

### Notes
- v0.18.0 has no SQLite schema migration and no new MCP tools.
- Existing script-friendly commands such as `traceary top`, `traceary tail`, `traceary doctor`, `traceary session handoff`, and `traceary memory inbox review` remain available; the cockpit is the discoverable operator console layered on top of those surfaces.
- Settings writes are conservative: invalid or unreadable config JSON is not overwritten, regex additions are validated before staging, and writes use atomic replacement.

## [v0.17.1] - 2026-05-23

### Fixed
- **Release-bot Homebrew autolabel hardening (#1021)** — the PR-side `autolabel` job now skips release-generated Homebrew formula branches (`maintenance/homebrew-v*`) and documents why those PRs are not release-note classification inputs. The normal autolabel path keeps running for human, Dependabot, and feature branches with explicit `issues: write` permission, and CI now verifies the release-drafter workflow guard.

### Notes
- v0.17.1 is a patch release for release automation only. It has no CLI behavior changes, no SQLite schema migration, and no new MCP tools.

## [v0.17.0] - 2026-05-23

### Added
- **Operator cockpit entrypoint (#990)** — `traceary tui` now opens an explicit TTY-only operator cockpit instead of requiring operators to remember separate `top`, `tail`, `doctor`, `session handoff`, and `memory inbox review` commands. Non-interactive callers keep deterministic script-friendly commands and receive fallback guidance when they try to start the cockpit without a TTY.
- **Cockpit home status model (#991)** — the cockpit home screen summarizes active sessions, recent failures, recent commands, candidate memories, accepted-memory counts, stale-session signals, large-payload counts, and doctor status so the operator starts from one triage surface.
- **Live tail inside the cockpit (#992)** — the cockpit can switch into a live event-tail pane with follow/refresh behavior and event-detail drill-down, reusing the existing event formatting and query paths while keeping `traceary tail` available for scripts and dedicated terminal sessions.
- **Memory inbox notifications and review flow (#993, #994)** — the cockpit surfaces candidate-memory counts, remember-intent priority, new-candidate warnings, and a direct memory-review flow so operators can accept, reject, skip, edit/distill, or inspect evidence without leaving the cockpit.
- **Doctor warnings pane (#995)** — `traceary tui` can open doctor details from the cockpit, grouping failures and warnings from the existing doctor report so hook/MCP/configuration problems are visible beside live work.
- **Persistent cockpit last-seen state (#996)** — the cockpit stores local last-seen timestamps under the user state directory, tracks event boundary IDs to avoid duplicate or missed new-event counts, and exposes `traceary tui --reset-state` for operators who want to clear local notification state.

### Changed
- **Explicit cockpit entrypoint documented as the stable path (#997)** — README, CLI reference, and interactive docs now explain that `traceary tui` is the supported operator entrypoint for v0.17.0 while bare `traceary` remains unchanged and continues to show help/usage instead of auto-opening the TUI.

### Notes
- v0.17.0 has no SQLite schema migration and no new MCP tools.
- The cockpit state file is local operator state, not shared project data; deleting it only resets cockpit notification checkpoints.
- Existing `traceary top`, `traceary tail`, `traceary doctor`, `traceary session handoff`, and `traceary memory inbox review` commands remain available for direct use and automation.

## [v0.16.0] - 2026-05-23

### Added
- **Stale active-session safeguards before host context retrieval (#982)** — host-facing context paths now identify active sessions that have aged past the stale threshold and surface cleanup guidance so abandoned sessions do not silently shadow the current working context.
- **Memory inbox backlog controls (#986)** — `traceary memory inbox list` gains age and quality filters, and `traceary memory inbox cleanup` provides a dry-run-first hygiene path for rejecting old or low-quality candidate memories without touching accepted memories.
- **Remember-intent promotion visibility (#987)** — memory inbox and top snapshot surfaces now expose remember-intent candidate counts and source filtering so explicit “remember this” prompts are easier to review and promote.
- **Dogfood reliability metrics in `traceary top --snapshot` (#988)** — text snapshots and JSON snapshots gain an additive reliability section covering stale active sessions, accepted/candidate memory counts, candidate age, and large-payload counts for recent command/failure panes.

### Changed
- **Workspace resolution is shared across sessions, events, and memories (#983)** — workspace-scoped handoff and context loading now use exact-match-first parent/child fallback, with user-visible hints when a child workspace request matched a parent-scoped session through event evidence.
- **Large command payloads are capped on context surfaces (#984)** — recent-command and recent-failure payloads in top snapshots, handoff context, and MCP session context use a shared truncation policy with metadata so noisy command output does not dominate host-agent context windows.
- **Accepted memories are the default durable-memory context (#985)** — host context now prefers accepted memories by default and reports candidate memories separately, reducing the chance that unreviewed extraction output is treated as trusted long-term context.

### Notes
- v0.16.0 has no SQLite schema migration and no new MCP tools.
- The `traceary top --snapshot --json` `reliability` key is additive; existing consumers can continue reading `sessions`, `failures`, `recent_commands`, `candidates`, and `stale_memories`.
- Candidate memories remain review-first: use `traceary memory inbox review` for interactive curation or `traceary memory inbox cleanup --dry-run` before bulk rejection.

## [v0.15.0] - 2026-05-10

### Added
- **Stale memory data in `traceary top --snapshot --json` (#959)** — the top data loader now fetches stale durable memories alongside sessions, recent failures, recent commands, and candidate memories. The JSON snapshot envelope gains a new additive `stale_memories` (`{ count, items }`) key whose rows reuse durable-memory summary fields plus a `reason`.
- **Stale memory pane in `traceary top` (#960)** — the live dashboard and text snapshot now include a fifth pane / section for stale durable memories so memory-hygiene work is visible in the daily-read surface without adding a new command.
- **Per-pane `/` search in `traceary top` (#961)** — the focused dashboard pane now supports incremental filtering with `/`; Enter keeps the filter, `/` reopens it for editing, and Esc clears the active filter without quitting.
- **Enter-to-detail drill-down in `traceary top` (#962)** — highlighted session, event, candidate-memory, and stale-memory rows can open a scrollable detail modal. Session details include lineage plus recent events; event and memory details reuse the existing CLI detail renderers.

### Changed
- **Release-drafter Dependabot intake (#953)** — updated the pinned `release-drafter/release-drafter` and autolabeler action baseline from 7.2.1 to 7.3.0 before release preparation.
- **Go dependency Dependabot intake (#954)** — updated the Go dependency group, including `github.com/charmbracelet/bubbles` 1.0.0, `github.com/anthropics/anthropic-sdk-go` 1.41.0, and the `golang.org/x/*` refresh needed before the TUI-heavy work.
- **Removed-alias documentation verifier covers v0.15 (#958)** — `scripts/verify_docs_no_removed_aliases.py` now rejects docs that recommend the v0.15-removed flat memory verbs or `integration codex uninstall` outside the historical migration allow-list.
- **`memory inbox review` reports per-id failures as command failures (#963)** — queued decisions still run to completion and stdout keeps the exact `FAILED` rows, but a partial review now returns a non-zero error so shell automation cannot treat it as success.
- **README, CLI reference, stability policy, and changelog synced for v0.15 (#964)** — the docs now describe top search / detail / stale-memory panes, the `stale_memories` JSON envelope key, v0.15 alias removals, and the completed Dependabot intake.

### Removed
- **Hidden flat memory aliases (#956)** — removed the v0.14 hidden deprecated aliases for `memory accept`, `memory reject`, `memory remember`, `memory propose`, `memory distill`, `memory extract`, `memory import codex`, `memory import instructions`, `memory export`, `memory activate`, `memory hygiene scan`, `memory hygiene apply`, `memory graph add`, `memory graph list`, `memory supersede`, `memory expire`, and `memory set-validity`. Use the canonical `memory inbox`, `memory store`, and `memory admin` paths.
- **Cleanup-only `traceary integration codex uninstall` (#957)** — removed the hidden cleanup command after the v0.14 migration window. Codex plugin install / uninstall is handled by Codex CLI's official `/plugins` flow; the Codex plugin guide keeps manual cleanup steps for legacy installs.

### Notes
- v0.15.0 has no SQLite schema migration and no new MCP tools.
- The `traceary top --snapshot --json` `stale_memories` key is additive; existing consumers that already read the v0.14 envelope can continue reading `sessions`, `failures`, `recent_commands`, and `candidates`.
- The v0.14 deprecation window for flat memory verbs and the Codex uninstall cleanup path is complete; new scripts should use only the canonical grouped paths.

## [v0.14.0] - 2026-05-07

### Added
- **Shared Bubble Tea TUI foundation under `presentation/cli/tui` (#924)** — introduced a small, opinionated package that wraps Bubble Tea, Bubbles, and Lip Gloss for Traceary's interactive surfaces. The package ships a Lip Gloss palette, a shared key map (movement, paging, select, refresh, help, quit with Ctrl-C and Esc bound to quit), TTY guard helpers, and a `Run` entry point that refuses to start without a TTY and delegates terminal restoration plus signal handling to Bubble Tea. The upcoming `memory inbox review` UI and the redesigned `top` dashboard will both build on this foundation. No user-visible command behavior changes in this release; existing `traceary top` keeps using its current renderer.
- **Interactive `traceary memory inbox review` (#925)** — added a TTY-only walk-through over the candidate durable-memory inbox built on the shared Bubble Tea foundation (#924). The command accepts the same filter set as `memory inbox list` (`--workspace`, `--agent`, `--session-family`, `--type`, `--source`, `--include-hidden`, `--limit`) so reviewers can pivot between the snapshot view and the interactive walk without re-tuning flags. Accept and reject decisions reuse the existing `MemoryUsecase.Accept` / `Reject` use cases (same dedupe, same status transitions); edit/distill requires the operator to type a new fact and routes through `memory store distill --replace=supersede` so the candidate's LLM-authored text is never auto-accepted. Without a TTY the command refuses to start and exits with code `2`, printing fallback guidance pointing at `memory inbox list` plus `memory inbox accept|reject` for batch / scripted callers.

### Changed
- **`traceary top --snapshot` extended for the redesigned dashboard (#929)** — the snapshot routes (`traceary top --snapshot` and `traceary top --snapshot --json`) now mirror the four-pane dashboard introduced in #928 so non-interactive consumers (pipes, CI, scripts) see the same data the live view shows. The text output gains explicit `ACTIVE SESSIONS`, `RECENT FAILURES`, `RECENT COMMANDS`, and `CANDIDATE MEMORIES (count=N)` sections; the active session tree is unchanged inside its section. The JSON contract is wrapped in a stable envelope with `sessions`, `failures`, `recent_commands`, and `candidates` (`{ count, items }`) keys. The session node fields are unchanged from earlier releases, but the top-level shape moved from a bare array of session nodes to this envelope object — scripts that consumed the array directly need to read `sessions` from the envelope. Empty panes serialize as `[]` / `count=0` so consumers can rely on every key being present. Per-pane row caps follow the dashboard defaults (50 failures, 50 recent commands, 25 candidates); the session pane keeps using `--limit`.
- **`traceary top` migrated to a shared TUI multi-pane dashboard (#928)** — the live `traceary top` view is now a Bubble Tea model built on the shared `presentation/cli/tui` foundation (#924) and is rendered as four panes: active sessions, recent failures, recent command_executed events, and durable-memory inbox candidates. Tab / shift+tab cycle pane focus, ↑/↓ (and pgup/pgdn / g / G) scroll the focused pane independently, `r` forces a snapshot refresh, `?` toggles a help overlay, and `q` / Ctrl-C / Esc quit cleanly via the shared safety net. The dashboard reuses the `topDataLoader` (#927) so panes share filter and lineage handling with `--snapshot` / `--snapshot --json`; the snapshot output and command name are unchanged. Non-TTY callers still receive the snapshot text writer. Narrow terminals fall back to a one-row-per-pane viewport so the dashboard stays usable when the window is small.
- **`traceary top` data fetching extracted into a testable loader (#927)** — the active session tree fetch and lineage expansion previously inlined on `RootCLI` now live behind `topDataLoader`, a small seam in `presentation/cli/top_data.go` that returns sessions, failures, recent commands, and candidate memories from a single `loadSnapshot` entry point. The cobra command keeps its current text / JSON snapshot output and only routes its session fetch through the loader; the failures / recent-commands / candidates panes are wired but stay dormant until #928 adds the multi-pane dashboard. Internal tests cover each loader method with deterministic fixtures so the new boundary stays observable as the dashboard grows.
- **`traceary-memory-review` skill points at the interactive CLI as the preferred human fallback (#926)** — the packaged `traceary-memory-review` skill for Claude Code, Gemini CLI, and Codex now names `traceary memory inbox review` (added in #925) as the preferred human fallback when the operator wants to walk the durable-memory inbox themselves at the terminal, and explicitly distinguishes that interactive flow from the script/batch path (`memory inbox list` + `memory inbox accept|reject`). The skill also tightens its no-auto-accept guardrail so the same rule applies to both MCP `manage_memory(action="accept")` calls and the CLI batch commands: never accept candidate memories without an explicit per-id operator decision. Skill version bumped to `1.1.0`; trigger phrases are unchanged.
- **`memory inbox accept` / `reject` accept a positional id and `--id-only` (#923)** — `traceary memory inbox accept <id>` and `traceary memory inbox reject <id>` now work for the common single-id case so the canonical inbox path matches what operators type interactively. The two canonical commands also gain `--id-only`, mutually exclusive with `--json`, so the canonical surface is a strict superset of the old flat `memory accept <id> --id-only` / `memory reject <id> --id-only` form: a single successful row prints just the memory id on stdout, batches print one id per successful row, and per-id failures surface on stderr with a non-zero exit. The existing `--ids id1,id2,...` form keeps working for batch scripts and MCP callers, and dedupe / failure semantics are unchanged when `--id-only` is not set. The hidden deprecated aliases `memory accept` / `memory reject` now point at the positional canonical form in their stderr deprecation notice.
- **Memory CLI grouped into `inbox` / `store` / `admin` namespaces (#922)** — `traceary memory --help` now advertises the grouped surface (`memory inbox`, `memory store`, `memory admin`) on top of the daily-read commands (`memory search`, `memory show`, `memory list`). The flat implementation verbs (`memory remember`, `memory propose`, `memory distill`, `memory extract`, `memory accept`, `memory reject`, `memory supersede`, `memory expire`, `memory set-validity`, `memory import`, `memory export`, `memory activate`, `memory hygiene`, `memory graph`) keep working as hidden deprecated aliases that emit a single-line stderr deprecation notice naming the canonical replacement and the v0.15 removal target. JSON / stdout output is unchanged so scripted callers and AI integrations keep working through one release of overlap.

### Docs
- **Memory command surface plan for v0.14 (#921)** — added `docs/operations/memory-command-surface.md` (and the Japanese paired doc) auditing every current `traceary memory ...` path and mapping it to the v0.14 target tree. The new tree groups subcommands under `memory inbox`, `memory store`, and `memory admin` while keeping `memory search`, `memory show`, and `memory list` at the top level for daily use. The doc lists every old path that will become a hidden deprecated alias in v0.14 and is scheduled for removal in v0.15.
- **CLI stability and deprecation policy (#931)** — added `docs/cli-stability.md` and `docs/cli-stability.ja.md` documenting the public / admin / plumbing tier classification for the v0.14 command surface, the stderr deprecation notice format (with stdout / `--json` / NDJSON byte-for-byte compatibility for the deprecation window), the one-minor compatibility window, and the v0 vs v1 removal policy. The README and the docs index now link to the policy alongside the CLI reference.
- **README, CLI reference, and operator docs synced for v0.14 (#932)** — the README pair now documents the new memory namespace (`memory inbox` / `memory store` / `memory admin`) and the interactive `memory inbox review` walk-through alongside the daily-read commands. `docs/cli/README.md` and `docs/cli/README.ja.md` reorganize the durable-memory section under the v0.14 namespaces, document the TTY-only `memory inbox review` action keys and exit-code-`2` non-TTY fallback, expand the `traceary top` multi-pane dashboard reference, and rewrite the migration banners for the removed top-level aliases (`init`, `backup`, `gc`, `handoff`, `compact-summary`) and the hidden Codex `integration codex uninstall` cleanup path. Storage / backup / operations / memory / interactive guides all use the canonical `traceary store ...` and `traceary session handoff` paths instead of the retired aliases. The new `scripts/verify_docs_no_removed_aliases.py` (wired into CI) flags any future doc that recommends a removed v0.14 alias outside the migration tables.

### Removed
- **Deprecated top-level command aliases (#918)** — `traceary init`, `traceary backup`, `traceary gc`, `traceary handoff`, and `traceary compact-summary` are no longer registered as working commands and have been dropped from `traceary --help`. Invoking the old names now exits with a usage error that points at the canonical replacement: `traceary store init`, `traceary store backup ...`, `traceary store gc`, `traceary session handoff`, and `traceary session handoff --compact-only`. The aliases have shipped a deprecation notice since v0.9 (see #696); v0.14.0 completes the planned removal.
- **Deprecated `traceary-memory-capture` skill stubs removed (#919)** — the placeholder `traceary-memory-capture` skill is removed from the Claude Code plugin, Gemini CLI extension, and Codex plugin packages. Use `traceary-memory-review` for inbox curation and session recaps, and `traceary-memory-remember` for explicit durable-memory writes.
- **Legacy `traceary integration codex install` retired (#920)** — the legacy install path is no longer a working command. Invoking it now exits with a usage error that names the v0.14.0 removal and points at Codex's official `/plugins` flow as the supported install path. `traceary integration codex uninstall` is kept as a hidden cleanup-only command (absent from `traceary integration codex --help`) for users migrating off the retired install path; it is scheduled for removal in v0.15.

## [v0.13.1] - 2026-05-04

### Changed
- **Compact read rows use interactive terminal width (#908, #912, #914)** — `traceary list`, `traceary search`, and `traceary tail` compact text output now use the detected TTY width instead of the legacy fixed 100-column cap when writing to an interactive terminal. Pipe, redirection, and non-TTY output keep the prior 100-column fallback so scripted output stays stable; `--wide` and `--json` remain outside the compact formatter contract. Dogfood coverage exercised all three commands in both TTY and pipe paths, and regression tests now pin the formatter budget behavior.
- **Dependency maintenance for v0.13.1 (#879, #910)** — updated `github.com/mattn/go-runewidth` to 0.0.23, `github.com/modelcontextprotocol/go-sdk` to 1.6.0, `github.com/pelletier/go-toml/v2` to 2.3.1, and `modernc.org/sqlite` to 1.50.0. Review triage confirmed Traceary does not use the MCP SDK `SetError` or Streamable HTTP cross-origin paths affected by upstream behavior changes, and the SQLite-backed store tests pass on the new driver.
- **Release-drafter action pin refresh (#878, #911)** — updated the pinned `release-drafter/release-drafter` and autolabeler action SHA to the upstream `v7.2.1` commit while keeping full-SHA pinning for supply-chain hygiene.

### Notes
- v0.13.1 is a patch release: no schema migrations, no new CLI flags, and no host activation behavior changes.

## [v0.13.0] - 2026-05-03

### Added
- **Claude Code host-native activation (#892, #893)** — `traceary memory activate --target claude` now plans, diffs, reports status, and explicitly applies a two-file activation pair: a managed import stub in `CLAUDE.md` plus accepted memories in `.traceary/memories/claude.md`. `traceary doctor --client claude` surfaces the same status and dry-run/apply remediation, and the structural smoke test verifies first apply, idempotent re-apply, final `in_sync`, and doctor pass behavior.
- **Gemini CLI host-native activation (#894, #895)** — `traceary memory activate --target gemini` provides the same read-only status/dry-run/diff and explicit apply workflow for `GEMINI.md` plus `.traceary/memories/gemini.md`. Apply preserves user-authored host context and Gemini-owned `## Gemini Added Memories` content, and `traceary doctor --client gemini` exposes actionable activation checks.
- **Activation target contract and docs (#889, #896)** — the new host-native activation ADR defines Claude/Gemini paths, import-stub marker layout, status states, safety rules, `.gitignore` policy, rejected alternatives, and the release sub-issue sequence. Memory, CLI, and integration docs now describe one cross-host workflow for Codex, Claude, and Gemini.

### Changed
- **Shared activation infrastructure (#890, #891)** — marker parsing, managed-region replacement, host target resolution, and safe activation file I/O are now host-agnostic primitives. The two-file planner tracks independent actions, statuses, and diffs for the host context stub and external memory file, writes the external memory file first, rejects unsafe targets such as symlinks/directories/newer markers, and remains idempotent.
- **Dogfooding evidence for activation workflows (#896)** — release-prep docs now record a Claude and Gemini temp-fixture dogfood pass covering `status -> dry-run --diff -> apply -> apply -> status -> doctor`. Codex activation behavior is unchanged from v0.12.0.

### Notes
- v0.13.0 is a minor release focused on completing host-native activation for Claude Code and Gemini CLI while preserving the v0.12 Codex activation contract.
- Live Claude/Gemini runtime probes remain opt-in via `TRACEARY_ENABLE_CLAUDE_RUNTIME_SMOKE=1` and `TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1` because host authentication and first-time import approval are environment-dependent. The default smoke/dogfood path verifies Traceary's deterministic file planning, apply, idempotency, preservation, and doctor behavior.

## [v0.12.0] - 2026-05-02

### Added
- **Durable-memory candidate quality controls (#857, #864)** — extraction now hides low-signal noise such as diff fragments, generated-code markers, standalone shell commands (including `rtk ...` wrappers), review-only conclusions, transient work declarations, and PR-round chatter while preserving durable Japanese / multilingual preferences and constraints.
- **Explicit remember and lifecycle extraction paths (#856, #862, #865)** — explicit "remember this" prompts, short remember-intent follow-ups with adjacent context, post-compact summaries, and clear/reset summaries now propose reviewable durable-memory candidates with evidence refs instead of relying on manual notes only.
- **Candidate distillation (#858)** — `traceary memory distill` turns one or more candidate memories into an accepted operator-curated fact while preserving source refs and supporting keep / reject / supersede source handling.
- **Codex memory bridge expansion (#859, #860)** — Codex import reads deterministic multi-file Markdown memory layouts under `~/.codex/memories/*.md`, while exports can include `global` memories alongside workspace memories so host-level operating rules travel with repository-specific facts.
- **Codex native activation (#866, #861, #867)** — `traceary memory activate --target codex` now supports dry-run/diff planning, read-only status (`missing`, `stale`, `in_sync`, `invalid`), and explicit `--apply` writes to the Traceary-managed Codex memory target (`~/.codex/memories/traceary.md` by default). Apply preserves user-authored content outside the managed block, is idempotent, refuses newer marker versions, and reports activated counts in text/JSON. `traceary doctor --client codex` surfaces the same activation status and remediation commands.
- **Host activation strategy docs (#868)** — memory and integration docs now distinguish the accepted Traceary store, instruction-file export, and host-native activation, with Codex implemented in v0.12 and Claude/Gemini native writes explicitly deferred to #883 / #884.

### Changed
- **Workspace exports include global memories by default (#860)** — `memory export` / MCP export include global scope entries beside an explicit workspace unless `--no-global` / `include_global=false` is used.
- **Codex import keeps legacy safety but supports shards (#859)** — legacy `MEMORY.md` keeps the historical heading allow-list, while additional Markdown shards import list items under any heading with per-file evidence/artifact refs and symlink/size guards.

### Notes
- v0.12.0 is a minor release focused on durable-memory quality, curation, and Codex-native activation. Claude and Gemini continue to use MCP tools plus instruction-file export for v0.12; native activation writes for those hosts are planned follow-ups, not shipped behavior.

## [v0.11.2] - 2026-04-28

### Added
- **Durable-memory intent diagnostics (#851)** — `traceary memory extract --debug-signals` explains segment-level extraction decisions without creating candidates, including detected features, inferred type, score, decision, reason, evidence refs, artifact refs, and source metadata (`client`, `event_kind`, `source_hook`). The debug path mirrors extraction dedupe, best-score selection, and candidate-limit behavior so dogfooding can see why a signal was proposed, hidden, skipped, or ignored.

### Changed
- **Explicit memory intent is classified instead of keyword-only matching (#851)** — extraction now recognizes durable-memory intent such as `Durable Memory:`, `Memory Note:`, `Remember:`, `Remember this:`, `覚えておいて:`, and `記憶:`. Explicit intent is scored strongly enough to be visible by default, with safe fallback type inference (`preference`, `constraint`, `decision`, `artifact`, otherwise `lesson`) instead of silently dropping ambiguous remember requests.

### Fixed
- **Avoid false-positive memory metrics (#851)** — generic `Memory:` / `メモリ:` telemetry-style lines are intentionally not treated as explicit durable-memory intent, preventing routine resource logs such as `Memory: 2 GB` from becoming visible lesson candidates.

## [v0.11.1] - 2026-04-28

### Fixed
- **Command audit evidence is visible from generic event surfaces (#842)** — `command_executed` event bodies now include the command line, exit code, input payload, and output payload so `list`, `search`, MCP `list_events`, and MCP `search` can retrieve Bash verification evidence without requiring a command-audit-specific detail lookup. Handoff recent-command summaries still trim back to the command line.
- **Claude compact summaries produce durable-memory candidates (#844)** — compact-summary events now run the same heuristic extraction path as prompt / transcript / note signals, and the extractor recognizes Japanese labels and durable markers (`決定`, `判断`, `制約`, `教訓`, `次回`, `確認済み`, etc.). Claude-style Japanese summaries now generate review-only candidates instead of silently returning `[]`.
- **Codex stale hook installs are diagnosed as memory-capture gaps (#843)** — `traceary doctor` now requires both Codex `Stop` hooks (`transcript` and `session stop`) and explains that missing `UserPromptSubmit` / transcript capture starves durable-memory extraction. The repair hint points to `traceary hooks install --client codex --upgrade`.

### Changed
- **Memory extraction visibility uses signal scoring (#835)** — the `extracted` vs `extracted-hidden` decision now combines structured labels, evidence refs, artifact refs, durable English/Japanese markers, and Latin/CJK length instead of using a length-only gate. Duplicate candidates are collapsed by dedupe key using the highest-scoring signal before assigning source, so weak early signals cannot hide stronger structured evidence.

## [v0.11.0] - 2026-04-27

### Added
- **Lifecycle observability documentation (#817, #818)** — new `docs/hooks/lifecycle-events.md` and `docs/hooks/host-coverage.md` (en+ja) describe the six lifecycle event kinds and a per-host wiring matrix.
- **Hook-driven L2/L3 memory pipeline (#825, #826, #829, #831)** — Claude `PreCompact` summary is synced into `sessions.summary`; subagent-stop and session-end hooks auto-extract candidate memories (`source=extracted`, `status=candidate`); `handoff` and `get_context` payloads include candidate memories with a status marker.
- **Quality filter + extracted-hidden visibility (#831, #833)** — auto-extracted candidates are filtered by length heuristic (Latin 20 / CJK 10 runes; artifact-types exempt), and the new `extracted-hidden` source flag preserves rejected items without surfacing them by default. CLI and MCP search both default-exclude `extracted-hidden` while exposing it on opt-in.
- **14-day auto-expire for stale candidates (#834)** — gc deletes stale auto-extracted candidates (`source IN ('extracted', 'extracted-hidden')`) older than 14 days, NULL-ing any incoming `supersedes_memory_id` references first to keep FK constraints intact.
- **SKILL redesign (#821, #822)** — `traceary-memory-review` (review/inbox/recap) and `traceary-memory-remember` (explicit-write only) replace the deprecated `traceary-memory-capture`, shipped to all three host integrations.
- **Gemini lifecycle wiring (#819, #820)** — Gemini CLI now records `prompt` events via `BeforeAgent` and `compact_summary` markers via `PreCompress`.
- **Daily host hook drift check (#828)** — `docs/operations/scheduled-tasks.md` documents a `/schedule`-driven daily diff between upstream host hook references and `host-coverage.md`.
- **Memory model docs (#823, #824, #827)** — README aligns on the three-layer model wording, the `candidate` status terminology is unified across docs, and `evidence_refs` / `artifact_refs` kind enums are now published.

### Changed
- **`traceary top` handles East Asian Wide characters (#836)** — runewidth-based column tracking replaces rune counting in `top`, `event_text_formatter`, and timeline padding. `runewidth.DefaultCondition.EastAsianWidth` is pinned to narrow at init time so column accounting is deterministic regardless of host locale.
- **MCP body truncation (#837)** — `list_events`, `get_context`, and `search` now truncate long event bodies at 500 runes by default, with `body_truncated` / `body_length` markers and `body_limit` / `full_body=true` overrides. `body_blocks` is omitted when truncation occurs.

### Notes
- Codex CLI `compact_summary` remains unimplemented because upstream openai/codex#16098 has not landed a compact hook.
- v0.11.0 is a minor release: no schema migrations; JSON contract additions only.

## [v0.10.3] - 2026-04-26

### Breaking changes
- **`traceary top --snapshot --json` contract split (#795)** — top snapshot JSON now uses a top-specific contract instead of reusing `traceary session tree --json`. Existing session tree fields remain intact, and top snapshots add `latest_event_kind`, `latest_event_message`, and `latest_event_at` for live dashboard consumers.

### Added
- **Workspace and latest event in `traceary top` rows (#794)** — text snapshot and the live TUI now include `workspace=…/owner/repo` (tail-preserving truncate at 36 runes) and `last=<kind>: <message>` (80-rune scrubbed message, `-` when there is no event yet) so parallel sessions are distinguishable at a glance. The recommended interactive workflow doc now lists `traceary top` as the first live-inspection command.
- **Latest event metadata on `SessionSummary` (#793)** — `LatestEventKind` and `LatestEventMessage` are populated by a new `latest_events` window-function CTE in `list_sessions.sql` / `list_session_tree.sql` / `session_lineage.sql`, with deterministic tie-breaking (`created_at DESC, id DESC`) and a new `idx_events_session_created_at_id_desc` index. Bodies are passed through `ExtractPlainBody` so transcript thinking blocks never reach the message field. MCP `session_status` payloads are explicitly guarded against exposing the new fields.

### Performance
- **`list_sessions` paginates before aggregating (#793)** — `LIMIT` / `OFFSET` are now pushed into the `filtered_sessions` CTE so the per-session event aggregation only touches the page that the caller actually requested, instead of every session matching the filter.


## [v0.10.2] - 2026-04-26

### Fixed
- **Hook audit / subagent-stop tests time bomb** — `TestRootCLI_HookAuditCommand_UsesActiveSubagentSession` and `TestRootCLI_HookSubagentStopCommand_EndsChildAndClearsActiveState` had hard-coded `started_at` timestamps that aged past the 24-hour `hookActiveSubagentStateTTL`, so `pruneHookActiveSubagentState` cleared the state before the assertion ran. Surfaced when v0.10.1's Homebrew formula PR CI ran. Tests now use `time.Now().UTC()` so the fixture is always within TTL.

## [v0.10.1] - 2026-04-26

### Fixed
- **PreToolUse subagent capture for `Agent` tool name (#785)** — Claude Code's plugin hooks now match `Task|Agent` for the PreToolUse:subagent-start hook. Previously the matcher accepted only `Task`, so subagent invocations dispatched as `Agent` (the current Claude Code tool name) never created the active-subagent state file or a child session row. Surfaced during the v0.10.0 post-release dogfooding (#778).

## [v0.10.0] - 2026-04-26

### Added
- **Bundle manifest v2 skeleton (#737)** — bundle export now writes `manifest_version=2` with a per-table registry entry for `events` (`table_name`, file, row count, SHA-256 checksum), import keeps the v1 reader path, and `bundle import` adds `--on-conflict {skip,replace,error}` plus the reserved `--missing-parent {reject,skip,backfill}` flag.
- **Bundle durable memories (#739)** — bundle export now includes `memories.ndjson` with scope, validity, supersession, evidence refs, and artifact refs. Bundle import surfaces `memories_imported` / `memories_skipped` counts and writes newly imported memories as review-required `candidate` rows (the candidate trust default) even if the source row was already accepted, so cross-machine memory trust is never elevated automatically.
- **Marketplace release manifest verification (#713)** — `scripts/verify_release_manifests.py` now verifies Claude/Codex marketplace manifests exist and that Claude, Gemini, and Codex plugin manifest versions match the root `VERSION`. CI, the release workflow, and `make release/bump` run the guard before publishing.
- **Sectioned `traceary doctor` output (#734)** — doctor reports are grouped into `Environment`, `Database`, `Plugins`, `MCP`, and `Hooks`; JSON output now includes additive `sections`, `summary`, and `exit_code` fields. Check severities are normalized to `PASS` / `WARN` / `FAIL`, with exit code `0` for all pass, `1` for any fail, and `2` for warnings without failures.
- **Experimental Anthropic native memory-tool backend (#742)** — `pkg/anthropicmemory` exposes a Go handler for Anthropic SDK `BetaMemoryTool20250818CommandUnion` payloads and returns SDK `tool_result` content backed by Traceary's local `memory_tool_files` table. The tool contract is pinned to `memory_20250818`; upgrades require manual review, not automatic SDK field bumps. A live-API smoke example is available in `examples/anthropic-memory/`.

### Docs
- README and Claude plugin docs now use Claude Code's `/plugin marketplace add` → `/plugin install` flow instead of the removed `claude plugins install` CLI wording.
- `docs/release/README.{md,ja.md}` documents release manifest verification and the `make release/bump VERSION=X.Y.Z` fix path for version drift.
- `docs/integrations/anthropic-memory-tool.{md,ja.md}` — explains native memory tool vs Traceary MCP `manage_memory`, path-traversal and size-limit threat model, SDK wiring, storage inspection/backup, experimental status, and version pinning. `docs/integrations/agent-sdks.{md,ja.md}` now removes the incorrect Python lock-in rationale and links to the native Go backend.

### Breaking changes
- **MCP tool consolidation (#708, breaking)** — the MCP server now exposes exactly 8 tools. The old 24 tool names were removed and replaced by action-parameter dispatch:

  | Old tool | New call |
  |---|---|
  | `propose_memory` | `manage_memory(action="propose", ...)` |
  | `remember_memory` | `manage_memory(action="remember", ...)` |
  | `accept_memory` | `manage_memory(action="accept", ids="<id>", ...)` |
  | `reject_memory` | `manage_memory(action="reject", ids="<id>")` |
  | `expire_memory` | `manage_memory(action="expire", ids="<id>", ...)` |
  | `supersede_memory` | `manage_memory(action="supersede", target_id="<id>", fact="...", ...)` |
  | `set_memory_validity` | `manage_memory(action="set_validity", ids="<id>", valid_from="...", valid_to="...", ...)` |
  | `import_memory_instructions` | `manage_memory(action="import_instructions", ...)` |
  | `accept_memories_batch` | `manage_memory(action="accept", ids=[...], ...)` |
  | `reject_memories_batch` | `manage_memory(action="reject", ids=[...])` |
  | `retrieve_memories` | `query_memory(action="retrieve", ...)` |
  | `export_memories` | `query_memory(action="export", ...)` |
  | `memory_pack` | `query_memory(action="pack", ...)` |
  | `scan_memory_hygiene` | `query_memory(action="scan_hygiene", ...)` |
  | `start_session` | `manage_session(action="start", ...)` |
  | `end_session` | `manage_session(action="end", ...)` |
  | `active_session` | `session_status(action="active", ...)` |
  | `latest_session` | `session_status(action="latest", ...)` |
  | `session_handoff` | `session_status(action="handoff", ...)` |
  | `add_log` | `record_event(type="log", ...)` |
  | `add_audit` | `record_event(type="audit", ...)` |
  | `list_events` | `list_events(...)` |
  | `search` | `search(...)` |
  | `get_context` | `get_context(...)` |

- **CLI JSON timestamp and duration freeze (#729)** — all CLI `--json` timestamp fields (`created_at`, `started_at`, `ended_at`, `valid_from`, `valid_to`, etc.) now serialize as UTC RFC3339Nano (`YYYY-MM-DDTHH:MM:SS[.nnnnnnnnn]Z`) before golden fixtures are recorded. `traceary session tree --json` drops `duration_ms` and keeps `duration_sec`; `traceary timeline --json` replaces the string `duration` field with numeric `duration_sec`.

## [v0.9.0] - 2026-04-25

Minor release: **multi-host local memory substrate completion (pre-v1.0 stabilization)**. v0.9 closes the remaining portability + structural gaps before a v1.0 scoping discussion. New top-level CLI grouping, an additive temporal graph overlay on durable memory, encrypted cross-machine event bundles, and consolidated agent-SDK integration docs.

### Added
- **Memory graph overlay (#573)** — additive `memory_edges` table layers typed relationships (`supersedes` / `contradicts` / `supports` / `related-to` / `causes`) on top of the existing memory store. Each edge carries its own half-open `[valid_from, valid_to)` window so `--as-of` queries compose with memory validity. `traceary memory graph add <from> --to <id> --relation <type>` and `traceary memory graph list [--memory-id <id>] [--relation <type>] [--as-of <ts>]`. Migration 000013 + compound partial indexes. SQLite stays the primary store; no graph-DB dependency. See `docs/architecture/temporal-memory.md` for the full evaluation.
- **Encrypted portability bundles (#572)** — `traceary bundle export --out <path>` produces an XChaCha20-Poly1305 archive (key derived via Argon2id) that operators can carry between machines via any transport (AirDrop / scp / Syncthing / iCloud — the bundle is already AEAD-encrypted). `traceary bundle import` is idempotent (UNIQUE collisions counted as `events_skipped`). v0.9 covers events; sessions / audits / memories / edges land in #702.
- **Agent SDK integration docs (#571 + #564-A)** — `docs/integrations/agent-sdks.{md,ja.md}` covers Claude Agent SDK, OpenAI Agents SDK, and Google ADK with verified MCP integration examples. Zero Python added — `traceary mcp-server` is the canonical path. Anthropic native memory-tool backend deferred to v0.10 as #699.

### Changed
- **CLI top-level reorganization (#696)** — administration commands moved under `store` (`store init`, `store backup create/restore`, `store gc`); session-bootstrap commands consolidated under `session` (`session handoff`, `session handoff --compact-only` replacing `compact-summary`). Top-level count: 22 → 16. Old top-level `init` / `backup` / `gc` / `handoff` / `compact-summary` remain as **deprecated aliases** with deprecation notices routed to **stderr** (so stdout stays byte-for-byte compatible with v0.8.x scripts) and hidden from `--help`. Aliases will be removed in v1.0.

### Dependencies
- `github.com/pelletier/go-toml/v2` 2.2.4 → 2.3.0
- `golang.org/x/mod` 0.34.0 → 0.35.0
- `golang.org/x/sys` 0.42.0 → 0.43.0
- `modernc.org/sqlite` 1.48.2 → 1.49.1
- New: `golang.org/x/crypto` (Argon2id + XChaCha20-Poly1305 for bundle encryption)

### Docs
- `docs/architecture/temporal-memory.{md,ja.md}` — temporal graph evaluation + minimum viable overlay design.
- `docs/integrations/agent-sdks.{md,ja.md}` — SDK MCP integration matrix + examples.
- `docs/operations/cross-machine-handoff.{md,ja.md}` — bundle export/import flow, transport recommendations, schema-safety rules.
- CLI reference promotes `session handoff` / `store *` as canonical forms; old paths documented as deprecated.

### Included work items for v0.9.0
- #696 v0.9-5 CLI subcommand reorganization
- #573 v0.9-3 temporal knowledge graph evaluation + minimum overlay
- #571 v0.9-1 OpenAI Agents SDK / Google ADK integration evaluation
- #564 v0.9-4 Claude Agent SDK integration (MCP path; native memory-tool backend split to #699)
- #572 v0.9-2 encrypted bundle export / import (events)

### Follow-up tickets
- #699 — v0.10 reassess Anthropic native memory-tool backend
- #702 — v0.10 extend bundle to sessions / audits / memories / edges

## [v0.8.2] - 2026-04-24

Patch release collecting v0.8.1 quality-phase tech-debt: transcript search no longer leaks thinking-block text, MCP \`list_events\` exposes the canonical block form alongside the plain-text projection, \`--source-hook\` now uses a compound index, and \`presentation/cli/doctor.go\` stops importing \`infrastructure/filesystem\` directly.

### Added
- **MCP \`list_events\` / \`add_log\` return \`body_blocks\`** — canonical transcript envelopes are exposed in their structured \`[{type, text}, ...]\` form so programmatic consumers can round-trip a transcript or build their own block-aware renderer. The existing \`body\` field keeps returning the plain-text projection (no thinking). \`search\` and \`get_context\` intentionally omit \`body_blocks\` so thinking-block text does not leak through those surfaces.
- **SQLite compound index for source_hook queries** — migration 000012 drops the simple \`idx_events_source_hook\` and creates \`idx_events_source_hook_time (source_hook, created_at DESC, id DESC) WHERE source_hook IS NOT NULL\`. Combined with a split into a primary-only query and a legacy-fallback UNION ALL query, \`traceary list --source-hook <name>\` now uses a covering index scan instead of filtering a created_at-ordered scan in memory.

### Changed
- **\`application\` gains plugin / hooks inspector contracts** — \`application.PluginCacheInspector\` + \`PluginCacheStatus\`, \`application.ClaudePluginDetector\` + \`ClaudePluginDetection\`, and an \`ExtractManagedKeyFromEntry\` method on \`application.HooksInspector\`. \`presentation/cli/doctor.go\` and \`claude_plugin_detection.go\` consume these interfaces instead of importing \`infrastructure/filesystem\` directly, restoring the hexagonal dependency direction flagged during v0.8.1 quality phase.

### Fixed
- **\`traceary search\` skips thinking-only matches** — v0.8.1's \`ExtractPlainBody\` stripped thinking blocks from the display surface, but the SQL search still ran \`body LIKE ?\` against the raw envelope. A match that only landed inside a \`thinking\` block would return a row that then rendered blank, effectively leaking internal reasoning into the search surface. The SQL now runs \`LIKE\` against a \`json_each\`-extracted projection of text blocks for canonical envelopes (with a \`typeof()\` guard that matches \`DecodeCanonicalEnvelope\`'s contract); legacy plain-text rows and non-envelope JSON stay raw-searchable.
- **Gemini CLI smoke warnings** — \`scripts/smoke_test_integrations.sh gemini\` wrote \`{}\` to \`~/.gemini/projects.json\`. Gemini CLI 0.38 expected \`{"projects":{}}\`, so \`ProjectRegistry.getShortId\` threw during cleanup and the smoke output carried two noisy \`Early cleanup failed\` / \`Tool output cleanup failed\` warnings per invocation. Writing the correct shape silences the warnings without weakening smoke coverage.

### Included work items for v0.8.2
- #682 search skips thinking-block text in transcript JSON envelopes
- #683 \`--source-hook\` filter now uses the source_hook index
- #684 MCP list_events exposes body_blocks
- #685 doctor drops infrastructure/filesystem import
- #536 gemini smoke cleanup warnings silenced

## [v0.8.1] - 2026-04-24

Patch release focused on **hook provenance, transcript structure, and validity-window precision**: a new `events.source_hook` column records exactly which host-side hook produced each event, transcript / prompt bodies are persisted as structured JSON blocks (thinking vs text separated), and the SQLite memory validity filter now preserves sub-second precision so the half-open `[valid_from, valid_to)` contract holds at fractional boundaries.

### Added
- **`events.source_hook`** — every event carries the hook identifier that produced it (`stop`, `subagent_stop`, `pre_compact`, `post_compact`, `session_start`, `session_end`, `user_prompt_submit`, `post_tool_use`, `after_agent`, `after_tool`). Non-hook writes (`traceary log`, MCP `add_log`) leave the column NULL. `traceary list --source-hook <name>` and MCP `list_events` `source_hook` param filter by it; `traceary show`, `list --wide`, `list --json`, `list --fields source_hook`, `context`, and replay HTML all surface the value. Legacy `[phase:subagent]` / `[phase:pre-compact]` body-prefix rows still match the same filter names through a migration-window fallback.
- **Transcript / prompt body blocks** — assistant transcripts and user prompts are now stored as a JSON envelope `{"blocks":[{"type":"thinking","text":"..."},{"type":"text","text":"..."}]}` so block-aware consumers can separate internal reasoning from the user-visible reply. Legacy plain-text rows continue to round-trip unchanged; non-envelope JSON bodies (e.g. `{"foo":"bar"}` notes) are preserved verbatim.
- **`memory supersede --from / --to`** — CLI and MCP `supersede_memory` accept explicit validity bounds on the replacement; reversed windows (`valid_to < valid_from`) are rejected with the same guard as `set-validity`.
- **Doctor multi-version cache warning** — `doctor` combines the existing staleness signal with a new check that fires when the Claude plugin cache contains more than one Traceary version (typical after `claude plugins update` on a resumed session), pointing operators at the fresh-session guidance.

### Changed
- **Memory validity timestamp storage** — `valid_from` / `valid_to` are normalised to fixed-width 9-digit nanoseconds so SQLite's lexicographic comparison matches `time.Time` comparison exactly. A one-off migration (000010) rewrites pre-v0.8.1 rows. The validity filter no longer wraps values in `datetime()`, so the partial index `idx_memories_valid_window` is now used.
- **`MemoryUsecase.Supersede` inherits validity** — when `memory hygiene apply` creates a supersede transition, the replacement now inherits the original's `[valid_from, valid_to)` window unless the caller passes explicit overrides. Prior builds silently dropped the window, which turned `validity_overlap_supersede` into a self-contradiction.
- **Hook body-prefix markers retired on write** — `hook_runtime` no longer emits `[phase:subagent]` on `session_ended` or `[phase:pre-compact]` on `compact_summary`; `source_hook` discriminates the lifecycle instead. Readers keep the prefix fallback for pre-v0.8.1 rows.

### Fixed
- **Non-canonical `--traceary-bin` basename** — `hooks install --upgrade` and `doctor` recognise Traceary-managed commands when the packaged binary is installed at a non-canonical path / basename (symlinks, `/usr/local/bin/traceary-dev`, Homebrew cellar paths). Prior builds misclassified them and emitted misleading "added" / "removed" summaries.

### Docs
- `CHANGELOG{,.ja}.md` entries (this file).

### Included work items for v0.8.1
- #662 structure event body as JSON blocks (transcript + prompt)
- #664 SQLite memory validity filter loses fractional seconds
- #665 MemoryUsecase.Supersede does not propagate valid_from / valid_to
- #666 Log redaction consolidation
- #667 non-canonical `--traceary-bin` basename detection
- #670 doctor plugin-cache active-snapshot vs cache mismatch
- #672 events.source_hook column (write side)
- #679 source_hook read side — CLI / MCP filter + JSON field + replay HTML + retire body-prefix markers

## [v0.8.0] - 2026-04-22

Minor release focused on **replay UX, temporal memory, and transcript capture**: a self-contained replay HTML that operators can share, per-memory validity windows for time-traveled reads, and assistant-reply transcripts across Claude Code / Codex CLI / Gemini CLI. Also expands hook coverage to Claude Code's 2026-Q2 surfaces (SubagentStop / PreCompact) and tightens the `traceary hooks install` and `doctor` ergonomics.

### Added
- **Replay export** — `traceary replay --out <path>` emits a self-contained HTML (inlined CSS, no external assets) with four panels plus a generated-at footer: Sessions, Timeline blocks, Failure hotspots, Durable memories. `--sessions`, `--events-per-session`, `--memories`, `--timeline-blocks`, `--hotspots` govern the per-panel limits. The timeline and hotspot panels share the semantics of `traceary timeline` and `traceary list --failures-only`, so operators can cross-reference either rendering.
- **Transcript events** — `transcript` is a new event kind that records the final assistant turn. Wired into the Claude Code `Stop` hook (JSONL `transcript_path`), the Codex CLI `Stop` hook (`last_assistant_message`), and the Gemini CLI `AfterAgent` hook (`prompt_response`). Built-in redactors and operator-configured `redact.extra_patterns` apply to the captured body.
- **Temporal memory validity** — every accepted memory now carries a `[valid_from, valid_to)` half-open window. `traceary memory set-validity --from / --to / --clear-to` writes the window; `traceary memory list --as-of YYYY-MM-DD` and MCP `session_handoff` / `memory_pack` filter against the window so retrieval can time-travel to any point.
- **Memory retrieval presets** — `--preset resume | review | incident` on `traceary memory list` (and MCP `session_handoff` / `memory_pack`) apply built-in type / confidence / limit defaults. User-defined entries in `read.presets` override built-in names. Explicit filter flags still win over a preset.
- **Memory hygiene detectors** — `memory hygiene scan` gains `validity_overlap_supersede` for overlapping temporally-annotated pairs. Temporally-bounded but disjoint pairs are excluded from the generic `supersede_candidate` pipeline so historical facts are never mis-reported as merge candidates.
- **Claude Code SubagentStop + PreCompact hooks** — wired via `traceary hook subagent-stop claude` and `traceary hook compact claude pre-compact`. SubagentStop persists as `session_ended` with a `[phase:subagent]` body prefix; PreCompact persists as `compact_summary` with a `[phase:pre-compact]` body prefix. `loadCompactSummary` filters the marker so handoff / memory_pack keep returning the latest post-compact digest.
- **PostToolUse matcher expansion** — Claude hook install now matches `Read | NotebookRead | Edit | MultiEdit | Write | NotebookEdit | Grep | Glob | Agent | Task | TodoWrite | WebFetch | WebSearch | ExitPlanMode` alongside `Bash` and `mcp__.*`, so every built-in Claude Code tool invocation is auditable.
- **`hooks install --upgrade`** — non-destructive migration that replaces only Traceary-managed entries, preserves user-added entries, strips obsolete event entries, and prints a summary of added / refreshed / preserved / removed events. Mutually exclusive with `--force`.
- **`hooks install --matcher <preset>`** — built-in `PostToolUse` matcher presets `minimal` (`Bash` + `mcp__.*`), `default` (+ the v0.8-6 built-in tool list; this is what packaged installs use), and `all` (+ `.*`). Omitting `--matcher` picks `default`.
- **Doctor new checks** — `claude-plugin-cache` (semver-aware: cached plugin version vs. marketplace manifest, suggests `claude plugins update`) and `<client>-host-capabilities` informational notes for 2026-Q2 host features.
- **MCP additions** — `add_log` now applies built-in + `extra_redact_patterns` redaction; `session_handoff` / `memory_pack` gain `as_of` for time-traveled memory filtering; `retrieve_memories` accepts the retrieval presets.

### Changed
- **Read-side usecases landed in `application/`** — `ContextUsecase` and `ReplayUsecase` consume `queryservice.*` (not write-side `Usecase` types) and CLI / MCP share the same builders. Removed the presentation-layer fallback that silently constructed a zero-value usecase.
- **Event body phase markers** — shared constants in `domain/types/event_body_markers.go` (`EventBodyMarkerCompactPreSnapshot`, `EventBodyMarkerSubagentStop`). Phase distinctions are encoded as body prefixes, not new event kinds, so downstream consumers stay backwards compatible.
- **Redaction leaf package** — `application/redaction` is now a standalone leaf package shared by write-side usecases and presentation callers. `transcript` / `add_log` ingest surfaces share the same compiled redactor set as `traceary log` / `hook audit`.
- **Codex Stop ordering** — the packaged Codex hook fires `hook transcript codex` before `hook session codex stop` so transcript records against the still-active session (session-stop clears the workspace state file as part of teardown).

### Fixed
- **Transcript redaction parity** — `hook_runtime.go` and MCP `add_log` apply operator-configured `extra_redact_patterns`, matching the audit path's policy. Prior builds silently leaked org-specific secret shapes in `transcript` bodies.
- **CLI vs. MCP null shape** — JSON outputs where a field was `null` in CLI and `""` in MCP (and vice versa) are unified. Empty strings are omitted; explicit nulls represent "unset".
- **`doctor` pseudo-version semver** — Go pseudo-versions (`v0.7.3-0.YYYYMMDDhhmmss-abc...`) are now compared against the latest release tag via semver, so a dev build reports `newer than the latest release` instead of being flagged as downgradable.
- **`doctor` plugin cache staleness** — reads the cached plugin manifest and the marketplace manifest, warns when cached < marketplace, and points operators at `claude plugins update`.
- **Hooks merge `--matcher`-only changes** — the managed-key comparator now includes the matcher so a preset change (same event, different matcher) is classified as `Refreshed` instead of `Preserved`.

### Docs
- `docs/hooks/contract{,.ja}.md` documents the three-tier capability matrix with the new transcript behaviours and the 2026-Q2 host-capability appendix (SubagentStop / PreCompact / Codex `last_assistant_message` / Gemini `AfterAgent` all annotated as `wired`).
- `docs/integrations/codex-plugin{,.ja}.md` and `docs/integrations/gemini-extension{,.ja}.md` list the transcript hook in their "what it wires automatically" sections.
- `docs/cli/README{,.ja}.md` list `--upgrade`, `--matcher`, `--timeline-blocks`, `--hotspots`, `--as-of`, and `--preset` under the corresponding commands.

### Included work items for v0.8.0
- #566 v0.8-4 memory block evaluation
- #565 v0.8-3 temporal validity
- #606 v0.8-7 transcript capture on Claude Stop
- #605 v0.8-6 PostToolUse matcher expansion
- #570 v0.8-5 memory retrieval presets
- #563 v0.8-1 replay export
- #624 v0.8 quality-phase polish
- #625 v0.8-followup redaction leaf package
- #619 v0.8-followup transcript → memory extraction + MCP descriptions
- #629 v0.8-followup SetValidity CLI/MCP tests
- #635 v0.8-followup propose_memory SKILL.md guidance
- #640 v0.8-followup transcript kind consistency
- #633 v0.8-followup doctor plugin cache version check + upgrade docs
- #634 v0.8-followup doctor pseudo-version semver compare
- #626 v0.8-followup transcript extra_redact_patterns
- #628 v0.8-followup CLI vs MCP JSON null shape
- #617 v0.8-followup as_of ContextPackCriteria
- #648 v0.8-followup MCP add_log redaction
- #621 v0.8-followup PostToolUse matcher 2026-Q2
- #632 v0.8-followup hooks install --matcher preset
- #654 v0.8-followup monitoring UX agent in default
- #637 v0.8-followup hooks install --upgrade flag
- #616 v0.8-followup auto-supersede heuristic (validity_overlap_supersede)
- #627 v0.8-followup replay bundle → application layer
- #636 v0.8-followup SubagentStop + PreCompact hooks
- #630 v0.8-followup replay timeline + failure-hotspot panels
- #631 v0.8-followup transcript Codex / Gemini parity
- #666 v0.8.0 move redaction into EventUsecase.Log (mirror AuditRedaction)
- #667 v0.8.0 recognise non-canonical --traceary-bin basenames via traceary- name prefix

## [v0.7.2] - 2026-04-20

Operational-safety hotfix for v0.7.1 runtime issues that affected tail polling, audit duplication, and hook install ergonomics.

### Added
- `traceary hooks install --global` writes hooks into the user-level config (`~/.claude/settings.json`, `~/.gemini/settings.json`) instead of the per-project path. Codex is already user-level so `--global` prints a no-op notice. `--global` is mutually exclusive with `--output` and refuses a relative HOME.
- `traceary doctor` now emits a `<client>-global-config` check alongside the project-level `<client>-config` check (Claude / Gemini; Codex stays opted out because its default install is already user-level).

### Fixed
- SQLite DSN now enables `journal_mode=WAL`, `synchronous=NORMAL`, and `busy_timeout=5000` so `traceary tail` polling no longer blocks behind short-lived hook writes and transient contention is auto-retried instead of surfacing `SQLITE_BUSY` to the caller.
- `traceary hooks install --client claude` detects an active Traceary Claude Code plugin via `~/.claude/settings.json`'s `enabledPlugins` and skips by default so a plugin-managed install plus a settings.json install no longer records every audit event twice. `--force` bypasses the check for plugin-development workflows.
- `traceary doctor --client claude` now reports the double-registration state as `warn` (previously silent pass) and still surfaces malformed project settings as `fail` even when the plugin would otherwise claim the hooks.

### Docs
- `docs/hooks/README{,.ja}.md` describe the `--global` flag, the plugin-skip semantics, and the new doctor checks.
- `docs/operations/README{,.ja}.md` document the new SQLite pragmas and WAL sidecar expectations.
- `docs/backup/README{,.ja}.md` warn about copying a live DB without its `<db>-wal` / `<db>-shm` sidecars.
- `docs/integrations/claude-plugin{,.ja}.md` note that `hooks install` is not required when the plugin is installed.
- `docs/cli/README{,.ja}.md` list `--global` under `hooks install`'s useful flags.

### Included issues
- #607 v0.7.2-1 SQLite DSN WAL + busy_timeout
- #603 v0.7.2-2 hooks install / doctor plugin-aware dedup
- #604 v0.7.2-3 hooks install --global + doctor global-config check

## [v0.7.1] - 2026-04-20

Patch release that closes gaps surfaced during v0.7 Multi-AI reviews
and the v0.7.0 release workflow.

### Added
- `traceary memory hygiene scan --similarity <N>` surfaces a new
  `supersede_candidate` kind: two accepted memories in the same scope
  whose fact text differs but whose word-Jaccard similarity meets or
  exceeds the threshold (default 0.6). The older memory is the
  supersede target; the newer memory's fact is the suggested
  replacement (`replacement_memory_id`, `replacement_fact`,
  `similarity`). `memory hygiene apply --ids` commits the transition
  via `MemoryUsecase.Supersede` inheriting scope / type / refs.
- MCP `export_memories` (read-only) renders accepted memories into
  CLAUDE.md / AGENTS.md / GEMINI.md markdown; MCP
  `import_memory_instructions` parses a path or inline markdown into
  durable-memory candidates. Both tools mirror the v0.7 CLI bridge
  surfaces so agent hosts can round-trip instruction files without
  shelling out.

### Changed
- Bridge marker parser now matches any `:v<N>` begin suffix via regex
  and emits a warning when the encoded version exceeds the binary's
  known max. The exporter keeps writing `:v1`, and the CLI merge
  helper refuses to downgrade a newer block so `:v2` content written
  by a future Traceary build is never silently overwritten.
- Release workflow's umbrella auto-close step now also accepts the
  minor-version title prefix (`v0.7: ...`) when the pushed tag has
  the form `vX.Y.0`, so future minor releases close their tracker
  without operator follow-up.

### Fixed
- `git tag` validation in the release workflow routes `${{ inputs.tag }}`
  through an env var instead of direct shell expansion, closing the
  known GitHub Actions injection shape.
- `mergeMemoryExportIntoExistingFile` anchors the marker regex at the
  start of a line so a marker string that appears inside prose (for
  example in hand-written documentation) cannot be mistaken for the
  managed block start.
- `import_memory_instructions` MCP tool is marked `DestructiveHint:
  false` to match `propose_memory` (both are additive candidate
  writes).

### Included issues
- #592 v0.7.1-1 release workflow matcher
- #593 v0.7.1-2 similarity-based supersede suggestion
- #594 v0.7.1-3 MCP export / import instructions tools
- #595 v0.7.1-4 bridge marker `:v<N>` forward-compat parsing

## [v0.7.0] - 2026-04-20

Codex standardization + durable-memory governance release. v0.7 moves the
read UX forward (configurable columns, presets, color highlighting,
session follow), gives Codex and subagent lineage first-class capture,
and turns durable memory into a governed substrate with inbox review,
bidirectional host-file bridges, and hygiene tooling.

### Added
- `traceary memory import codex` reads `~/.codex/memories/MEMORY.md` and records bullets under User preferences / Reusable knowledge / Failures as durable-memory candidates with file evidence refs
- `traceary memory inbox list / accept --ids / reject --ids` plus MCP `accept_memories_batch` / `reject_memories_batch` for the candidate review workflow
- `traceary memory export --target <claude|codex|gemini> --out <path>` serializes accepted memories into a deterministic markdown block wrapped in `<!-- traceary-memories:begin:v1 -->` markers so re-runs are idempotent and hand-written sections of the instruction file are preserved
- `traceary memory import instructions --source <...> --in <path>` round-trips CLAUDE.md / AGENTS.md / GEMINI.md back into durable-memory candidates
- `traceary memory hygiene scan` surfaces `redaction_hit` / `expiry_candidate` / `duplicate` suggestions for accepted memories; `memory hygiene apply --ids` commits the implied lifecycle transition; MCP `scan_memory_hygiene` mirrors the scanner read-only
- `traceary tail --follow-session <prefix>` tails a specific session id (minimum 8-rune prefix, UUID-safe)
- `traceary session tree` JSON gains `parent_session_id`, `depth`, `duration_ms`, and `subagent_type`; text rows surface the most specific subagent role and `N cmds / M events`; `--root <session-id>` focuses on a subtree and `--ongoing-only` keeps only lineages with an active session
- `traceary tail / list / search` accept `--fields` (configurable column order), `--preset` (built-in catalog `failures` / `prompts-only` / `compact-summaries` plus user-defined presets in `~/.config/traceary/config.json`), and `--color=auto|always|never` highlighting with `NO_COLOR` + terminal-injection defenses
- Codex hooks now capture `UserPromptSubmit` and Codex is reachable through the official Codex plugin directory layout
- `traceary doctor` ships a `<client>-host-capabilities` informational check per client, documenting 2026 Q2 capabilities Traceary does not yet wire (Claude `SubagentStop` / `PreCompact`, Codex memory feature flag, Gemini 0.38.x memory-manager preview)
- MCP tool metadata / annotations audit brings every tool in line with the Tool Search era

### Changed
- Codex standardization: v0.7-1 aligns Codex with the official `/plugins` flow and Plugin Directory conventions
- Read command text output defaults are driven by the new field / preset / color surfaces; legacy `--wide --utc` still reproduces the pre-v0.6.1 byte-for-byte output
- Durable-memory list criteria push `Sources()` down to the SQLite layer so `--source` on the inbox paginates correctly

### Fixed
- `traceary memory import codex` symlinked MEMORY.md rejection now blocks at the raw path, not after EvalSymlinks, so a symlink inside the memory root cannot redirect the reader at another file
- Bridge import dedupe pre-load now uses a valid limit (the real SQLite datasource rejects limit<1); this also fixes the same latent bug in the Codex memories import
- `memory export --out` no longer destroys hand-written sections of CLAUDE.md / AGENTS.md / GEMINI.md — the CLI reads the existing file and replaces only the marker-bracketed block
- `tail --follow-session` advances the cursor over the unfiltered poll batch, so non-matching session traffic no longer pins the scan window
- `session tree --ongoing-only` excludes `status=stale` sessions (previously treated as ongoing because they have no end event)
- Various sanitizer / size-guard / parser robustness fixes across the memories import and bridge import paths

### Included issues
- #551 v0.7-1 Codex plugin directory
- #575 v0.7-2 Codex UserPromptSubmit capture
- #576 v0.7-3 Codex memories import
- #552 v0.7-4 host matrix / doctor / smoke refresh
- #553 v0.7-5 configurable columns for tail / list / search
- #554 v0.7-6 saved view presets
- #555 v0.7-7 highlight / color with NO_COLOR support
- #556 v0.7-8 `tail --follow-session`
- #557 v0.7-9 durable-memory review inbox
- #560 v0.7-10 CLAUDE.md / AGENTS.md / GEMINI.md bridges
- #561 v0.7-11 subagent lineage in session tree
- #568 v0.7-12 durable-memory hygiene (redaction / expiry / duplicate)
- #569 v0.7-13 MCP tool metadata / annotations audit

## [v0.6.1] - 2026-04-15

Terminal readability release for `tail`, `list`, `search`, and `timeline`.

### Added
- `traceary timeline` per-workspace sub-rows with an activity summary chosen via the `compact_summary` → first `prompt` → kind-count fallback chain, plus a `workspace_breakdown` array in the JSON output
- `--utc` flag on `tail`, `list`, `search`, and `timeline` text output for parity (local time remains the default)
- shared `presentation/cli/event_text_formatter.go` helper (compact row / wide row / timestamp / session / workspace shorteners) so `tail`, `list`, and `search` render through the same code path
- "Inspect recent and live activity" section and `traceary timeline` entry in `docs/cli/README.md` / `README.ja.md` plus new tail / timeline examples in the top-level README

### Changed
- `traceary tail`, `traceary list`, and text-mode `traceary search` now default to a compact single-line row (`HH:MM:SS  kind  sess=<first-8>  ws=<basename>  message`) in local time that fits inside ~100 columns; `--wide` restores the legacy seven-column tab-separated format and `--wide --utc` reproduces the pre-v0.6.1 output byte-for-byte
- `traceary timeline` header now includes a `total events:` label alongside the duration

### Fixed
- `traceary timeline` no longer leaks empty-workspace legacy rows into the breakdown or the JSON `workspaces` array
- `traceary timeline` no longer lets a whitespace-only `compact_summary` / `prompt` body override a later non-blank summary candidate in the same block

### Included issues
- #538 compact tail output with local TZ
- #539 timeline activity summary with per-workspace breakdown
- #540 add README examples for tail and timeline workflows
- #541 compact list and search output with local TZ parity

## [v0.6.0] - 2026-04-14

Architecture consistency and runtime entrypoint cleanup release.

### Added
- software architecture principle guides that document the four-layer boundaries, runtime-entrypoint rules, and the role of `scripts/`
- Go `traceary hook ...` runtime subcommands for session boundaries, command audits, prompt capture, and compact-summary capture
- `traceary integration codex install` / `uninstall` as the user-facing Codex integration entrypoints
- maintainer-facing documentation for the planned `go run ./cmd/repo-tooling ...` migration path

### Changed
- packaged hook generation now targets Go runtime entrypoints directly instead of relying on embedded runtime shell-script assets
- repository code now uses the convention Optional API (`Some`, `None`, `Value`) while keeping legacy aliases as compatibility shims
- representative test suites now prefer inline assertions and `cmp.Diff` for repeated comparisons

### Fixed
- hook runtime state handling remains best-effort while clearing stale duplicate end markers and handling wrapper/grandparent process state more safely
- managed hook detection, merge behavior, and Codex plugin install/uninstall flows now preserve unrelated custom hooks and support custom `traceary` wrapper paths

### Included issues
- #459 align Optional[T] API with Go conventions
- #506 document software architecture principles and runtime boundaries
- #507 move hook runtime logic into Go subcommands
- #508 migrate packaged hooks away from embedded runtime shell assets
- #509 reduce Python dependency in user-facing and maintainer workflows
- #522 replace Codex Python install helpers with Go entrypoints
- #523 migrate maintainer Python helpers into Go repo tooling
- #525 migrate Optional[T] toward the convention API
- #527 align repository tests with the Go testing conventions

## [v0.5.2] - 2026-04-14

Documentation accuracy, navigation, and GoDoc polish release.

### Added
- durable memory concept guides under `docs/memory/` in English and Japanese

### Changed
- refreshed the storage and interactive guides so they match the shipped `tail`, `handoff`, and durable-memory behavior
- aligned `workspace` terminology across public docs and operator-facing CLI help text
- reorganized README/docs entry points around the three-layer model, lifecycle, hook contract, and durable memory flows
- updated release/support guidance to reflect the current maintainer review flow and release automation
- deepened GoDoc package/interface comments so layer boundaries and compatibility surfaces are easier to discover from `go doc`

### Included issues
- #500 refresh stale storage and interactive docs to match shipped behavior
- #501 complete workspace terminology cleanup across docs and CLI help
- #502 improve documentation entry points and add a durable memory concept guide
- #503 refresh release/support docs and examples for the current maintainer workflow
- #504 deepen GoDoc package/interface comments for architecture discovery

## [v0.5.1] - 2026-04-14

Read-side ergonomics and documentation follow-up release.

### Added
- `traceary tail` command for following new events as they arrive, with NDJSON output support
- CI job that enforces CHANGELOG coverage for every released tag before release

### Changed
- README restructured around the three-layer memory model and host-capability matrix
- backfilled missing changelog entries for v0.2.2 through v0.4.0

### Fixed
- `traceary tail` follow mode now scans each poll window under a single SQLite read snapshot, closing a data-loss bug where concurrent writers could silently drop events via OFFSET-based pagination
- durable memory extraction now dedupes existing facts after caller-supplied redaction patterns normalize them, so rotating an extra redact pattern no longer produces duplicate candidates
- broadened artifact-ref extraction to cover more path shapes (dotted paths, trailing punctuation) while rejecting slash-prose false positives

### Included issues
- #473 broader artifact-ref extraction for memory candidates
- #474 dedupe extracted memories across redaction-rule changes
- #477 backfill missing changelog entries for v0.2.2 through v0.4.0
- #478 restructure README around the memory model and host capabilities
- #481 live tailing for Traceary event streams
- #483 enforce changelog coverage in CI and release preparation
- #489 fix tail pagination data loss under concurrent writes
- #490 strengthen dedupe test to cover redaction-pattern normalization
- #491 pin tail boundary dedupe and From inclusivity contract

## [v0.5.0] - 2026-04-13

Durable memory and context-aware workflows release.

### Added
- first-class durable memory domain model with typed scopes, evidence refs, artifact refs, and lifecycle state
- durable memory SQLite persistence and query support
- CLI durable memory commands: `remember`, `propose`, `accept`, `reject`, `supersede`, `expire`, `list`, `search`, `show`, and `extract`
- `ContextUsecase`-backed structured handoff/context assembly shared by `traceary handoff`, `traceary session handoff`, and `traceary compact-summary`
- MCP durable memory tools and memory-aware context retrieval
- candidate extraction from session summaries, compact summaries, and note/review/prompt signals

### Changed
- release workflow now uses a GitHub App token for the Homebrew maintenance PR path on protected `main`
- integration manifests and package metadata advance to the `0.5.0` release line

### Included issues
- #457 Use GitHub App token for Homebrew release PRs
- #453 Define a three-layer model for audit logs, working memory, and durable memory
- #460 Introduce the Memory aggregate and typed memory value objects
- #461 Add SQLite persistence and query support for durable memory
- #462 Add manual memory lifecycle usecases and CLI commands
- #463 Introduce ContextUsecase and unify handoff/context semantics
- #464 Add MCP memory tools and memory-aware context retrieval
- #465 Extract memory candidates from session and compact summaries

## [v0.4.0] - 2026-04-12

Timeline, prompt capture, and architecture-hardening release.

### Added
- event-kind expansion for `compact_summary` and `prompt` signals, plus `--kind` support for `traceary log` / MCP `add_log`
- generated hooks for `PostCompact` and `UserPromptSubmit`, including persisted compact-summary and prompt events
- `traceary timeline` plus timeline block query support for workspace activity inspection
- lifecycle and privacy documentation for the expanded hook surface

### Fixed
- session boundary persistence, duplicate session handling, and compact/prompt agent resolution became more defensive
- hook install/read paths now reject unsafe symlink traversal patterns
- `--db-path` / `TRACEARY_DB_PATH` are honored consistently across subcommands
- query/input validation gaps introduced during interface consolidation were restored

### Changed
- the presentation, usecase, queryservice, and sqlite wiring was consolidated around multi-method interfaces and datasource-per-aggregate structure
- repository/type ownership moved deeper into `domain/` and `application/types`, including the `domain/port` removal
- JSON/output structs, DTOs, and Optional propagation were normalized across CLI and MCP surfaces

## [v0.3.0] - 2026-04-11

Workspace rename and consolidated-usecase architecture release.

### Added
- `Client` and `Workspace` value objects plus filter-criteria DTOs for the new application-layer query surface
- consolidated Event/Session/Store usecase interfaces and a service-factory-based composition path
- repository interfaces and session-label support needed for the next architecture pass

### Changed
- renamed the repository/work-context concept to `workspace` across hooks, CLI, docs, and storage-facing APIs
- moved DB path injection to datasource construction and migrated presentation/MCP wiring onto the consolidated usecases
- refreshed release automation/supporting repo metadata (release checklist, dependabot, pinned actions, release-drafter split)

### Fixed
- MCP session handoff now preserves `session_id` correctly
- remaining `repo` references and generated plugin hook drift were removed after the workspace rename
- release automation and review follow-ups landed before the release tag

## [v0.2.5] - 2026-04-11

Session lifecycle and queryservice cleanup release.

### Added
- `traceary session tree --json`
- `--since` / `--until` aliases for session-list date filters
- parent-session propagation through `TRACEARY_PARENT_SESSION_ID`

### Fixed
- handoff / compact-summary now pass the requested session filter through session lookup correctly
- session end, duplicate session start, stale-session GC, and invalid parent-session input handling became stricter and more explicit
- doctor version comparison now strips build metadata before evaluating upgrade status

### Changed
- extracted stale-session closing into a dedicated usecase
- moved queryservice consumer interfaces to `domain/port`
- extracted remaining inline SQL into embedded `.sql` files

## [v0.2.4] - 2026-04-11

MCP audit enrichment patch release.

### Added
- MCP audit payloads now fall back to `tool_name` when `tool_input` is empty

### Fixed
- `traceary doctor` recognizes Claude plugin installs as a valid hook source
- ending an already-ended session now emits an explicit warning instead of silently doing nothing

### Changed
- release/version-bump automation advanced to support the newer patch-release flow

## [v0.2.3] - 2026-04-11

Review-fix patch release for `v0.2.2`.

### Fixed
- follow-up review findings from the `v0.2.2` release line

### Changed
- Homebrew formula metadata advanced to the `v0.2.2` release state

## [v0.2.2] - 2026-04-11

Query-surface ergonomics patch release.

### Added
- `traceary list --from/--to`
- `traceary session list --client`
- `list_events` MCP filters for client / agent / workspace / session / kind
- positional argument support for `traceary backup create`

### Fixed
- `traceary show` now includes `exit_code` for command-audit events
- `traceary search --failures` counts as a valid structured search constraint
- `traceary list --kind audit` resolves cleanly to command-audit events
- CLI date parsing and session-list date-range validation behave consistently, including inverted-range rejection

## [v0.2.1] - 2026-04-11

Complete v0.2.0 scope gaps.

### Added
- `traceary session gc` command to close stale sessions with `--dry-run` support
- `session_handoff` MCP tool for structured session summary
- `traceary search --failures` flag for failure-first search
- Compact-summary command tests (3 cases)
- Golden file test for session start hook output
- `traceary_resolve_session_id` and `traceary_resolve_effective_repo` shared helpers in common.sh

### Changed
- Extracted `search_events` SQL to go:embed file
- Consolidated shared hook resolution functions to reduce duplication
- Fixed goreleaser formula generation (`skip_upload: true` instead of invalid `disable: true`)
- Added auto-merge for Homebrew formula PRs in release workflow

### Included issues
- #277 Extract remaining SQL
- #278 Session handoff MCP tool
- #279 Compact-summary tests
- #280 Search --failures flag
- #281 Session gc command
- #283 Hook function consolidation
- #284 Golden file tests

## [v0.2.0] - 2026-04-11

Context preservation and production readiness release.

### Added
- Automatic context preservation: PostCompact and SessionStart(compact) hooks inject lightweight context pointer after compact/clear
- `traceary compact-summary` command for LLM-free context pointer generation
- `traceary session handoff` command for concise session state summary
- `traceary session tree` command for parent-child session visualization
- `traceary list --failures` flag for failure-first view (filter by non-zero exit code)
- `exit_code` column in command_audits for failure tracking (migration 000005)
- Version check in `traceary doctor` via GitHub API with 3s timeout
- Hook contract documentation defining capability tiers across Claude, Codex, Gemini
- Integration contract tests verifying hooks.json structure for all 3 clients
- Migration regression tests (empty DB, idempotency, backfill accuracy)

### Changed
- Gemini CLI AfterTool expanded from `run_shell_command` to all tools
- Stale session detection: active sessions >24h marked as "stale" in session list
- README restructured as CLI-first install flow (Step 1: CLI, Step 2: Plugin)
- Makefile translated to English with `code/build` and `code/cover` targets added
- All test names migrated from Japanese to English for OSS contributors
- Repository interfaces moved to `domain/port` package (fixing infrastructure → application dependency)
- `list_sessions` SQL extracted to embedded file via go:embed

### Improved
- domain/model coverage: 48.8% → 97.6%, domain/types: 42.3% → 100%
- scripts/hooks coverage: 14.3% → 78.6%
- mcpserver coverage: 66.7% → 73.8%

### Included issues
- #236 Automatic context preservation across compact/clear
- #237 Session handoff command
- #238 Session tree visualization
- #239 Failure-first view in list
- #240 Gemini full tool audit
- #241 Codex SessionEnd reliability (stale detection)
- #242 Unified hook contract across clients
- #243 scripts/hooks test coverage
- #244 domain model/types test coverage
- #245 main/mcpserver test coverage
- #246 Hook payload normalization (exit_code)
- #247 Integration contract tests
- #248 Migration regression tests
- #249 Version check in doctor
- #250 README CLI-first restructure
- #251 Repository interfaces to domain/port
- #252 SQL extraction to go:embed
- #253 Test names English migration
- #254 Makefile improvements

## [v0.1.19] - 2026-04-10

This release improves CLI visibility, makes config failures visible before they silently weaken redaction behavior, and removes drift-prone hook asset duplication.

### Added
- `traceary doctor` now reports config health states for missing, loaded, unreadable, and invalid `config.json` files
- CLI and MCP config loading now emit operator-visible warnings when a broken config disables extra redaction patterns
- regression coverage for embedded hook script line-ending normalization and required-flag setup behavior

### Fixed
- `traceary session list` text and JSON output now surface `label`, `summary`, and `parent_session_id` consistently
- CLI docs and top-level docs now document `traceary session label` and the richer `session list` metadata surface
- tabular session metadata output now normalizes tabs/newlines to avoid terminal layout breakage
- packaged hook scripts now normalize embedded line endings to LF before installation, avoiding `/bin/bash\r` shebang regressions on Windows checkouts

### Changed
- packaged hook assets are now derived from the canonical `scripts/hooks/*.sh` sources instead of duplicate handwritten string literals
- the remaining Cobra required-flag setup panics were replaced with graceful command-construction errors while preserving required-flag semantics
- updated integration manifests to version `0.1.19`

### Included issues
- #219 Surface session metadata consistently in CLI output and docs
- #220 Make config load failures visible to operators
- #221 Make hook scripts single-source for packaging and tests
- #222 Replace remaining CLI setup panics with graceful errors

## [v0.1.18] - 2026-04-10

This release introduces a dedicated sessions table, enriches session metadata with labels, summaries, and parent-child relationships, and improves data quality.

### Added
- `sessions` table with migration and backfill from existing events
- `traceary session label <text> --session-id <id>` command to tag sessions
- `--label` filter for `session list` command
- `--summary` flag on `traceary session end` to record session summaries
- `--parent-session-id` flag on `traceary session start` for sub-agent hierarchy
- MCP tool call recording via `mcp__.*` matcher in Claude Code hooks
- Gemini CLI one-command install script (`scripts/install-gemini-extension.sh`)

### Fixed
- Audit hooks now persist repo from session start and reuse it, preventing CWD-based repo drift in sub-agents
- Audit script falls back to `tool_name` when `tool_input.command` is absent (MCP tools)
- Consolidated date validation into queryservice layer (removed redundant infra-layer validation)
- Fixed excess `localizef` arguments in doctor config checks

### Changed
- `session list` query rewritten to use sessions table with events JOIN for aggregated counts
- `SessionSummary` DTO now includes `label`, `summary`, `parent_session_id`
- Updated integration manifests to version `0.1.18`

### Included issues
- #196 Consolidate date validation into queryservice layer
- #200 Record MCP tool calls via hooks
- #201 Add searchable labels/task names to sessions
- #202 Record parent-child relationships between agent sessions
- #203 Normalize repo field to prevent CWD-based drift
- #204 Record session summary on session end
- #206 Introduce session metadata model
- #207 Improve Gemini CLI installation experience

## [v0.1.17] - 2026-04-09

This release focuses on multi-agent workflow improvements and CLI ergonomics.

### Added
- `traceary session list` command — aggregated session summaries with status, duration, event/command counts, and agent breakdown per session
- sub-agent identification for Claude Code — hooks now read `agent_type` from the payload and record hierarchical agent names (e.g. `claude/Explore`) when running inside a subagent
- `--from`, `--to` date filters for `session list` with YYYY-MM-DD validation

### Fixed
- MESSAGE column in `list`/`search` table output is now truncated to 80 characters with newline normalization — prevents terminal layout breakage from long command bodies
- `chmod(0600)` errors during DB initialization are now best-effort — read-only commands work on read-only filesystems or when the DB is owned by another user

### Changed
- enforced "1 issue = 1 branch = 1 PR (no exceptions)" rule in CLAUDE.md, AGENTS.md, and GEMINI.md
- updated integration manifests to version `0.1.17`

### Included issues
- #185 Truncate MESSAGE column in list/search output
- #186 Add session summary command (`session list`)
- #187 Support sub-agent identification for Claude Code
- #188 Make read-only commands safe on read-only filesystems
- #194 Add hook guardrail for 1-issue-1-PR rule

## [v0.1.16] - 2026-04-09

This release improves code quality, adds user-configurable audit redaction patterns, and enriches debug-level diagnostics across the CLI and MCP server.

### Added
- user-configurable audit redaction patterns via `~/.config/traceary/config.json` — extra regex patterns are applied after the built-in rules in both the CLI (`traceary audit`) and the MCP server (`add_audit`)
- debug-level logging for suppressed cleanup errors across all `infrastructure/sqlite/` files
- debug-level logging for each fallback stage in session and repo context resolution
- `CLAUDE.md`, `AGENTS.md`, and `GEMINI.md` project convention files for consistent AI agent behavior
- tests for `LoadConfig`, `compileExtraRedactPatterns`, and `setupLogger`

### Fixed
- replaced `log.Fatalf` in `init()` with a graceful error return from `run()` — invalid `LOG_LEVEL` now prints a clean error instead of a stack trace
- MCP server now loads config once at startup instead of per-request

### Changed
- added issue closing policy to agent instruction files (implementation PRs close sub-issues only; parent issues are closed by release PRs)
- excluded AI agent instruction files from the docs i18n check
- documented config.json and extra redaction patterns in the environment reference docs
- updated release-facing integration manifests to version `0.1.16`

### Included issues
- #170 Replace panic calls in CLI initialization with graceful errors
- #171 Make audit redaction patterns user-configurable
- #172 Add debug logging for suppressed cleanup errors
- #173 Clarify error propagation in session resolution logic

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
