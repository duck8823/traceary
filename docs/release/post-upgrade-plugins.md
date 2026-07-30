# Post-upgrade plugin refresh checklist

[日本語](./post-upgrade-plugins.ja.md)

Part of #1361 · parent cut #1360.

Homebrew / `go install` / release binary upgrades update the **Traceary CLI binary only**. Host plugin packages live in each host’s cache or install root and **do not upgrade automatically** with `brew upgrade traceary`.

After every released binary upgrade:

1. Confirm the binary: `traceary -v`.
2. Refresh every installed host package using the matrix below.
3. Complete its activation step before collecting the doctor report.
4. Run the body-free release-QA gate. Every unskipped host must have a `pass` plugin-version result. Antigravity may additionally report `skip` for an incomplete dual-path twin only when another copy passes:

   ```sh
   ./scripts/verify-post-upgrade-plugin-refresh.sh \
     --skip gemini='legacy extension is not installed on this machine' \
     --skip antigravity='Antigravity is not used on this machine' \
     --skip grok='Grok Build is not used on this machine' \
     --skip kimi='Kimi Code is not used on this machine'
   ```

   Replace the sample skips with the hosts intentionally absent from this machine. Do **not** use a skip to hide a stale installed package. The gate runs `traceary doctor --client <host> --json --warnings-ok`, reads only each host's canonical plugin check name/status (`grok-plugin` for Grok; `*-plugin-version` for the other hosts), and fails on `warn` or `fail`; it never emits prompt, transcript, command, or database-event bodies.

## Host refresh and verification matrix

| Host | Refresh | Activation | Version verification | Supported skip |
|---|---|---|---|---|
| Claude Code | In Claude Code, run `claude plugins update <Traceary marketplace key>`; `traceary doctor --client claude --json` prints the exact key in `FixCommand`. | Restart Claude Code (or start a new process) so the updated package is loaded. | `traceary doctor --client claude --json` → `claude-plugin-version` is `pass`. | `--skip claude='reason'` only when Claude Code/its Traceary package is intentionally not installed. |
| Codex | Reinstall the plugin from `plugins/traceary/` in the checkout that matches `traceary -v`; see the Codex plugin documentation. | Use `/plugins` to refresh/reinstall the package, then start a new Codex session. | `traceary doctor --client codex --json` → `codex-plugin-version` is `pass`. | `--skip codex='reason'` only when Codex/its Traceary package is intentionally not installed. |
| Gemini CLI (legacy extension) | `gemini extensions update traceary`. | Restart Gemini CLI. | `traceary doctor --client gemini --json` → `gemini-plugin-version` is `pass`. | `--skip gemini='reason'` only when the legacy extension is intentionally not installed. |
| Antigravity | Use the safe dual-path procedure below, then `agy plugin install integrations/antigravity-plugin`. | Quit and reopen Antigravity (or start a new CLI session). | `traceary doctor --client antigravity --json` → every `antigravity-plugin-version` is `pass`, or an incomplete twin is the documented `skip` while another copy passes. | `--skip antigravity='reason'` only when Antigravity/its Traceary package is intentionally not installed. |
| Grok Build | `./scripts/install-grok-plugin.sh`. | Restart Grok Build or start a new session. | `traceary doctor --client grok --json` → `grok-plugin` is `pass`. | `--skip grok='reason'` only when Grok Build/its Traceary package is intentionally not installed. |
| Kimi Code | `./scripts/install-kimi-plugin.sh`. The installer stages a new generation and atomically flips the managed `traceary` symlink while preserving the install record. | Run `/plugins reload` **or start a new Kimi session**. | `traceary doctor --client kimi --json` → `kimi-plugin-version` and native `kimi-plugin` are healthy. | `--skip kimi='reason'` only when Kimi Code/its Traceary package is intentionally not installed. |

Prefer the `FixCommand` printed by doctor when it provides an exact non-interactive command. Do not invent host CLI flags.

## Antigravity stale dual-path remediation

Antigravity can retain two independently materialized packages:

- `~/.gemini/config/plugins/traceary`
- `~/.gemini/antigravity-cli/plugins/traceary`

A previously Gemini-imported copy can have the old top-level `{"hooks": ...}` shape. In that state `agy plugin install` can report success without replacing the stale CLI-path package. First run `traceary doctor --client antigravity --json`; if it reports a stale or mismatched path, stop Antigravity and **quarantine only that failing copy** before reinstalling. Do not remove a copy that doctor reports as version-aligned.

```sh
# Run from the matching Traceary release checkout. Replace PATH with the
# failing path reported by doctor; the move is reversible.
stale_path="$HOME/.gemini/antigravity-cli/plugins/traceary"
quarantine_path="${stale_path}.stale.$(date +%Y%m%d%H%M%S)"
test ! -e "$quarantine_path"
mv "$stale_path" "$quarantine_path"
agy plugin install integrations/antigravity-plugin
traceary doctor --client antigravity --json
```

If the other path is a healthy package but an incomplete leftover twin has no `version`, doctor reports the twin as `skip`; this is the only automatic dual-path skip. Quarantine the unused incomplete directory after confirming the healthy route, then rerun doctor. Direct hooks are an alternative when the CLI package is not required:

```sh
traceary hooks install --client antigravity --upgrade
```

## Homebrew note

`brew upgrade traceary` never rewrites host plugin caches. Treat plugin refresh as a required post-upgrade step on dogfood machines.

## Related

- [Release guide](./README.md)
- [Integrations overview](../integrations/README.md)
