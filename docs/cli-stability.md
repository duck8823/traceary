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

Current public commands, including compatibility aliases introduced after v0.15, are grouped by intent:

- **Event recording** — `traceary log`, `traceary audit`
- **Read / inspection** — `traceary list`, `traceary search`, `traceary tail`, `traceary timeline`, `traceary show`, `traceary context`, `traceary sessions` (and `traceary sessions --snapshot` / `--snapshot --json`), plus the permanent compatibility alias `traceary top` (including `traceary top --snapshot` / `--snapshot --json`)
- **Sessions** — `traceary session start`, `traceary session end`, `traceary session handoff` (including `--compact-only`), `traceary session list`, `traceary session tree`, `traceary session lineage`, `traceary session label`, `traceary session latest`, `traceary session active`
- **Durable memory daily read** — `traceary memory list`, `traceary memory search`, `traceary memory show`
- **Durable memory inbox** — `traceary memory inbox list`, `traceary memory inbox accept`, `traceary memory inbox reject`, `traceary memory inbox review` (TTY-only)
- **Durable memory store** — `traceary memory store remember`, `traceary memory store propose`, `traceary memory store distill`
- **Hooks** — `traceary hooks print`, `traceary hooks install`, `traceary hooks guide`, `traceary completion`
- **MCP server** — `traceary mcp-server`
- **Diagnostics** — `traceary doctor` (alias `traceary status`)
- **Replay / archive** — `traceary replay`
- **Bundle import / export** — `traceary bundle export`, `traceary bundle import`

The `traceary doctor` JSON envelope (`sections` / `summary` / `exit_code` / per-check fields), `traceary sessions --snapshot --json` / `traceary top --snapshot --json` envelope (`sessions` / `failures` / `recent_commands` / `candidates` / `stale_memories`), `traceary timeline --json` (`workspace_breakdown`), `traceary session tree --json` lineage fields, and the structured-text `traceary session handoff` field labels are all part of the public contract. They are golden-tested under `presentation/cli/testdata/` — see [JSON and snapshot contract tests](./operations/json-contract-tests.md) for the contract test workflow.

`traceary top` is not deprecated in v0.19.0; it remains a permanent compatibility alias for every `traceary sessions` form. Removing it later would require the deprecation flow below. The v0.19.0 text snapshot intentionally inserts `name="..."` before the raw `workspace=` / `agent=` metadata for readability; scripts that need a stable machine contract should prefer the unchanged `--json` envelope or parse text fields by key rather than by position.

> Public commands that are TTY-only (currently `traceary memory inbox review`) document the TTY requirement explicitly and exit with a non-zero code that names the scripted fallback when stdin/stdout is not a TTY. Adding a new TTY-only public command requires a documented batch fallback path.

### Admin / maintenance commands (v0.15)

Admin commands are operator-facing maintenance surfaces. They are still listed in `--help` and documented in the CLI reference, but they are not part of the daily read path. Admin commands may evolve faster than public commands when the affected audience is operators only, but they still follow the deprecation notice expectations below.

Admin commands in v0.15:

- **Store administration** — `traceary store init`, `traceary store backup create`, `traceary store backup restore`, `traceary store gc`
- **Session administration** — `traceary session gc` (closes stale sessions; visible under the `session` namespace and registered alongside the public session subcommands, but treated as an admin-tier maintenance entrypoint)
- **Durable memory admin** — `traceary memory admin extract`, `traceary memory admin import codex`, `traceary memory admin import instructions`, `traceary memory admin export`, `traceary memory admin activate`, `traceary memory admin hygiene scan`, `traceary memory admin hygiene apply`, `traceary memory admin graph add`, `traceary memory admin graph list`, `traceary memory admin supersede`, `traceary memory admin expire`, `traceary memory admin set-validity`

### Plumbing / hidden / deprecated commands (v0.15)

These commands are hidden from `traceary --help`. In v0.15 the hidden surface has two groups:

- **Migration-error stubs for removed names** — former top-level aliases removed in v0.14.0, flat memory aliases removed in v0.15.0, and the retired `integration codex install` / `integration codex uninstall` paths still register hidden stubs that return targeted non-zero migration errors. They are not working aliases: they do not preserve legacy flags, do not execute the old behavior, and exist only so old invocations receive a concrete replacement instead of Cobra's generic unknown-command output.
- **Hook runtime entrypoints** — internal commands called by packaged Traceary hook scripts.

Hidden runtime entrypoints called by packaged Traceary hook scripts (registered with `Hidden: true`, no stderr deprecation notice):

- `traceary hook session`, `traceary hook audit`, `traceary hook compact`, `traceary hook subagent-start`, `traceary hook subagent-stop`, `traceary hook prompt`, `traceary hook transcript` — invoked from hook scripts written out by `traceary hooks print` / `traceary hooks install`.
- `traceary hooks helper json-get`, `traceary hooks helper build-failure-output`, `traceary hooks helper normalize-git-remote` — internal helpers used by the same packaged hook scripts.

Stability and deprecation expectations for these runtime entrypoints:

- They are an internal contract between the Traceary binary and the hook configs it generates. Operators and external scripts should not invoke them directly; the canonical operator-facing entrypoint is `traceary hooks print` / `traceary hooks install`, and reinstalling regenerates hook configs that match the installed Traceary version.
- The command path and argument shape stay stable across patch releases (`v0.N.x`).
- Across minor boundaries (`v0.N.0` → `v0.(N+1).0`) and across `v1.x` minors once v1.0 ships, they may be renamed, removed, or have their argument shape changed without going through the public stderr deprecation flow, provided the new minor's `traceary hooks install` regenerates compatible scripts and the changelog calls out that hooks must be reinstalled to upgrade.
- Adding a new hidden runtime entrypoint follows the same rule: it is allowed at any minor boundary as long as it is paired with a same-version `traceary hooks print` / `traceary hooks install` update.

Historical removal log:

- Removed in v0.14.0 after earlier deprecation: `traceary init` → `traceary store init`, `traceary backup` → `traceary store backup ...`, `traceary gc` → `traceary store gc`, `traceary handoff` → `traceary session handoff`, `traceary compact-summary` → `traceary session handoff --compact-only`, and the retired `traceary integration codex install` helper → Codex official `/plugins` flow.
- Removed in v0.15.0 after the v0.14 compatibility window: `traceary memory accept`, `traceary memory reject`, `traceary memory remember`, `traceary memory propose`, `traceary memory distill`, `traceary memory extract`, `traceary memory supersede`, `traceary memory expire`, `traceary memory set-validity`, `traceary memory import codex`, `traceary memory import instructions`, `traceary memory export`, `traceary memory activate`, `traceary memory hygiene scan`, `traceary memory hygiene apply`, `traceary memory graph add`, and `traceary memory graph list`. Use the canonical `memory inbox` / `memory store` / `memory admin` paths documented in the CLI reference.
- Removed in v0.15.0 after the v0.14 cleanup-only window: `traceary integration codex uninstall` → Codex official `/plugins` flow plus manual cleanup steps in `docs/integrations/codex-plugin.md`.

## Deprecation notice expectations

When a public or admin command path, flag, JSON field name, or output shape needs to change in a way that affects callers, Traceary follows a single deprecation flow.

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
- The notice must name the removal target version (`v0.15`, `v1.0`, etc.).
- The notice goes to **stderr** so stdout / `--json` / NDJSON output stays byte-for-byte identical to the canonical command. Cobra's built-in `Deprecated` field routes its warning through stdout, so Traceary emits the notice itself instead.
- A single invocation must not emit more than one notice — even when the deprecated command is a parent group whose subcommand is the actual entry point, the notice fires once for the executing leaf and names the precise canonical leaf.

### Stdout / JSON / NDJSON compatibility

For the duration of the deprecation window, the deprecated command must keep emitting:

- the same stdout text bytes as before,
- the same `--json` output (same field names, same field order where the contract documents one, same NDJSON line shape),
- the same exit codes,
- the same `--id-only` byte shape.

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

When the change affects a heavily scripted output (a public `--json` envelope, a structured-text contract such as `traceary session handoff`, or a public command path that AI skills wire in directly), the deprecation window may be extended beyond one minor at the maintainers' discretion. The decision is recorded in the originating issue and in the changelog entry. A longer window is the exception, not the default.

### When no window is required

A change does not require a deprecation window when it is purely additive:

- adding a new public subcommand,
- adding a new optional flag,
- adding a new optional field at the end of a JSON object (consumers must tolerate unknown fields),
- adding a new section to `traceary doctor` or a new pane to the canonical `traceary sessions --snapshot` surface (mirrored by the `traceary top --snapshot` compatibility alias).

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

- MCP tool registry stability — see [JSON and snapshot contract tests](./operations/json-contract-tests.md) and the registry snapshot under `presentation/mcpserver/testdata/`.
- Hook capture stability — see the [hook contract](./hooks/contract.md) and [host coverage matrix](./hooks/host-coverage.md).
- Storage / SQLite schema migrations — see the [storage model](./storage/README.md).
- Host-native memory activation marker compatibility — see the [host-native memory activation contract](./architecture/host-native-memory-activation.md).

## Related docs

- [CLI reference](./cli/README.md)
- [Memory command surface plan](./operations/memory-command-surface.md)
- [JSON and snapshot contract tests](./operations/json-contract-tests.md)
- [Release guide](./release/README.md)
- [Repository README](../README.md)
