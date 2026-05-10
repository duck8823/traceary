# Gemini CLI extension

[日本語](./gemini-extension.ja.md)

The Gemini package lives under `integrations/gemini-extension/`. Gemini CLI expects `gemini-extension.json` at the root of the installed extension, so Traceary ships this package as a dedicated extension archive on tagged releases.

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart` / `SessionEnd` hooks
- `AfterAgent` transcript hook — records the agent response as a `transcript` event
- `AfterTool` shell-audit hook for `run_shell_command`
- slash commands: `/traceary-help` and `/traceary-doctor`
- contextual skills: `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember`. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて").

## Memory activation strategy

Gemini integration uses Traceary's accepted memory store through MCP tools,
instruction-file export, and host-native activation. To make reviewed memories
visible in Gemini instructions, you have two options.

**Option 1 — instruction-file export (still supported).** Export accepted
memories into a Traceary-managed block inside `GEMINI.md` directly:

```sh
traceary memory admin export --target gemini --out GEMINI.md
```

**Option 2 — host-native activation (v0.13.0+, recommended for projects).** Use
`traceary memory admin activate --target gemini` to manage a small import stub inside
`GEMINI.md` and an external memory file under `.traceary/memories/gemini.md`.
The activation pair preserves user-authored content outside the managed
regions, refuses unsafe targets (symlinks, directories, malformed markers,
newer marker versions), and is idempotent. Traceary never reads or rewrites
Gemini's `## Gemini Added Memories` section produced by `save_memory`; that
section is owned by Gemini's auto-memory tool and is preserved as ordinary
host-context content. When the section is present, Traceary appends the
managed import stub at end-of-file so both sources of truth coexist safely.
The Gemini activation smoke test asserts that the seeded `## Gemini Added
Memories` section is preserved byte-for-byte after `--apply`.

```sh
# inspect the live host pair (read-only)
traceary memory admin activate --target gemini --status

# preview the planned changes (dry-run, no writes)
traceary memory admin activate --target gemini --dry-run --diff

# apply the pair with safe per-file writes (idempotent)
traceary memory admin activate --target gemini --apply
```

Defaults:

- activation root: nearest `.git` ancestor, or the working directory when no
  `.git` is present
- host context file: `<root>/GEMINI.md`
- external memory file: `<root>/.traceary/memories/gemini.md`
- import line rendered into `GEMINI.md`: `@./.traceary/memories/gemini.md`

Override with `--root <dir>` or `--path <file>`; see the v0.13 host-native
memory activation [ADR](../architecture/host-native-memory-activation.md) for
the full contract (managed marker layout, status states, and tracked-file
policy) and the [durable memory guide](../memory/README.md#recovering-from-invalid-state)
for `invalid` recovery steps. `traceary doctor --client gemini` surfaces a
`gemini-memory-activation` check with the same dry-run / apply remediation
commands.

## Install

1. Install the Traceary CLI first.

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# or
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Install the extension from a Traceary GitHub release.

```sh
gemini extensions install https://github.com/duck8823/traceary --ref <tag>
```

Traceary publishes a dedicated `traceary.tar.gz` release asset whose archive root is the extension root expected by Gemini CLI.

For local development against this repository, use a link instead:

```sh
gemini extensions link integrations/gemini-extension
```

## Update

```sh
gemini extensions update traceary
```

To pin a specific release again, reinstall with `--ref <tag>`.

## Uninstall

```sh
gemini extensions uninstall traceary
```

## Doctor and smoke test

Primary runtime check:

```sh
traceary doctor --client gemini --json
```

Package validation:

```sh
gemini extensions validate integrations/gemini-extension
```

End-to-end smoke test from this repository:

```sh
TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 ./scripts/smoke_test_integrations.sh gemini
```

The opt-in environment variable is intentional: the Gemini CLI may open a browser authentication prompt, so the default `./scripts/smoke_test_integrations.sh all` path skips this runtime probe in headless release-prep shells.
