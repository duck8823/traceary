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

   This gate is valid against the live default store path, including a store
   at or above the 2 GiB bounded-doctor threshold: the host package identity
   family (`*-plugin-version` and the native Grok/Kimi plugin checks) reads
   only host manifests, host plugin caches, and host CLI probes, so it stays
   in the report even when doctor returns `mode: "metadata_only_large_store"`.
   A large store is **not** a reason to `--skip` a host — `--skip` is reserved
   for hosts that are intentionally not installed on this machine.

## Host refresh and verification matrix

| Host | Refresh | Activation | Version verification | Supported skip |
|---|---|---|---|---|
| Claude Code | Headless: `claude plugin marketplace update traceary-plugins`, then `claude plugin update traceary@traceary-plugins` (user scope) and the same command with `--scope local`. The bare name `claude plugin update traceary` fails with "Plugin not found". Interactive `/plugin update traceary` remains valid. Restart after update; resumed sessions may keep old snapshot hooks until fully restarted. | Restart Claude Code (or start a new process) so the updated package is loaded. | `traceary doctor --client claude --json` → `claude-plugin-version` is `pass`. | `--skip claude='reason'` only when Claude Code/its Traceary package is intentionally not installed. |
| Codex | Run `codex plugin marketplace list` to identify the root. Git-clone: `git checkout` the tag matching `traceary -v` in that clone. Local-path (checkout is the marketplace root; manifest `.agents/plugins/marketplace.json`): `git -C <marketplace-root> checkout` the same tag. Then `codex plugin add traceary@traceary-marketplace`. Do not use `codex plugin marketplace upgrade` on a local-path marketplace (it reports "No configured Git marketplaces"). Interactive `/plugins` remains valid. | Start a new Codex session after the add (or `/plugins` refresh). | `traceary doctor --client codex --json` → `codex-plugin-version` is `pass`. | `--skip codex='reason'` only when Codex/its Traceary package is intentionally not installed. |
| Gemini CLI (legacy extension) | `./scripts/install-gemini-extension.sh` (uninstall + `install --consent`, tag-pinned). Then refresh managed hook generation (see below). | Restart Gemini CLI. | `traceary doctor --client gemini --json` → `gemini-plugin-version` is `pass` and `gemini-config` is `pass`. | `--skip gemini='reason'` only when the legacy extension is intentionally not installed. |
| Antigravity | From the checkout matching `traceary -v`, `rsync -a --delete integrations/antigravity-plugin/` onto `~/.gemini/config/plugins/traceary/` (and the legacy `~/.gemini/antigravity-cli/plugins/traceary/` copy if present), or `agy plugin install integrations/antigravity-plugin`. If doctor still reports a stale twin, use the dual-path procedure below. | Quit and reopen Antigravity (or start a new CLI session). | `traceary doctor --client antigravity --json` → every `antigravity-plugin-version` is `pass`, or an incomplete twin is the documented `skip` while another copy passes. | `--skip antigravity='reason'` only when Antigravity/its Traceary package is intentionally not installed. |
| Grok Build | `./scripts/install-grok-plugin.sh`. | Restart Grok Build or start a new session. | `traceary doctor --client grok --json` → `grok-plugin` is `pass`. | `--skip grok='reason'` only when Grok Build/its Traceary package is intentionally not installed. |
| Kimi Code | `./scripts/install-kimi-plugin.sh`. The installer stages a new generation and atomically flips the managed `traceary` symlink while preserving the install record. | Run `/plugins reload` **or start a new Kimi session**. | `traceary doctor --client kimi --json` → `kimi-plugin-version` and native `kimi-plugin` are healthy. | `--skip kimi='reason'` only when Kimi Code/its Traceary package is intentionally not installed. |

Prefer the `FixCommand` printed by doctor when it provides an exact non-interactive command. Do not invent host CLI flags.

Note on `--scope local` installs: a local install row in Claude's `~/.claude/plugins/installed_plugins.json` survives the deletion of the project directory it points at. Doctor reports those rows as the additive `claude-plugin-local-leftovers` WARN (count plus a bounded sample of the missing paths); the user-cache `claude-plugin-cache` / `claude-plugin-version` checks stay `pass`. The WARN carries a FixCommand — `traceary doctor --fix --dry-run --client claude` lists every leftover path, and `traceary doctor --fix` prints the same list as a no-op. Traceary never rewrites Claude's inventory — no host CLI can uninstall a local-scope row whose project directory is gone, so inspect `installed_plugins.json` and remove the leftover local rows in Claude yourself.

## Codex headless probes require a trusted git directory

A headless `codex exec` probe (the shape `scripts/smoke_test_integrations.sh`
uses with `TRACEARY_ENABLE_CODEX_RUNTIME_SMOKE=1`) fails immediately from a
non-git directory:

```
Not inside a trusted directory and --skip-git-repo-check was not specified.
```

Do not bypass this Codex host policy with `--skip-git-repo-check` or
`-a never`. Run the probe from a git root with a throwaway store instead:

```sh
tmp_db_dir="$(mktemp -d)"
TRACEARY_DB_PATH="${tmp_db_dir}/traceary.db" \
  codex exec -C <traceary-git-root> -s read-only '<probe prompt>'
rm -rf "${tmp_db_dir}"
```

The hooks then record `session_started` / `prompt` / `transcript` into the
throwaway store; no `session_ended` is synthesized. See
[Codex plugin](../integrations/codex-plugin.md#headless-codex-exec-probes-require-a-trusted-git-directory)
for the full probe recipe.

## Stale processes

Homebrew / `go install` upgrades replace the on-PATH binary, but **already-running processes keep the old executable**. The 2026-08-18 dogfood found long-lived `traceary mcp-server` processes on superseded Cellar binaries (0.32–0.34). MCP was retired in v0.35.0; those processes predate the store-lease protocol, so a stale host session that talks to them mid-compact can write without exclusive access.

`traceary doctor` reports this as the `stale-processes` check (store-independent, ps-level). It WARNs with pid, version, age, and reap guidance, and PASSes silently when none are running. Plugin-cache WARNs do **not** cover live processes.

After every binary upgrade:

1. Run `traceary doctor --json` and inspect `stale-processes`.
2. Quit the host session that launched each pid so it cannot write without a store lease.
3. Confirm with `ps -p <pid>`, then `kill <pid>` only if the process is unused.
4. Remove leftover `mcp-server` entries from host config. See [MCP retirement](../mcp/README.md).

Do not treat a `pass` plugin-version check as proof that no stale binaries are still running.

## Gemini managed hook generation refresh

`./scripts/install-gemini-extension.sh` replaces the extension package in
`~/.gemini/extensions/traceary/` (uninstall then `install --consent`, pinned
to the running CLI tag). It does **not** rewrite the Traceary-managed hook
entries already present in `~/.gemini/settings.json`. Those entries were
written by an older hook generation and may have stale timeouts (for example,
5000 ms instead of the current 10000 ms). Doctor reports this as
`gemini-config=warn` with an `installed=…ms desired=…ms` drift message.

Do not run `gemini extensions update traceary` for a local install — that
command prompts interactively. After the script, run:

```sh
# Preview — shows only what would change in Traceary-managed entries.
traceary doctor --fix --dry-run --client gemini --project-dir <dir>

# Apply — rewrites only Traceary-managed entries; non-Traceary hooks are preserved.
traceary doctor --fix --client gemini --project-dir <dir>
```

Only apply if the dry-run output touches Traceary-managed hook entries only.
After applying, rerun `traceary doctor --client gemini --json` and confirm
both `gemini-plugin-version` and `gemini-config` are `pass`.

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
