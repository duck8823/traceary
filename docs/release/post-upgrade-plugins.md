# Post-upgrade plugin refresh checklist

[日本語](./post-upgrade-plugins.ja.md)

Part of #1361 · parent cut #1360.

Homebrew / `go install` / release binary upgrades update the **Traceary CLI binary only**. Host plugin packages (Claude / Codex / Gemini / Antigravity / Grok) live in each host’s cache or install root and **do not upgrade automatically** with `brew upgrade traceary`.

After every binary upgrade to a new minor/patch:

1. Confirm the binary: `traceary -v`
2. Run doctor for each host you use: `traceary doctor --client <host> --json`
3. Refresh any `*-plugin-version` WARN using the host-specific command below
4. Re-run doctor until plugin-version checks **PASS** or **SKIP** with a clear reason

## Host refresh commands

| Host | Typical install root | Refresh command |
|---|---|---|
| Claude Code | Claude plugin cache + marketplace | Inside Claude Code: `claude plugins update` for the Traceary marketplace key (doctor `FixCommand` prints the exact key) |
| Codex | `~/.codex/plugins/cache/.../traceary/` | Reinstall from a **matching release tag** checkout of this repository (see Codex plugin docs under `plugins/traceary/`) |
| Gemini CLI (legacy extension) | `~/.gemini/extensions/traceary/` | `gemini extensions update traceary` |
| Antigravity | `~/.gemini/config/plugins/traceary` and/or `~/.gemini/antigravity-cli/plugins/traceary` | From a matching checkout: `agy plugin install integrations/antigravity-plugin` |
| Grok Build | host plugin root (see `scripts/install-grok-plugin.sh`) | From a matching checkout: `./scripts/install-grok-plugin.sh` |

Doctor `*-plugin-version` messages include a `FixCommand` when an exact non-interactive path is known. Prefer that over inventing flags.

## Antigravity dual install paths

Antigravity may leave packages under both:

- `~/.gemini/config/plugins/traceary`
- `~/.gemini/antigravity-cli/plugins/traceary`

If **one** path matches the running binary and the other is incomplete (missing `version`), doctor **skips** a permanent WARN on the incomplete twin (#1361). You can still remove the unused directory after verifying hooks with `traceary doctor --client antigravity`.

## Homebrew note

`brew upgrade traceary` never rewrites host plugin caches. Treat plugin refresh as a required post-upgrade step on dogfood machines.

## Related

- [Release guide](./README.md)
- [Integrations overview](../integrations/README.md)
