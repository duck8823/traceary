# Memory command surface plan (v0.14)

[日本語](./memory-command-surface.ja.md)

`traceary memory ...` has grown to 18 direct subcommands across review, write, lifecycle, hygiene, graph, host activation, and import/export concerns. The flat list is no longer easy to scan, and several admin-only paths sit next to daily-use commands without any visual grouping.

This document is the v0.14 baseline that subsequent v0.14 sub-issues build on. It is documentation only — no runtime behavior changes in this issue.

## Goals

- Group memory commands by intent: daily read, inbox curation, durable write, and admin/host-side operations.
- Keep the everyday read path (`memory search`, `memory show`) unchanged so existing scripts and muscle memory keep working.
- Move admin/host-side and lifecycle commands behind clearer namespaces (`memory inbox`, `memory store`, `memory admin`).
- Preserve every old path as a hidden deprecated alias until v0.15 so external scripts and AI integrations have one release of overlap.

## Current command tree (v0.13.1)

Reproduced from `traceary memory --help` on the v0.13.1 build:

| Current path | Purpose | Notes |
|---|---|---|
| `memory search` | Search durable memories | Daily read |
| `memory show` | Show durable memory details | Daily read |
| `memory list` | List durable memories | Daily read |
| `memory inbox list` | List candidate durable memories awaiting review | Already nested |
| `memory inbox accept` | Accept every candidate by id | Already nested |
| `memory inbox reject` | Reject every candidate by id | Already nested |
| `memory accept` | Accept a candidate durable memory (top-level form) | Duplicates `memory inbox accept` |
| `memory reject` | Reject a candidate durable memory (top-level form) | Duplicates `memory inbox reject` |
| `memory propose` | Record a candidate durable memory | Inbox write |
| `memory distill` | Distill candidate memories into an accepted fact | Inbox curation |
| `memory extract` | Extract candidate durable memories from an existing session | Inbox feeder |
| `memory remember` | Record an accepted durable memory | Durable write |
| `memory supersede` | Replace an accepted durable memory | Durable write |
| `memory expire` | Expire an active durable memory | Durable lifecycle |
| `memory set-validity` | Set the content validity window on a durable memory | Durable lifecycle |
| `memory import codex` | Import `~/.codex/memories/*.md` as durable-memory candidates | Admin / host-side (executable leaf under `memory import`) |
| `memory import instructions` | Import bullets from a host instruction file as durable-memory candidates | Admin / host-side (executable leaf under `memory import`) |
| `memory export` | Export accepted memories into a host instruction file | Admin / host-side |
| `memory activate` | Plan host-native durable-memory activation | Admin / host-side |
| `memory hygiene scan` | Scan accepted/candidate memories for hygiene issues | Admin |
| `memory hygiene apply` | Apply hygiene suggestions by memory id | Admin |
| `memory graph add` | Record a typed relationship between two memories | Admin |
| `memory graph list` | List memory edges matching the given filters | Admin |

## Target command tree (v0.14)

```
memory
├── search           # daily read (unchanged)
├── show             # daily read (unchanged)
├── list             # daily read (unchanged)
├── inbox            # candidate review surface
│   ├── list
│   ├── accept
│   └── reject
├── store            # deliberate write/store workflows
│   ├── remember
│   ├── propose
│   └── distill
└── admin            # extraction + host-side + maintenance + lifecycle
    ├── extract
    ├── import       # parent group (no executable form)
    │   ├── codex
    │   └── instructions
    ├── export
    ├── activate
    ├── hygiene
    │   ├── scan
    │   └── apply
    ├── graph
    │   ├── add
    │   └── list
    ├── supersede
    ├── expire
    └── set-validity
```

### Why these three groupings

- `memory inbox` is the candidate review surface only — listing what is awaiting review and approving or rejecting individual ids. Anything that *writes* a candidate (or an accepted row) lives under `memory store`; anything that *transforms* existing rows lives under `memory admin`.
- `memory store` is the deliberate write/store surface. `remember` writes an accepted row, `propose` writes a candidate row, and `distill` writes a new accepted row out of one or more existing candidates. Grouping all three together keeps every "this command writes a durable-memory row" verb in one namespace, regardless of the lifecycle status of the resulting row.
- `memory admin` collects everything else: extraction (`extract`) which mines candidates from existing sessions, host-side I/O (`import`, `export`, `activate`), maintenance (`hygiene`, `graph`), and the lifecycle verbs that mutate already-stored rows (`supersede`, `expire`, `set-validity`). These are operator-facing concerns, not part of the daily read path.

### Why `search`, `show`, and `list` stay top-level

`memory search` and `memory show` are the highest-frequency commands in dogfood data and across the AI integrations. They are the daily read path and breaking them would force users and skills to update wired-in command strings for no operational benefit. `memory list` is in the same daily-read category and keeps its top-level position alongside them.

The acceptance criteria for this issue explicitly mention `search` and `show`. `list` follows the same principle and is intentionally included in the daily read tier here. If implementation in a follow-up sub-issue uncovers a concrete reason to demote any of these (for example, an unavoidable flag conflict between `memory list` and a grouped `memory inbox list`), the implementing PR may revisit the placement and update this document accordingly.

## Path mapping

| Old path | New canonical path | Status in v0.14 |
|---|---|---|
| `memory search` | `memory search` | Unchanged |
| `memory show` | `memory show` | Unchanged |
| `memory list` | `memory list` | Unchanged (revisit only if implementation forces it) |
| `memory inbox list` | `memory inbox list` | Unchanged |
| `memory inbox accept` | `memory inbox accept` | Unchanged |
| `memory inbox reject` | `memory inbox reject` | Unchanged |
| `memory accept <memory-id>` | `memory inbox accept <memory-id>` | Hidden deprecated alias (see signature note below) |
| `memory reject <memory-id>` | `memory inbox reject <memory-id>` | Hidden deprecated alias (see signature note below) |
| `memory remember` | `memory store remember` | Hidden deprecated alias |
| `memory propose` | `memory store propose` | Hidden deprecated alias |
| `memory distill` | `memory store distill` | Hidden deprecated alias |
| `memory extract` | `memory admin extract` | Hidden deprecated alias |
| `memory supersede` | `memory admin supersede` | Hidden deprecated alias |
| `memory expire` | `memory admin expire` | Hidden deprecated alias |
| `memory set-validity` | `memory admin set-validity` | Hidden deprecated alias |
| `memory import codex` | `memory admin import codex` | Hidden deprecated alias (see signature note below) |
| `memory import instructions` | `memory admin import instructions` | Hidden deprecated alias (see signature note below) |
| `memory export` | `memory admin export` | Hidden deprecated alias |
| `memory activate` | `memory admin activate` | Hidden deprecated alias |
| `memory hygiene scan` | `memory admin hygiene scan` | Hidden deprecated alias |
| `memory hygiene apply` | `memory admin hygiene apply` | Hidden deprecated alias |
| `memory graph add` | `memory admin graph add` | Hidden deprecated alias |
| `memory graph list` | `memory admin graph list` | Hidden deprecated alias |

## Hidden deprecated aliases

For every old path marked above, v0.14 keeps the command working but registers it as `Hidden: true` so it does not appear in `traceary memory --help`. Invoking the old path should:

- complete the same operation as before (no behavior change),
- emit a single deprecation notice on stderr that names the canonical replacement,
- leave stdout / JSON output bytes unchanged so scripted callers do not break.

The aliases exist so the v0.13 → v0.14 upgrade does not silently break user scripts, AI skill packs, or example snippets in older docs. They are scheduled for removal in v0.15, matching the rolling one-release deprecation policy already used for the top-level command aliases retired in #918.

### Signature-preservation requirements

Two old → new mappings cross a signature boundary, not just a name change. The implementing PRs in this milestone must preserve the v0.13.1 caller contract:

- `memory accept <memory-id>` and `memory reject <memory-id>` accept the memory id as a **positional argument** (`exactArgsLocalized(1)`) and expose `--id-only` for scripted callers that want only the resulting id on stdout. The current `memory inbox accept` / `memory inbox reject` commands instead take `--ids` (a comma-separated list, repeatable) and do not have `--id-only`.
- `memory import` is a parent group whose **executable leaves are `memory import codex` and `memory import instructions`** — there is no `traceary memory import` command on its own. The follow-up that introduces `memory admin import` must also expose `memory admin import codex` and `memory admin import instructions` as the canonical leaves and route both old leaves into them, not just the parent name.

To satisfy both at the same time without breaking scripts, the v0.14 plan is:

1. **Canonical `memory inbox accept` / `memory inbox reject` gain positional id support and `--id-only`.** Sub-issue #923 already tracks adding the positional form to the canonical inbox commands. `--id-only` is added on those canonical commands so the surface that scripts migrate to is a strict superset of the old one. The existing `--ids` batch flag stays.
2. **Hidden aliases preserve every previous flag and arg shape.** `memory accept <memory-id>` and `memory reject <memory-id>` keep their positional argument, keep `--id-only`, keep `--confidence` (accept only), keep `--db-path`, and keep `--json`. They must not gain new required flags during the deprecation window.
3. **`memory import codex` and `memory import instructions` keep every flag they have today** (`--db-path`, `--root`, `--workspace`, `--watch`, `--interval`, `--json` for `codex`; the existing flag set for `instructions`). The hidden alias only re-routes the path, not the flag surface.

If a follow-up sub-issue cannot satisfy one of these requirements (for example, a flag conflict on the canonical inbox command), the implementing PR must update this document before merging — not after — so the deprecation contract stays single-sourced.

## Removal timeline

- v0.14.0: introduce the new tree, register every old path as a hidden deprecated alias, ship the bilingual deprecation notice on stderr.
- v0.14.x patch releases: no surface change. Bug fixes only.
- v0.15.0: remove every alias listed above. Old paths exit with a usage error pointing at the canonical replacement, mirroring the #918 / #919 retirement model.

## Out of scope for this issue

- Implementation of the new `memory store` and `memory admin` Cobra groups.
- Hidden alias registration in `presentation/cli`.
- Updating docs under `docs/cli/` and `docs/memory/` to reference the new paths.
- Updating AI integration manifests (Claude / Codex / Gemini packages) that wire memory commands into hooks or skills.

Each of those is tracked by its own sub-issue under the v0.14 parent. This document is the shared baseline they reference.

## Related docs

- [CLI reference](../cli/README.md)
- [Durable memory guide](../memory/README.md)
- [Host-native memory activation contract](../architecture/host-native-memory-activation.md)
