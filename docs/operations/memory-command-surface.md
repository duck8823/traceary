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
| `memory import` | Import memories from host-native sources as candidates | Admin / host-side |
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
│   ├── reject
│   ├── propose
│   ├── distill
│   └── extract
├── store            # durable write + lifecycle
│   ├── remember
│   ├── supersede
│   ├── expire
│   └── set-validity
└── admin            # host-side + maintenance
    ├── import
    ├── export
    ├── activate
    ├── hygiene
    │   ├── scan
    │   └── apply
    └── graph
        ├── add
        └── list
```

### Why these three groupings

- `memory inbox` is already the natural home for candidate review. We are folding the standalone `accept`, `reject`, `propose`, `distill`, and `extract` subcommands underneath it so all curation happens in one namespace.
- `memory store` is the durable write surface. Recording new accepted memories, replacing them, expiring them, and adjusting their validity window are the operations that actually mutate the durable layer.
- `memory admin` collects the operator-facing concerns: bringing memories in from host files, exporting them out to host files, planning activation against Claude/Codex/Gemini, hygiene scans, and the typed-relationship graph. These are not part of the daily read path and historically each had its own top-level entry.

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
| `memory accept` | `memory inbox accept` | Hidden deprecated alias |
| `memory reject` | `memory inbox reject` | Hidden deprecated alias |
| `memory propose` | `memory inbox propose` | Hidden deprecated alias |
| `memory distill` | `memory inbox distill` | Hidden deprecated alias |
| `memory extract` | `memory inbox extract` | Hidden deprecated alias |
| `memory remember` | `memory store remember` | Hidden deprecated alias |
| `memory supersede` | `memory store supersede` | Hidden deprecated alias |
| `memory expire` | `memory store expire` | Hidden deprecated alias |
| `memory set-validity` | `memory store set-validity` | Hidden deprecated alias |
| `memory import` | `memory admin import` | Hidden deprecated alias |
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
