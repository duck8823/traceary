# Host-native memory activation contract

[日本語](./host-native-memory-activation.ja.md)

This ADR defines the v0.13.0 contract for activating accepted Traceary durable memories into Claude Code and Gemini CLI native context surfaces.

Status: accepted for implementation planning, with Claude Opus review still blocked by local authentication.

## Context

Traceary keeps durable memory in the local SQLite store. Accepted memories are the source of truth; host files are projections that make those reviewed facts visible to coding agents.

v0.12.0 shipped Codex activation as a safe managed-file projection:

- explicit `status`, `dry-run`, `diff`, and `apply` modes
- no implicit filesystem mutation
- Traceary-managed markers
- preservation of user-authored content outside managed regions
- refusal to overwrite newer marker versions or malformed managed blocks
- symlink refusal and atomic writes
- `traceary doctor` visibility

v0.13.0 extends the same safety model to Claude Code and Gemini CLI. The new part is that Claude and Gemini discover context through host instruction files (`CLAUDE.md` / `GEMINI.md`) that may already be user-authored and, in many projects, tracked in Git.

## Host documentation baseline

The contract relies on host-supported markdown imports rather than host-owned auto-memory stores.

- Claude Code loads `CLAUDE.md` files at session start and supports `@path/to/import` entries inside `CLAUDE.md`. Imported files are expanded into context at launch; relative paths resolve relative to the file containing the import. External imports may require a first-time approval dialog. Source: <https://code.claude.com/docs/en/memory>
- Claude auto memory is host-owned, stored under `~/.claude/projects/<project>/memory/`, and read/written by Claude during sessions. Traceary must not write there by default. Source: <https://code.claude.com/docs/en/memory>
- Gemini CLI loads hierarchical `GEMINI.md` context files, supports `@file.md` imports in `GEMINI.md`, and supports relative and absolute import paths. Source: <https://google-gemini.github.io/gemini-cli/docs/cli/gemini-md.html>
- Gemini `save_memory` appends facts to the user's `~/.gemini/GEMINI.md` under `## Gemini Added Memories`. Traceary must not modify that section. Source: <https://google-gemini.github.io/gemini-cli/docs/tools/memory.html>
- Gemini's Memory Import Processor documents import depth, circular-import handling, and access validation. Source: <https://google-gemini.github.io/gemini-cli/docs/core/memport.html>

## Decision

Use a two-file activation contract for Claude and Gemini:

1. a small Traceary-managed import stub inside the host context file, and
2. a Traceary-managed external memory file containing the rendered accepted memories.

The host context file is the stable integration point. The external memory file is the frequently updated projection. Subsequent memory refreshes should update only the external memory file when the import stub is already correct.

Codex remains unchanged in v0.13.0: its activation target is still a Traceary-managed native memory file such as `~/.codex/memories/traceary.md`.

## Default targets

### Claude

When `--target claude` is implemented:

- default activation root: detected project root, or the current working directory when no project root is available
- default host context file: `<root>/CLAUDE.md`
- default external memory file: `<root>/.traceary/memories/claude.md`
- default import line rendered in `CLAUDE.md`: `@./.traceary/memories/claude.md`

`CLAUDE.local.md` remains a possible future user-local mode, but it is not the v0.13.0 default because Traceary must first ship one deterministic path that `status`, `doctor`, and tests can validate.

### Gemini

When `--target gemini` is implemented:

- default activation root: detected project root, or the current working directory when no project root is available
- default host context file: `<root>/GEMINI.md`
- default external memory file: `<root>/.traceary/memories/gemini.md`
- default import line rendered in `GEMINI.md`: `@./.traceary/memories/gemini.md`

Traceary must not rewrite or reorder Gemini's `## Gemini Added Memories` section. If a Traceary-managed import stub is added to a file that also contains that section, the section must remain byte-for-byte unchanged outside the managed region.

### Overrides

The existing activation flags keep their meanings, with host-specific resolution:

- `--root <dir>` resolves the activation root. For Claude/Gemini, this root contains the host context file and the `.traceary/memories/<target>.md` external file.
- `--path <file>` explicitly selects the host context file. The external memory file is derived from the context file's directory as `.traceary/memories/<target>.md`.
- `--path` wins over `--root`.
- v0.13.0 should not add a second path flag unless implementation proves the derived external-file path blocks a real dogfood scenario. If such a flag is needed, prefer `--memory-path` and document it before implementation.

Import paths must be rendered relative to the host context file whenever both files share a root. Absolute import paths are allowed only when a future explicit override makes relative rendering impossible.

## Managed regions

The import stub and the external memory block have separate marker contracts.

Host context file stub:

```md
<!-- traceary-memory-import:begin:v1 -->
<!-- DO NOT EDIT: this import is managed by Traceary. Run `traceary memory activate --target <host> --dry-run --diff` before applying updates. -->
@./.traceary/memories/<host>.md
<!-- traceary-memory-import:end -->
```

External memory file:

```md
<!-- traceary-memories:begin:v1 -->
<!-- DO NOT EDIT: this block is managed by Traceary. Hand edits will be overwritten by `traceary memory export` or `traceary memory activate`. -->

# Traceary-managed <host> memories

...
<!-- traceary-memories:end -->
```

The external memory block should reuse the existing `memory export` renderer so Codex, Claude, and Gemini projections stay consistent.

## Status semantics

Claude/Gemini status is computed from the pair of files, not from one file alone.

| State | Meaning |
| --- | --- |
| `missing` | The host context file is missing, the import stub is absent, the external memory file is missing, or the external file lacks the Traceary-managed memory block. No malformed or unsafe condition was found. |
| `stale` | The stub points to an old expected path, or the external memory block differs from the current accepted-memory projection. |
| `in_sync` | The host context file contains exactly one supported Traceary import stub pointing at the expected external memory file, and that external file contains a supported Traceary memory block matching the current accepted-memory projection. |
| `invalid` | Traceary cannot safely interpret or write either file: unsupported symlink, directory target, duplicate markers, orphan markers, newer marker version, malformed managed region, unreadable file, or an import path that escapes the allowed activation root without an explicit override. |

JSON output should expose component-level details for the host context file and external memory file so `traceary doctor` can give actionable remediation.

## Apply semantics

`--status` and `--dry-run` are read-only. `--apply` is the only mutating mode.

Apply must be idempotent and must preserve user-authored content outside Traceary-managed regions. For Claude/Gemini the write order is:

1. render and safely write the external memory file;
2. write the host context import stub only if it is missing or stale;
3. leave the host context file untouched when the stub is already in sync.

If the first write succeeds and the second write fails, Traceary does not attempt complex rollback. The next `status` must report the remaining missing/stale/invalid condition, and a repeated `apply` must converge safely.

Atomic write behavior, permission preservation, parent-directory creation, symlink refusal, and newer-marker refusal should follow the v0.12 Codex activation implementation.

## Tracked project file policy

Traceary may update project files only through explicit `--apply`. It must never mutate `CLAUDE.md`, `GEMINI.md`, or `.traceary/memories/<host>.md` from `doctor`, `status`, or `dry-run`.

When the host context file is tracked in Git, Traceary still may update the managed stub under explicit `--apply`, but the diff must be reviewable and limited to the managed region. Traceary must not stage or commit those changes.

When the host context file does not exist, `--apply` may create it with only the managed stub. The dry-run output and doctor remediation command must make that planned creation explicit.

## Rejected alternatives

### Write directly into Claude auto memory

Rejected. Claude auto memory is a host-owned store under `~/.claude/projects/<project>/memory/` and is read/written by Claude itself. Writing there would bypass Traceary's accepted-memory source of truth and would couple Traceary to a host-managed format.

### Write directly into Gemini `## Gemini Added Memories`

Rejected. Gemini `save_memory` owns that section in the user's global `GEMINI.md`. Traceary must not mix reviewed accepted memories with facts appended by Gemini's memory tool.

### Inject the full memory block directly into `CLAUDE.md` / `GEMINI.md`

Rejected as the default. It is simpler, but it churns user/project instruction files every time accepted memories change. The import-stub strategy confines frequent updates to `.traceary/memories/<host>.md` while still using host-native context loading.

### Keep only manual `memory export --out`

Rejected for v0.13.0. Export remains useful, but activation needs read-only status, dry-run/diff, explicit apply, doctor integration, and idempotent remediation commands.

### Use symlinks from host context files to Traceary files

Rejected. Symlink behavior is platform-sensitive and weakens the existing activation safety model. Traceary should render normal markdown imports and reject symlink targets it would write.

## Implementation sequence

1. #889: this contract / ADR.
2. #890: extract host-agnostic activation target resolution, marker parsing, status, and safe writer code without changing Codex behavior.
3. #891: add two-file activation planning primitives and component-level status/diff output.
4. #892: wire Claude read-only `status`, `dry-run`, and `diff`.
5. #893: wire Claude `apply` and doctor integration.
6. #894: wire Gemini read-only `status`, `dry-run`, and `diff`.
7. #895: wire Gemini `apply` and doctor integration.
8. #896: document and dogfood Claude/Gemini workflows with temporary HOME/project fixtures.
9. #897: prepare v0.13.0 release metadata and changelog.

## Review notes

Gemini review was run twice. The first pass objected to import stubs, but it assumed imports were not loaded natively. After providing the official Claude and Gemini import documentation, the second pass accepted the import-stub strategy and highlighted the real risks: Claude's first-time external-import approval dialog, safe stub injection, and import-path resolution relative to the host context file.

Claude Opus 4.7 Max review was attempted locally, but Claude Code is not authenticated in this environment:

```text
$ claude auth status
{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}

$ claude --print --model opus --effort max --permission-mode plan ...
Not logged in · Please run /login
```

Implementation PRs after this ADR should remain draft or blocked until Claude Opus review is obtained, unless maintainers explicitly accept an exception on the PR.
