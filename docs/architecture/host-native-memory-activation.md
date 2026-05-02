# Host-native memory activation contract

[日本語](./host-native-memory-activation.ja.md)

This ADR defines the v0.13.0 contract for activating accepted Traceary durable memories into Claude Code and Gemini CLI native context surfaces.

Status: accepted for implementation planning. Claude Opus 4.7 Max and Gemini scout reviews found no MUST blockers for this design/spec PR.

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

- Claude Code loads `CLAUDE.md` files at session start and supports `@path/to/import` entries inside `CLAUDE.md`. Imported files are expanded into context at launch; relative paths resolve relative to the file containing the import. External imports may require a first-time approval dialog. The same documentation shows importing from a hidden user directory such as `@~/.claude/...`, so hidden paths are not rejected solely because they start with a dot. Source: <https://code.claude.com/docs/en/memory>
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

- default activation root: the nearest ancestor containing `.git`, or the current working directory when no `.git` root is available
- default host context file: `<root>/CLAUDE.md`
- default external memory file: `<root>/.traceary/memories/claude.md`
- default import line rendered in `CLAUDE.md`: `@./.traceary/memories/claude.md`

`CLAUDE.local.md` remains a possible future user-local mode, but it is not the v0.13.0 default because Traceary must first ship one deterministic path that `status`, `doctor`, and tests can validate.

Implementation PRs must prove that the current Claude Code version can load this exact import line from the default hidden `.traceary/` directory before Claude apply is marked ready. If that smoke test fails, this ADR and the downstream issues must be updated before applying writes to real user files.

### Gemini

When `--target gemini` is implemented:

- default activation root: the nearest ancestor containing `.git`, or the current working directory when no `.git` root is available
- default host context file: `<root>/GEMINI.md`
- default external memory file: `<root>/.traceary/memories/gemini.md`
- default import line rendered in `GEMINI.md`: `@./.traceary/memories/gemini.md`

Traceary must not rewrite or reorder Gemini's `## Gemini Added Memories` section. If a Traceary-managed import stub is added to a file that also contains that section, the section must remain byte-for-byte unchanged outside the managed region.

Implementation PRs must prove that the current Gemini CLI version can load this exact import line from the default hidden `.traceary/` directory before Gemini apply is marked ready. If that smoke test fails, this ADR and the downstream issues must be updated before applying writes to real user files.

### Overrides

The existing activation flags keep their meanings, with host-specific resolution:

- `--root <dir>` resolves the activation root. For Claude/Gemini, this root contains the host context file and the `.traceary/memories/<target>.md` external file.
- `--path <file>` explicitly selects the host context file. The external memory file is derived from the context file's directory as `.traceary/memories/<target>.md`.
- `--path` wins over `--root`.
- v0.13.0 should not add a second path flag unless implementation proves the derived external-file path blocks a real dogfood scenario. If such a flag is needed, prefer `--memory-path` and document it before implementation.

Import paths must be rendered relative to the host context file whenever both files share a root. Absolute import paths are allowed only when a future explicit override makes relative rendering impossible.

The root detector must be deterministic: starting from the command working directory, walk upward until the first directory containing `.git`; if none is found, use the command working directory. `--root` bypasses detection entirely, and `--path` bypasses both detection and `--root` for the host context file path.

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

When the host context file already contains a supported Traceary import stub, Traceary replaces that region in place. When the file has no managed import stub, Traceary appends the stub at end-of-file using the existing managed-block spacing rule: preserve the existing bytes, add enough newlines to leave one blank line before the stub, then append the managed region. Traceary does not insert before frontmatter, headings, or Gemini's `## Gemini Added Memories` section because doing so would require interpreting user-authored markdown structure. If the file contains an unmanaged import line that already points at the expected `.traceary/memories/<host>.md` file outside a Traceary-managed stub, status must be `invalid` and apply must refuse to add a duplicate import until the operator removes or adopts that unmanaged line through a future explicit workflow. Unmanaged imports pointing at different files are user-authored content and are ignored by activation.

## Status semantics

Claude/Gemini status is computed from the pair of files, not from one file alone.

| State | Meaning |
| --- | --- |
| `missing` | The host context file is missing, the import stub is absent, the external memory file is missing, or the external file lacks the Traceary-managed memory block. No malformed or unsafe condition was found. |
| `stale` | The stub points to an old expected path, or the external memory block differs from the current accepted-memory projection. |
| `in_sync` | The host context file contains exactly one supported Traceary import stub pointing at the expected external memory file, and that external file contains a supported Traceary memory block matching the current accepted-memory projection. |
| `invalid` | Traceary cannot safely interpret or write either file: unsupported symlink, directory target, duplicate markers, orphan markers, newer marker version, malformed managed region, unreadable file, or an import path that escapes the allowed activation root without an explicit override. |

JSON output should expose component-level details for the host context file and external memory file so `traceary doctor` can give actionable remediation.

Host import visibility is an implementation-readiness gate, not only a release dogfood task. The first Claude/Gemini read-only PRs must include a smoke test or recorded manual verification that the host resolves the planned import path; the apply PRs must stay draft/blocked until that evidence exists.

For Claude, the v0.13.0-4 read-only PR records that evidence in two artefacts that together stand in for a flaky live launch:

- `application/usecase/claude_import_readiness_internal_test.go` pins the rendered import line, marker layout, and external-file resolution to the exact form Claude's official memory documentation specifies (`@<relative-path>` resolved against the directory containing `CLAUDE.md`). It runs in CI, so a refactor cannot silently move the import path.
- `scripts/smoke_test_claude_activation.sh` materialises a temp project (`.git`, `CLAUDE.md`, `.traceary/memories/claude.md`) using the live `traceary memory activate --target claude --dry-run --json` plan. By default it removes the temp project after the structural check; set `TRACEARY_KEEP_SMOKE_TEMP=1` to retain it and launch `claude` from that directory for a manual runtime probe. The live launch is gated behind `TRACEARY_ENABLE_CLAUDE_RUNTIME_SMOKE=1` because Claude Code's first-time external-import approval dialog and authentication state make an unattended runtime probe non-deterministic.

The Claude `--apply` PR (#893) keeps the live runtime evidence in scope through the same two artefacts. The script's structural section now exercises `--apply` end-to-end (first apply creates both files, a second apply converges to noop, and `--status` reports `in_sync` afterwards), so a CI-side regression in the apply path is detected without launching Claude. A maintainer-recorded `smoke_test_claude_activation.sh` run with `TRACEARY_ENABLE_CLAUDE_RUNTIME_SMOKE=1` is still required when an alternative gate is not available, or the ADR must be updated explaining why a different gate is acceptable.

For Gemini, the v0.13.0-6 read-only PR records the same two artefacts that together stand in for a flaky live launch:

- `application/usecase/gemini_import_readiness_internal_test.go` pins the rendered import line, marker layout, and external-file resolution to the form Gemini's official Memory Import Processor documentation specifies (`@<relative-path>` resolved against the directory containing `GEMINI.md`). The test additionally asserts that the rendered DO NOT EDIT warning carries `--target gemini` so the remediation command points at the right host. It runs in CI, so a refactor cannot silently move the import path or mis-target the warning.
- `scripts/smoke_test_gemini_activation.sh` materialises a temp project (`.git`, `GEMINI.md`, `.traceary/memories/gemini.md`) using the live `traceary memory activate --target gemini --dry-run --json` plan. The Gemini `--apply` path is not yet implemented (it lands in #895), so the script does not exercise apply: it only validates the dry-run pair contract today and is expanded by #895 to cover the apply ordering. Set `TRACEARY_KEEP_SMOKE_TEMP=1` to retain the temp project for a manual `gemini` runtime probe; the live launch is gated behind `TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1` because Gemini CLI's hierarchical context loading and authentication state make an unattended runtime probe non-deterministic.

The Gemini `--apply` PR (#895) must extend `scripts/smoke_test_gemini_activation.sh` end-to-end (first apply creates both files, a second apply converges to noop, post-apply status reports `in_sync`) so the apply path inherits the CI-side regression coverage Claude already has. The PR must also ensure the existing `## Gemini Added Memories` section in any pre-existing `GEMINI.md` is preserved byte-for-byte after `--apply`. A maintainer-recorded run with `TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1` is still required when an alternative gate is not available.

## Apply semantics

`--status` and `--dry-run` are read-only. `--apply` is the only mutating mode.

Apply must be idempotent and must preserve user-authored content outside Traceary-managed regions. For Claude/Gemini the write order is:

1. render and safely write the external memory file;
2. write the host context import stub only if it is missing or stale;
3. leave the host context file untouched when the stub is already in sync.

If the first write succeeds and the second write fails, Traceary does not attempt complex rollback. The next `status` must report the remaining missing/stale/invalid condition, and a repeated `apply` must converge safely.

Atomic write behavior, permission preservation, parent-directory creation, symlink refusal, and newer-marker refusal should follow the v0.12 Codex activation implementation.

Those safety guarantees apply independently to both files in the pair. Traceary must inspect and write the host context file and external memory file through the same safe-writer contract: `lstat` before write, reject symlinks and directories, preserve existing permissions when replacing a file, create parent directories only for the file being written, write through a temporary file in the same directory, sync, and rename atomically where the platform supports it.

## Tracked project file policy

Traceary may update project files only through explicit `--apply`. It must never mutate `CLAUDE.md`, `GEMINI.md`, or `.traceary/memories/<host>.md` from `doctor`, `status`, or `dry-run`.

When the host context file is tracked in Git, Traceary still may update the managed stub under explicit `--apply`, but the diff must be reviewable and limited to the managed region. Traceary must not stage or commit those changes.

When the host context file does not exist, `--apply` may create it with only the managed stub. The dry-run output and doctor remediation command must make that planned creation explicit.

Traceary does not edit `.gitignore` during activation. Teams may choose to commit `.traceary/memories/<host>.md` when they want shared project memory projection, or ignore it when they want each machine to project from its own local Traceary store. The source of truth remains SQLite either way, so documentation and dry-run output must make the selected path visible before apply.

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

Gemini review was run multiple times. One pass objected to import stubs because it assumed Claude imports were not loaded natively and worried that `.traceary/` might be ignored. That was triaged against the official Claude memory documentation, which documents `@path/to/import` expansion at launch and includes a hidden-directory import example. The remaining real risks are therefore captured as implementation gates: Claude's first-time external-import approval dialog, safe stub injection, import-path resolution relative to the host context file, and smoke verification that each host loads the default `.traceary/` import path before apply PRs are made ready.

Claude Opus 4.7 Max review initially failed while Claude Code was not authenticated:

```text
$ claude auth status
{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}

$ claude --print --model opus --effort max --permission-mode plan ...
Not logged in · Please run /login
```

Implementation PRs after this ADR should remain draft or blocked until Claude Opus review is obtained, unless maintainers explicitly accept an exception on the PR.

After authentication was restored, Claude Opus 4.7 Max reviewed PR #898 and reported no MUST blockers. Its SHOULD findings are captured in this ADR: deterministic append-only insertion, both-file safe-writer guarantees, `.git` root detection precedence, and explicit `.gitignore` non-mutation policy. Claude's ready decision for this design/spec PR was `ready`, assuming CI and Gemini scout pass.
