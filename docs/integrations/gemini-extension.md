# Gemini CLI extension

[日本語](./gemini-extension.ja.md)

> **Maintenance-mode notice:** Google stopped serving Gemini CLI to free and Google AI Pro/Ultra users on 2026-06-18 and directs them to **Antigravity**. Gemini CLI remains supported upstream for Gemini Code Assist Standard/Enterprise and users of paid Gemini or Gemini Enterprise Agent Platform API keys. Traceary therefore keeps this extension available and maintained for those installations, but new free/Pro/Ultra users should install the [Antigravity plugin](./antigravity.md) instead. See [Google's transition announcement](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/).

The Gemini package lives under `integrations/gemini-extension/`. Gemini CLI expects `gemini-extension.json` at the root of the installed extension, so Traceary ships this package as a dedicated extension archive on tagged releases.

## What it wires automatically

- `SessionStart` / `SessionEnd` hooks
- `BeforeAgent` prompt hook — records the submitted user prompt as a `prompt` event
- `AfterAgent` transcript and usage-availability hooks — records the agent response as a `transcript` event and records that provider usage is unavailable from this body-free hook surface
- `AfterTool` shell-audit hook for `run_shell_command`
- `PreCompress` compact marker hook — records the pre-compress boundary (Gemini exposes no post-compress summary hook)
- slash commands: `/traceary-help` and `/traceary-doctor` (`/traceary-help` orients on the CLI, hooks, and doctor)
- contextual skills (one per job; see [skills](./skills.md)): `traceary-session-history`, `traceary-session-refine`, `traceary-memory-review`, and `traceary-memory-remember`. All four route through the Traceary CLI.

## Usage metadata

Gemini CLI has two deliberately separate capture paths:

- A Traceary-owned one-shot command such as `traceary session run -- gemini -p "..." --output-format stream-json` records the terminal `result.stats` totals. When the result contains per-model totals, Traceary stores those model rows and does not store the aggregate again.
- Interactive `AfterAgent` hooks record an explicit **unavailable** usage observation. Traceary does not install `AfterModel` for usage because that hook also carries model request/response bodies.

The adapter reads only the versioned, body-free metadata fields needed for source identity, terminal status, timestamp, and token totals. It does not infer usage from prompt length, response length, tool count, or elapsed time. Replaying the same terminal result or `AfterAgent` timestamp is idempotent.

## Memory activation strategy

Gemini integration uses Traceary's accepted memory store through
instruction-file export and host-native activation. To make reviewed memories
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

Do not use `gemini extensions update` for a locally installed Traceary
extension: that command waits on an interactive prompt and can hang in
headless runs. Use the install script, pinned to the running CLI tag:

```sh
./scripts/install-gemini-extension.sh
# or an explicit ref:
./scripts/install-gemini-extension.sh --ref v0.43.0
```

Gemini CLI refuses `extensions install` when the same name is already
installed, so the script copies the previous extension aside, uninstalls,
then installs the new package. Each `gemini` invocation is bounded by
`TRACEARY_GEMINI_TIMEOUT` (default 60s). A failed or timed-out call
restores the copy.
When `--ref` matches this checkout's `VERSION`, it installs from
`integrations/gemini-extension` instead of a temp clone (temp clones have
hung on Gemini's second folder-trust prompt).

Recovery if an older uninstall-first run left the host bare:

```sh
gemini extensions install --consent integrations/gemini-extension
```

## Uninstall

```sh
gemini extensions uninstall traceary
```

## Doctor and smoke test

Primary runtime check:

```sh
traceary doctor --client gemini --json
```

`doctor` now checks three Gemini capture failure modes:

- `gemini-config` warns when the installed Traceary-managed hooks are partial
  (for example, legacy SessionStart / SessionEnd / AfterTool only) and can be
  repaired with `traceary doctor --client gemini --fix` for settings.json
  installs.
- `gemini-event-coverage` scans recent Gemini sessions and warns when the
  prompt/transcript-missing session ratio is above `--coverage-threshold` (default `0.5`). Audit-only sessions still warn because they lack conversation coverage.
  If you rely on the Gemini extension package instead of settings.json, refresh
  it with `./scripts/install-gemini-extension.sh` so the packaged BeforeAgent /
  AfterAgent hooks are reinstalled from the matching CLI tag.
- `gemini-host-eligibility` re-runs a bounded headless `gemini -p` probe
  (throwaway `TRACEARY_DB_PATH`, 60s timeout) once the project config passes
  inspection, and warns when the account is rejected with `IneligibleTierError`.
  On an ineligible account the host aborts the run after `SessionStart`, so
  `prompt`/`transcript` events are absent even though the hooks are wired —
  this is a Google account-tier rejection, not a broken install, and Traceary
  cannot fix it (migrate to Antigravity or an eligible plan). When the binary
  is missing, the probe times out, or it fails for any other reason, the check
  reports skip rather than pass.

### Isolated `-p` probe recipe (eligible tier vs missing hooks)

To tell an **ineligible account** apart from a **hook wiring problem**, re-run
the dogfood observation against a throwaway store from a project with the
Traceary hooks installed:

```sh
PROBE_DIR="$(mktemp -d)"
cd <your project with Traceary hooks>
TRACEARY_DB_PATH="$PROBE_DIR/probe.db" gemini -p "Reply with the single word ok." --approval-mode plan
TRACEARY_DB_PATH="$PROBE_DIR/probe.db" traceary list --limit 10
```

Interpret the result:

- stderr shows `IneligibleTierError` ("no longer supported for Gemini Code
  Assist for individuals") and the throwaway store lists only
  `session_started`/`session_ended`: the **account is not eligible**. Hooks are
  fine; the host aborts the run before `BeforeAgent`. Migrate to Antigravity or
  switch to an eligible Gemini Code Assist / paid API plan.
- The store lists `prompt` (and `transcript` when the host fires `AfterAgent`):
  wiring **and** eligibility are both good on this host.
- No `IneligibleTierError`, but `prompt` is still missing: treat it as a hook
  wiring problem — check `gemini-config` and `gemini-event-coverage` and
  refresh the managed hooks (`traceary doctor --client gemini --fix`, or
  reinstall the extension from a matching release tag).

The matrix `prompt` cell for Gemini stays `wired` with this ineligible-tier
caveat: the wiring is intact and eligible accounts still capture prompts, but
the cell is not a per-account capture guarantee. If a later Gemini build makes
`-p` succeed on your account, evidence from the recipe above is what justifies
a fresh probe date on the matrix.


Package validation:

```sh
gemini extensions validate integrations/gemini-extension
```

End-to-end smoke test from this repository:

```sh
TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 ./scripts/smoke_test_integrations.sh gemini
```

The opt-in environment variable is intentional: the Gemini CLI may open a browser authentication prompt, so the default `./scripts/smoke_test_integrations.sh all` path skips this runtime probe in headless release-prep shells.
