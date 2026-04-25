# Environment and runtime reference

[日本語](./README.ja.md)

This page centralizes Traceary's environment variables, runtime assumptions, and public support promises.

## DB and operator-facing environment variables

| Variable | Purpose |
| --- | --- |
| `TRACEARY_DB_PATH` | Override the SQLite DB path for all CLI commands |
| `TRACEARY_LANG` | Switch operator-facing CLI messages (`en` default, `ja` supported) |
| `TRACEARY_CLIENT` | Default `client` attribution for `log`, `audit`, and session commands |
| `TRACEARY_AGENT` | Default `agent` attribution for `log`, `audit`, and session commands |
| `TRACEARY_SESSION_ID` | Default session ID for `log`, `audit`, and `session end` |
| `TRACEARY_WORKSPACE` | Override the auxiliary work-context identifier |
| `TRACEARY_ALLOW_SECRETS` | Disable best-effort secret redaction for `traceary audit` |
| `TRACEARY_MAX_AUDIT_INPUT_BYTES` | Default stored input size limit for `traceary audit` |
| `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` | Default stored output size limit for `traceary audit` |

## Hook-related environment variables

| Variable | Purpose |
| --- | --- |
| `TRACEARY_BIN` | Override the `traceary` binary path used by generated hooks |
| `TRACEARY_HOOK_STATE_DIR` | Override the temporary directory for hook session state |
| `TRACEARY_HOOK_STATE_KEY` | Override the per-process hook state key when the default is not suitable |

## Logging and diagnostics environment variables

| Variable | Purpose |
| --- | --- |
| `LOG_LEVEL` | Configure structured log verbosity (`debug`, `info`, `warn`, `error`) |
| `LOG_OPTION` | Use `development` for text logs with source info; default is JSON logs |

## Configuration file

Traceary reads an optional JSON configuration file from `~/.config/traceary/config.json`.

| Key | Type | Purpose |
| --- | --- | --- |
| `redact.extra_patterns` | string array | Extra regex patterns for audit and transcript redaction. Each entry is compiled as a Go `regexp` pattern and matched content is replaced with `[REDACTED]`. Applied after the built-in redaction rules in both the CLI (`traceary audit`, `traceary log --kind transcript`, Claude Stop-hook transcript capture) and MCP server (`record_event(type="audit")`, `record_event(type="log")` with `kind=transcript`). |
| `read.fields` | string array | Default compact column order for `traceary tail` / `list` / `search` text output when `--fields` is omitted. Accepted field names: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`. Unknown / empty / duplicate entries are rejected at command runtime; the `--fields` flag always overrides this setting. Does not affect `--wide` or `--json` output. |
| `read.presets` | object | Named saved views for `traceary tail` / `list` / `search`, applied via `--preset <name>`. Each entry can set `fields` (same registry as `read.fields`) and `filters` (any of `kind`, `failures`, `workspace`, `session_id`, `client`, `agent`). Explicit CLI flags always override preset values. Entries whose name collides with a built-in preset (`failures`, `prompts-only`, `compact-summaries`) win over the built-in but emit a `[WARN]` line on stderr. |
| `read.color` | string | Default `--color` mode for `traceary tail` / `list` / `search` compact text output. Accepted values: `auto`, `always`, `never`. `auto` colorizes only when stdout is a TTY. `NO_COLOR` env and explicit `--color=never` always win over the config. `--wide` and `--json` output are always uncolored regardless of this setting. |

Example:

```json
{
  "redact": {
    "extra_patterns": ["my_custom_secret", "internal_auth_header:\\s*\\S+"]
  },
  "read": {
    "fields": ["ts", "kind", "session", "ws", "message"],
    "presets": {
      "my-view": {
        "fields": ["ts", "kind", "message"],
        "filters": {
          "kind": "prompt",
          "failures": true
        }
      }
    }
  }
}
```

If the file does not exist, Traceary uses the built-in defaults for every config-backed feature.
If the file exists but is unreadable or invalid JSON, Traceary falls back to the built-in defaults, emits an operator-visible warning when a config-backed feature would have been used, and reports the broken state in `traceary doctor`.

## Runtime assumptions

- Traceary is local-first and stores data in SQLite on the current machine
- the core CLI and `traceary mcp-server` are actively tested on macOS and Linux
- release archives are currently published for macOS and Linux
- hooks currently assume `bash` and Unix-like shell semantics
- `git` is optional; when available, Traceary prefers a normalized `remote.origin.url`, then falls back to the local git worktree root before giving up on automatic work-context detection
- native Windows PowerShell / `cmd.exe` hook workflows are not supported today; use WSL or another POSIX-compatible environment when you need hooks on Windows

## Privacy posture

- Traceary has no hosted service requirement
- Traceary does not send telemetry to a Traceary-owned backend
- when you run `traceary audit`, payloads are written to your local SQLite store unless redaction or truncation changes them first
- `prompt` events (from `UserPromptSubmit` hooks) and `compact_summary` events (from `PostCompact` hooks) are stored as-is without redaction or truncation — this is by design, as recording the user's intent is a core purpose of Traceary
- `transcript` events (from Claude `Stop` hook, `traceary log --kind transcript`, or MCP `record_event(type="log")` with `kind=transcript`) are redacted in the same way as `audit` events (built-in redactors plus `redact.extra_patterns`) because assistant transcripts routinely re-state shell output and file contents that include secrets
- secret redaction is best effort, not a complete data-loss-prevention system

## Related docs

- CLI command surface: [`../cli/README.md`](../cli/README.md)
- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- backup flow: [`../backup/README.md`](../backup/README.md)
