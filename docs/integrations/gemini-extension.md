# Gemini CLI extension

[日本語](./gemini-extension.ja.md)

> **Maintenance-mode notice:** Google stopped serving Gemini CLI to free and Google AI Pro/Ultra users on 2026-06-18 and directs them to **Antigravity**. Gemini CLI remains supported upstream for Gemini Code Assist Standard/Enterprise and users of paid Gemini or Gemini Enterprise Agent Platform API keys. Traceary therefore keeps this extension available and maintained for those installations, but new free/Pro/Ultra users should install the [Antigravity plugin](./antigravity.md) instead. See [Google's transition announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/).

The Gemini package lives under `integrations/gemini-extension/`. Gemini CLI expects `gemini-extension.json` at the root of the installed extension, so Traceary ships this package as a dedicated extension archive on tagged releases.

## What it wires automatically

- `traceary` MCP server via `traceary mcp-server`
- `SessionStart` / `SessionEnd` hooks
- `BeforeAgent` prompt hook — records the submitted user prompt as a `prompt` event
- `AfterAgent` transcript and usage-availability hooks — records the agent response as a `transcript` event and records that provider usage is unavailable from this body-free hook surface
- `AfterTool` shell-audit hook for `run_shell_command`
- `PreCompress` compact marker hook — records the pre-compress boundary (Gemini exposes no post-compress summary hook)
- slash commands: `/traceary-help` and `/traceary-doctor`
- contextual skills: `traceary-session-history`, `traceary-memory-review`, and `traceary-memory-remember`. `traceary-memory-review` triggers on review-intent phrases ("Traceary inbox", "review memory candidates", "session recap") and curates the inbox; `traceary-memory-remember` triggers only on explicit-write phrases ("remember that", "覚えておいて").

## Usage metadata

Gemini CLI has two deliberately separate capture paths:

- A Traceary-owned one-shot command such as `traceary session run -- gemini -p "..." --output-format stream-json` records the terminal `result.stats` totals. When the result contains per-model totals, Traceary stores those model rows and does not store the aggregate again.
- Interactive `AfterAgent` hooks record an explicit **unavailable** usage observation. Traceary does not install `AfterModel` for usage because that hook also carries model request/response bodies.

The adapter reads only the versioned, body-free metadata fields needed for source identity, terminal status, timestamp, and token totals. It does not infer usage from prompt length, response length, tool count, or elapsed time. Replaying the same terminal result or `AfterAgent` timestamp is idempotent.

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

### Choose the supported route

- **Free, Google AI Pro, or Google AI Ultra:** migrate to Antigravity. Install the Traceary CLI, then run `traceary hooks install --client antigravity` to configure Traceary hooks directly. Verify the result with `traceary doctor --client antigravity`; the [Antigravity guide](./antigravity.md) also documents the separate packaged-plugin route through `agy plugin install` and cleanup of stale Gemini-shaped packages.
- **Gemini Code Assist Standard/Enterprise or a paid Gemini or Gemini Enterprise Agent Platform API key:** you may continue using this Gemini extension. Traceary treats it as a maintenance-mode integration: compatibility, bug, and security fixes continue, while new Google-host integration work targets Antigravity.

Keeping the Gemini extension installed does not migrate its hooks or settings into Antigravity. Configure the Antigravity integration separately, verify it, and remove the Gemini extension only after you no longer need Gemini CLI.

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

`doctor` now checks two Gemini capture failure modes:

- `gemini-config` warns when the installed Traceary-managed hooks are partial
  (for example, legacy SessionStart / SessionEnd / AfterTool only) and can be
  repaired with `traceary doctor --client gemini --fix` for settings.json
  installs.
- `gemini-event-coverage` scans recent Gemini sessions and warns when the
  prompt/transcript-missing session ratio is above `--coverage-threshold` (default `0.5`). Audit-only sessions still warn because they lack conversation coverage.
  If you rely on the Gemini extension package instead of settings.json, update
  it with `gemini extensions update traceary` so the packaged BeforeAgent /
  AfterAgent hooks are refreshed.

Package validation:

```sh
gemini extensions validate integrations/gemini-extension
```

End-to-end smoke test from this repository:

```sh
TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 ./scripts/smoke_test_integrations.sh gemini
```

The opt-in environment variable is intentional: the Gemini CLI may open a browser authentication prompt, so the default `./scripts/smoke_test_integrations.sh all` path skips this runtime probe in headless release-prep shells.
