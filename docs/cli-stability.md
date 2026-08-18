# CLI stability and deprecation policy

[日本語](./cli-stability.ja.md)

This document is the operator- and integrator-facing contract for Traceary's CLI surface.
It describes which commands are part of the public surface, which are admin/maintenance, and which are plumbing or hidden deprecation shims.
It also defines the deprecation notice expectations, the one-minor compatibility window, and the v0 vs v1 removal policy that the v0.15 series and the upcoming v1.0 release commit to.

The companion [CLI reference](./cli/README.md) documents per-command flags and behavior.
This page deliberately stays at policy level so it can be linked from skill packs, AI integrations, and external tooling that needs to reason about stability without re-reading the full reference.

## Goals

- Make the v0.15 command surface explicit before v1.0 so external tooling can pin against documented stability tiers.
- Keep daily-use commands (the public surface) byte-for-byte stable across minor releases.
- Allow admin/maintenance commands to evolve at minor boundaries without breaking the public surface.
- Give scripts, AI skills, and example snippets one full minor cycle to migrate before any working command is removed.

## Stability tiers

Every Traceary subcommand belongs to exactly one tier.
The tier sets the rules for what may change, when it may change, and what notice external callers receive.

| Tier | Visibility | Stable surface | Allowed changes per release |
|---|---|---|---|
| **Public** | Listed in `traceary --help` and in `docs/cli/README.md`. | Command path, flag names, exit codes, stdout text shape, `--json` / `--id-only` / NDJSON byte shape, error message structure. | Additive only between minors (new flags, new optional JSON fields, new subcommands). Breaking changes require the deprecation flow below and at least one minor of overlap. |
| **Admin / maintenance** | Listed in `--help` under their namespace (`store`, `memory admin`, etc.) and in `docs/cli/README.md`. | Command path and the documented flag set; `--json` / `--dry-run` / `--apply` semantics where applicable. | Additive between minors. Breaking changes use the same deprecation flow as public commands but may move faster (stderr notice in N, removal in N+1) when the affected audience is operators only. |
| **Plumbing / hidden / deprecated** | Hidden from `--help` (`Hidden: true`). Documented as "deprecated alias" or "cleanup-only" in the CLI reference. | Argument and flag shape of the canonical replacement they re-route into; stderr deprecation notice format. | May be removed at the next minor release named in the deprecation notice. New plumbing commands should not be introduced unless they exist to bridge an in-flight migration. |

### Public commands (current)

The public surface is the operator-facing daily-use surface. Public commands keep their command path, flag names, stdout text shape, and `--json` / `--id-only` / NDJSON byte shape stable across minor releases.

Current public commands, including compatibility aliases introduced after v0.15, are grouped by intent. The shipped classification of every visible public/admin leaf is `presentation/cli/pillar_inventory.go` (#1692).

- **Event recording** — `traceary log`, `traceary audit`
- **Read / inspection** — `traceary list` (including `--follow` and `--blocks`), `traceary search`, `traceary show`, `traceary context` (including `--handoff` and `--compact-only`)
- **Sessions** — `traceary session start`, `traceary session end`, `traceary session run`, `traceary session refine`
- **Durable memory daily read** — `traceary memory search` (including `--all`), `traceary memory show`
- **Durable memory inbox** — `traceary memory inbox list`, `traceary memory inbox show`, `traceary memory inbox accept`, `traceary memory inbox reject`, `traceary memory inbox attach`, `traceary memory inbox cleanup`, `traceary memory inbox restore`, `traceary memory inbox review` (TTY-only)
- **Durable memory store** — `traceary memory store propose`, `traceary memory store distill`
- **Hooks** — `traceary hooks install` (including `--dry-run`), `traceary hooks guide`, `traceary completion` (`bash` / `zsh` / `fish` / `powershell`)
- **Diagnostics** — `traceary doctor` (alias `traceary status`, including the additive `store-capacity` check), `traceary report`
- **Bundle import / export** — `traceary bundle export`, `traceary bundle import`

The `traceary doctor` JSON envelope (`sections` / `summary` / `exit_code` / per-check fields), `traceary list --blocks --json` (`workspace_breakdown`; formerly `traceary timeline --json`), and the structured-text `traceary context --handoff` field labels (formerly `traceary session handoff`) are all part of the public contract. They are golden-tested under `presentation/cli/testdata/` — see [JSON and snapshot contract tests](./operations/json-contract-tests.md) for the contract test workflow.

`traceary store compact --projection-rebuild` stdout is two JSON shapes. Branch on the additive `result_kind` field (`generation` for start/replace, `run` for a matching-hash resume). Do not sniff structural fields. `--projection-abort` is a separate abandon object and has no `result_kind`.

`traceary doctor` defaults to exit code `0` for all-pass reports, `1` when any check fails, and `2` for warning-only reports. Automation that treats warnings as operator-visible drift but not a broken install should pass `--warnings-ok`; in that mode warning-only reports exit `0`, failures still exit `1`, and the JSON `summary` / per-check severities remain unchanged.

> Public commands that are TTY-only (currently `traceary memory inbox review`) document the TTY requirement explicitly and exit with a non-zero code that names the scripted fallback when stdin/stdout is not a TTY. Adding a new TTY-only public command requires a documented batch fallback path.

### Admin / maintenance commands (v0.15)

Admin commands are operator-facing maintenance surfaces. They are still listed in `--help` and documented in the CLI reference, but they are not part of the daily read path. Admin commands may evolve faster than public commands when the affected audience is operators only, but they still follow the deprecation notice expectations below.

Admin commands as of v0.35:

- **Store administration** — `traceary store backup create`, `traceary store backup restore`, `traceary store compact` (including `--archive`, `--archive-verify`, `--archive-restore`, `--retention-plan`, `--retention-apply`, `--projection-rebuild`, `--projection-abort`), `traceary store compact rollback`
- **Durable memory admin** — `traceary memory admin extract`, `traceary memory admin import codex`, `traceary memory admin import instructions`, `traceary memory admin export`, `traceary memory admin activate`, `traceary memory admin hygiene scan`, `traceary memory admin hygiene apply`, `traceary memory admin supersede`, `traceary memory admin expire`, `traceary memory admin set-validity`
- **Report administration** — `traceary report workspace-identity`

### Plumbing / hidden / deprecated commands (v0.15)

These commands are hidden from `traceary --help`. In v0.15 the hidden surface has two groups:

- **Removed names (no stubs)** — retired paths such as the former top-level aliases (v0.14.0), flat memory aliases (v0.15.0), and the entire `traceary integration` subtree (removed in v0.25.0, #1266) no longer register migration stubs. They return Cobra's unknown-command / unknown-subcommand error with a non-zero exit. See the historical removal log below.
- **Hook runtime entrypoints** — internal commands called by packaged Traceary hook scripts.

Hidden runtime entrypoints called by packaged Traceary hook scripts (registered with `Hidden: true`, no stderr deprecation notice):

- `traceary hook session`, `traceary hook audit`, `traceary hook compact`, `traceary hook subagent-start`, `traceary hook subagent-stop`, `traceary hook prompt`, `traceary hook transcript` — invoked from hook scripts written out by `traceary hooks install`.
- `traceary hooks helper json-get`, `traceary hooks helper build-failure-output`, `traceary hooks helper normalize-git-remote` — internal helpers used by the same packaged hook scripts.

Stability and deprecation expectations for these runtime entrypoints:

- They are an internal contract between the Traceary binary and the hook configs it generates. Operators and external scripts should not invoke them directly; the canonical operator-facing entrypoint is `traceary hooks install` (use `--dry-run` to preview), and reinstalling regenerates hook configs that match the installed Traceary version.
- The command path and argument shape stay stable across patch releases (`v0.N.x`).
- Across minor boundaries (`v0.N.0` → `v0.(N+1).0`) and across `v1.x` minors once v1.0 ships, they may be renamed, removed, or have their argument shape changed without going through the public stderr deprecation flow, provided the new minor's `traceary hooks install` regenerates compatible scripts and the changelog calls out that hooks must be reinstalled to upgrade.
- Adding a new hidden runtime entrypoint follows the same rule: it is allowed at any minor boundary as long as it is paired with a same-version `traceary hooks install` update.

Currently deprecated:

- none.

### Pillar inventory (v0.35)

#1692 walked every visible public/admin leaf against the two pillars (記録 = capture / summarise / compress / evict; 記憶 = consolidate and supply automatically). Hidden hook entrypoints stay plumbing (see below). The shipped table is `presentation/cli/pillar_inventory.go`; a test fails if a visible action is added without a row.

A command is removed only for empty backing data, duplication, or serving no pillar. Usage counts are not grounds. Remaining groups from the #1870 97→29 keep-list were never in the v0.34 deprecation registry, so v0.35 does not delete them. `list --follow` landed in v0.42.0 (#2068). `list --blocks` landed in v0.42.0 (#2069). `hooks install --dry-run` landed in v0.42.0 (#2070). `memory search --all` landed in v0.42.0 (#2071). Doctor absorbed `store capacity` in v0.42.0 (#2072). `context --handoff` / `--compact-only` landed in v0.42.0 (#2073). `store compact --archive` / `--retention-plan` landed in v0.42.0 (#2074). `doctor --alias-add` / `--alias-remove` / `--alias-list` landed in v0.42.0 (#2075). `store init` folded into auto-init and `doctor --fix` in v0.42.0 (#2076). `store search-projection` folded into `doctor --fix` / `store compact --projection-rebuild` / `--projection-abort` in v0.42.0 (#2077). `replay` was removed in v0.42.0 (#2078), superseding the earlier re-keep: reading is `report` / `context` / `list`, and `bundle export` is the machine-portable export.

Historical removal log:

- Removed in v0.43.0 (#2123): `traceary memory decay`. Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-18, recorded in #2108). Session-end hooks apply decay when `TRACEARY_MEMORY_DECAY` is on (default) with window `TRACEARY_MEMORY_DECAY_AFTER`. The operator trigger is `traceary doctor --fix`. Recovery is `traceary memory inbox restore`.
- Removed in v0.43.0 (#2122): `traceary session gc` and `traceary session repair-one-shot`. Invocations fail as unknown subcommands with a non-zero exit and no `DEPRECATED` notice. Admin-tier leaves; operator-flow notice is sufficient. Stale still-open sessions are closed by hook opportunistic GC and `traceary doctor --fix` (default 24h window; the custom `--stale-after` knob is gone). Historical one-shot rows stay as recorded; there is no absorb destination for evidence-manifest repair.
- Removed in v0.42.0 (#2077): `traceary store search-projection` (start/resume/status/abort/probe). Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Parked recovery is `traceary doctor --fix`. A new generation or an in-flight rebuild resume is `traceary store compact --projection-rebuild` (same budget flags). Abort is `traceary store compact --projection-abort`. Lifecycle/budget inspection stays on `traceary doctor`.
- Removed in v0.42.0 (#2078): `traceary replay`. Invocations fail as an unknown command with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17) and supersedes the earlier note that replay stayed as the only single-file HTML export. Use `traceary report` / `traceary context` / `traceary list` for period reading, and `traceary bundle export` for a machine-portable copy.

- Removed in v0.42.0 (#2076): `traceary store init`. Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Empty stores still auto-init on first write or `traceary doctor`. Data-dependent offline migrations apply with `traceary doctor --fix` (can take minutes). Large-store default doctor stays filesystem-metadata-only.
- Removed in v0.42.0 (#2075): `traceary store workspace-alias` (add/list/remove). Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary doctor --alias-add` / `--alias-remove` / `--alias-list` (same reviewed-alias rows; add still requires `--session`, `--workspace`, `--reviewed-by`). `doctor --fix` does not invent aliases. Existing alias rows and `report workspace-identity` grouping are unchanged.
- Removed in v0.42.0 (#2074): `traceary store archive` (create/verify/restore, including `--delete-after-verify`) and `traceary store retention` (`files plan` / `files apply`). Invocations fail as unknown subcommands with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary store compact --archive` / `--archive-verify` / `--archive-restore` (same verify-before-delete) and `traceary store compact --retention-plan` / `--retention-apply` (same immutable plan and `--confirm-plan-id`). Default compact rewrite is unchanged. Hook `archive_then_gc` still calls the usecase internally.
- Removed in v0.42.0 (#2073): `traceary session handoff` (including `--compact-only`). Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary context --handoff` (same TRACEARY HANDOFF field labels) or `traceary context --compact-only` (same resume summary; `--recent` defaults to 3 unless set). Default `context` stays raw events plus `--json`. Internal `ContextUsecase.Handoff` and hook `printCompactSummaryWithOptions` remain.
- Removed in v0.42.0 (#2072): `traceary store capacity`. Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary doctor` (additive `store-capacity` check from the same bounded InspectCapacity path). Default doctor on stores ≥2 GiB stays metadata-only and does not open SQLite or walk dbstat.
- Removed in v0.42.0 (#2071): `traceary memory list`. Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary memory search --all` (same List backend, filters, default workspace scope, ordering, and `--json`). `--all` cannot be combined with a query term.
- Removed in v0.42.0 (#2070): `traceary hooks print`. Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary hooks install --dry-run` (same generated config bytes; `--client` / `--traceary-bin` / `--matcher`).
- Removed in v0.42.0 (#2069): `traceary timeline`. Invocations fail as an unknown command with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary list --blocks` (same gap-detected blocks, #2033 scan-cap disclosure, and `workspace_breakdown` JSON; `--gap` moved onto `list`).
- Removed in v0.42.0 (#2068): `traceary tail`. Invocations fail as an unknown command with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `traceary list --follow` (same stream, filters, and rendering; `--follow-session` moved onto `list`).
- Removed in v0.42.0 (#2061): `traceary sessions` (including `--snapshot` / `--snapshot --json`). Invocations fail as an unknown command with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Use `list` / `search` / `context` / `report` / `session handoff`. Internal `Session.List` remains for hook workspace canonicalization; `list_sessions.sql` is kept for that caller.
- Removed in v0.42.0 (#2057): `traceary session latest` (including `--active`) and `traceary session list`. Invocations fail as unknown subcommands with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window (owner decision 2026-08-17). Open-session identity is delivered in hook messages (`[Traceary] Session <id>`); recent work is read with `list` / `search` / `context`; period summaries use `report`. Internal `Active` / `Latest` / `List` queries remain for handoff, hooks, context, and memory extract.
- Removed in v0.36.0 after the v0.35 deprecation (#1692 / #1870): `traceary memory store remember`. Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. Use `traceary memory store propose` (`status=candidate`). The skill `traceary-memory-remember` already lands on `propose`.
- Removed in v0.36.0 (#1704): `traceary session active`. Invocations fail as an unknown subcommand with a non-zero exit and no `DEPRECATED` notice. Use `traceary session latest --active` (same stale defaults: 24h, `--stale-after`, `--allow-stale`). `--stale-after` and `--allow-stale` without `--active` are rejected. This is a same-minor fold onto an existing command: the behaviour is unchanged, only the spelling.
- Removed in v0.35.0 (#1872): the store-size reduction command family is folded into `traceary store compact`. Invocations fail as unknown commands with a non-zero exit and no `DEPRECATED` notice. Removed: `traceary store gc`, `traceary store dedupe` / `content-events`, `traceary store retention plan|apply|restore` (raw-body retention; `store retention files` remains), `traceary store payload-rehearsal` (`preview|run|resume|scrub|rollback`), `traceary store payload-backfill` (`preview|run|resume|status`), `traceary store search-retire`, and `traceary store compact plan|apply|resume|status`. Use `traceary store compact` (optional `--force`, `--keep-days`) and `traceary store compact rollback RUN_ID`. `traceary store search-projection` is unchanged. The old file is the archive until rollback is discarded.
- Removed in v0.35.0 (#1871): `traceary mcp-server`, the `presentation/mcpserver` package, its nine tools, and every shipped host package's MCP server declaration (Claude/Codex/Gemini/Grok/Kimi/Antigravity). Invocations fail as an unknown command (`unknown command "mcp-server"`) with a non-zero exit and no `DEPRECATED` notice. This is an explicit policy exception to the one-minor deprecation window: MCP was public and was not listed in the v0.34 "Currently deprecated" registry. Removal is an owner decision on #1693 justified by "nothing is lost" evidence — 16 historical MCP writes out of 659,304 events (0.0024%, last write 2026-07-19); hook capture remains shell (`traceary hook …`); every shipped host has a shell; skills route through the CLI (#1875). Use the CLI for the same work (for example `session handoff` / `context`, `search`, `list`, `report`, and the memory inbox/store/admin commands). Claude `hooks.json` keeps `matcher: mcp__.*` so audits of *other* servers' tool calls continue.
- Removed in v0.35.0 (#1869): `traceary session tree` and `traceary session lineage`. Invocations fail as unknown subcommands with a non-zero exit and no `DEPRECATED` notice. `traceary sessions --snapshot` / `--snapshot --json` remain the script-friendly active-session view.
- Removed in v0.35.0 after the v0.34 deprecation (#1688 / #1690): `traceary top` (including `traceary top --snapshot` / `--snapshot --json`). Invocations fail as an unknown command with a non-zero exit and no `DEPRECATED` notice. Use `traceary sessions` (or `traceary sessions --snapshot` / `--snapshot --json`); the snapshot contracts are unchanged.
- Removed in v0.35.0 after the v0.34 announcement (#1765 / #1766): the interactive `traceary sessions` live dashboard. Bare `traceary sessions` is now a plain text command and is byte-identical to `traceary sessions --snapshot` for every caller. `sessions --snapshot` / `--snapshot --json` remain unchanged.
- Removed in v0.35.0 after the v0.34 announcement (#1687 / #1764): `traceary tui`, `traceary dashboard`, and the bare interactive TTY default that opened the operator cockpit. Bare `traceary` always prints help (TTY and non-TTY). Use `traceary sessions --snapshot` for the surviving script-friendly view of related session data. The orphan local state file `~/.local/state/traceary/cockpit.json` (or `$XDG_STATE_HOME/traceary/cockpit.json`) is safe to delete manually; Traceary no longer reads or writes it.
- Removed in v0.35.0 after the v0.34 deprecation (#1689 / #1691): `traceary memory admin graph add` and `traceary memory admin graph list` (no replacement; the reference store had zero `memory_edges` rows). The `memory_edges` table remains for gc and bundle export/import.
- Removed in v0.35.0 after the v0.34 deprecation (#1689 / #1691): `traceary session label`, `traceary session list --label`, the `LABEL` column in `session list` text output, and the `label` field in `session list` JSON output (no replacement; the reference store had zero labelled sessions). The `sessions.label` column remains in the store schema.
- Replaced in v0.35.0 after the v0.34 announcement (#1717 / #1775): `traceary search --json` top-level array → `{"events": [...], "sessions": [...]}` object. Both keys are always present; empty arrays mean the tier returned no hits.
- Removed in v0.14.0 after earlier deprecation: `traceary init` → `traceary store init`, `traceary backup` → `traceary store backup ...`, `traceary gc` → `traceary store gc`, `traceary handoff` → `traceary session handoff`, `traceary compact-summary` → `traceary session handoff --compact-only`, and the retired `traceary integration codex install` helper → Codex official `/plugins` flow.
- Removed in v0.15.0 after the v0.14 compatibility window: `traceary memory accept`, `traceary memory reject`, `traceary memory remember`, `traceary memory propose`, `traceary memory distill`, `traceary memory extract`, `traceary memory supersede`, `traceary memory expire`, `traceary memory set-validity`, `traceary memory import codex`, `traceary memory import instructions`, `traceary memory export`, `traceary memory activate`, `traceary memory hygiene scan`, `traceary memory hygiene apply`, `traceary memory graph add`, and `traceary memory graph list`. Use the canonical `memory inbox` / `memory store` / `memory admin` paths documented in the CLI reference.
- Removed in v0.15.0 after the v0.14 cleanup-only window: `traceary integration codex uninstall` → Codex official `/plugins` flow plus manual cleanup steps in `docs/integrations/codex-plugin.md`.
- Hidden in v0.20.0 and fully removed in v0.25.0 (#1266): the `traceary integration` command subtree (the `integration` parent, the `codex` group, and the former migration stubs for install/uninstall). Invocations now fail as unknown commands; use Codex CLI's official `/plugins` flow.

## Deprecation notice expectations

When a public or admin command path, flag, JSON field name, or output shape needs to change in a way that affects callers, Traceary follows a single deprecation flow. The same single notice form also covers a default-behaviour change; in that case, the subject named in the notice is the behaviour rather than the command path.

### Stderr notice

Every deprecated command emits exactly one stderr line on each invocation:

```
DEPRECATED: this command is deprecated, use `<canonical replacement>` instead. Removal target: v<X.Y>.
```

The Japanese form follows the same structure under `TRACEARY_LANG=ja`:

```
DEPRECATED: このコマンドは非推奨です。代わりに `<canonical replacement>` を使用してください。削除予定: v<X.Y>。
```

Notice rules:

- The notice must name the canonical replacement command (with subcommand path, e.g. `traceary memory admin hygiene scan`, not just the parent group).
- When a surface is removed without a successor, the notice says that there is no replacement instead of naming one; the deprecation entry must state the evidence that nothing is lost.
- The notice must name the removal target version (`v0.15`, `v1.0`, etc.).
- The notice goes to **stderr** so stdout / `--json` / NDJSON output stays byte-for-byte identical to the canonical command. Cobra's built-in `Deprecated` field routes its warning through stdout, so Traceary emits the notice itself instead.
- A single invocation must not emit more than one notice — even when the deprecated command is a parent group whose subcommand is the actual entry point, the notice fires once for the executing leaf and names the precise canonical leaf.
- The notice fires when the command actually runs, so it is attached to the run step rather than to a pre-run hook. Cobra resolves `--help`, rejects invalid arguments, and validates required flags before the command runs, and none of those paths emit a notice; in exchange, a deprecated command's `Short` and `Long` text must name the deprecation and the removal target so `--help` still tells the caller.

### Stdout / JSON / NDJSON compatibility

For the duration of the deprecation window, the deprecated command must keep emitting:

- the same stdout text bytes as before,
- the same `--json` output (same field names, same field order where the contract documents one, same NDJSON line shape),
- the same exit codes,
- the same `--id-only` byte shape.

Help and usage text is deliberately outside this guarantee. The notice rule above *requires* a deprecated command to change its `Short` and `Long`, which changes the parent's command listing, so freezing help bytes would make the flow self-contradictory. Automation must not parse `--help`; it is the one output shape that announces deprecations rather than preserving them.

Adding a new optional flag to a deprecated alias is allowed only when the canonical replacement has the same flag (so callers can move without rewriting their argument list).

When a flag itself is being deprecated (rather than the whole command), the same stderr notice form is used. The flag must keep its old behavior for the deprecation window, the notice names the replacement flag, and the change appears in `CHANGELOG.md` under "Deprecated".

### Documentation requirements

Every deprecation must update three places in the same change:

1. The CLI reference (`docs/cli/README.md` and `docs/cli/README.ja.md`) — annotate the deprecated path with its replacement and removal target.
2. The changelog (`CHANGELOG.md` and `CHANGELOG.ja.md`) — add an entry under "Deprecated" or "Changed" naming the path, the replacement, and the removal target.
3. The relevant operations / planning doc when the change is part of a larger surface plan (for example, the [memory command surface plan](./operations/memory-command-surface.md) for the memory tree restructure).

## Compatibility windows

### One-minor compatibility window

The default deprecation window is **one minor release**. A command, flag, or JSON shape deprecated in v0.N.0 stays working with the deprecation notice through every v0.N.x patch and is removed in v0.(N+1).0.

Examples that follow this default:

- The grouped memory tree introduced in v0.14.0 (`memory inbox` / `memory store` / `memory admin`) kept the flat verbs (`memory remember`, `memory propose`, `memory accept`, ...) as hidden deprecated aliases through v0.14.x and removed them in v0.15.0. See the [memory command surface plan](./operations/memory-command-surface.md).
- The retired Codex install helper kept its uninstall counterpart as a hidden cleanup-only command in v0.14.0; that cleanup-only command was removed in v0.15.0.

### Longer windows for breaking output changes

When the change affects a heavily scripted output (a public `--json` envelope, a structured-text contract such as `traceary context --handoff`, or a public command path that AI skills wire in directly), the deprecation window may be extended beyond one minor at the maintainers' discretion. The decision is recorded in the originating issue and in the changelog entry. A longer window is the exception, not the default.

Announced under this rule:

- **`traceary search --json` became an object in v0.35.0 (#1717 / #1775).** v0.34.0 announced that the top-level event array would become `{"events": [...], "sessions": [...]}` so session-tier hits could ship without interleaving session rows among event rows. v0.34.x kept the array and reported omitted session hits on stderr; v0.35.0 completes the replacement. Both keys are always present (empty arrays when a tier has no hits).

### When no window is required

A change does not require a deprecation window when it is purely additive:

- adding a new public subcommand,
- adding a new optional flag,
- adding a new optional field at the end of a JSON object (consumers must tolerate unknown fields),
- adding a new section to `traceary doctor`.

Removing or renaming any of those is a breaking change and goes through the deprecation flow.

## v0 vs v1 removal policy

### v0.x series

Traceary is currently in the `v0.x` series. The intent of v0.x is to let the surface stabilize before v1.0 with a predictable, advertised cadence:

- **Public commands**: breaking changes are allowed at minor boundaries (`v0.N.0` → `v0.(N+1).0`) using the one-minor compatibility window above. Patch releases (`v0.N.x`) are non-breaking.
- **Admin commands**: same default as public, but the maintainers reserve the right to use a faster cadence (deprecation in v0.N, removal in v0.(N+1)) when the audience is operators only.
- **Plumbing / hidden / deprecated commands**: removed at the minor release named in their stderr notice.

The aliases retired in v0.14.0 (`traceary init`, `traceary backup`, `traceary gc`, `traceary handoff`, `traceary compact-summary`) followed this model: deprecation in v0.9.0, removal in v0.14.0, with the deprecation notice and replacement guidance shipped continuously between those releases.

### v1.0 onward

Once Traceary releases v1.0:

- **Public commands**: stable across the entire `v1.x` series. Breaking changes happen only at major boundaries (`v1.x` → `v2.0`). Minor releases (`v1.0.0` → `v1.1.0`) must remain backwards compatible: existing public command paths, flag names, exit codes, stdout shapes, and documented JSON field names keep working byte-for-byte with the next minor of `v1.x`.
- **Admin commands**: still backwards compatible across `v1.x` minor releases, but admin-only flag additions or flag renames are allowed at minor boundaries provided the deprecation flow above is followed (stderr notice for at least one minor before removal).
- **Plumbing / hidden / deprecated commands**: removed at the minor release named in their stderr notice, same as v0.x.
- **Major-version migrations**: when a future `v2.0` is planned, the `v1.x` series ships a final pre-v2 minor (`v1.last`) that emits the stderr deprecation notice for everything that will change in `v2.0`. The `v2.0` release notes restate the same set so external callers have a single migration list.

In short: v0.x lets the surface evolve at minor boundaries with one-minor overlap; v1.x freezes the public surface across the entire major; v2.0 (if and when it happens) is the next time the public surface may break.

## Out of scope

This policy describes the CLI surface. The following are documented separately:

- Hook capture stability — see the [hook contract](./hooks/contract.md) and [host coverage matrix](./hooks/host-coverage.md).
- Storage / SQLite schema migrations — see the [storage model](./storage/README.md).
- Host-native memory activation marker compatibility — see the [host-native memory activation contract](./architecture/host-native-memory-activation.md).

## Related docs

- [CLI reference](./cli/README.md)
- [Memory command surface plan](./operations/memory-command-surface.md)
- [JSON and snapshot contract tests](./operations/json-contract-tests.md)
- [Release guide](./release/README.md)
- [Repository README](../README.md)
