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
| `TRACEARY_REPO` | Override the auxiliary work-context identifier |
| `TRACEARY_ALLOW_SECRETS` | Disable best-effort secret redaction for `traceary audit` |
| `TRACEARY_MAX_AUDIT_INPUT_BYTES` | Default stored input size limit for `traceary audit` |
| `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` | Default stored output size limit for `traceary audit` |

## Hook-related environment variables

| Variable | Purpose |
| --- | --- |
| `TRACEARY_BIN` | Override the `traceary` binary path used by generated hooks |
| `TRACEARY_HOOK_SCRIPTS_DIR` | Override where portable hook scripts are materialized |
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
| `redact.extra_patterns` | string array | Extra regex patterns for audit redaction. Each entry is compiled as a Go `regexp` pattern and matched content is replaced with `[REDACTED]`. Applied after the built-in redaction rules in both the CLI (`traceary audit`) and MCP server (`add_audit`). |

Example:

```json
{
  "redact": {
    "extra_patterns": ["my_custom_secret", "internal_auth_header:\\s*\\S+"]
  }
}
```

If the file does not exist, Traceary uses the built-in redaction patterns only.

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
- secret redaction is best effort, not a complete data-loss-prevention system

## Related docs

- CLI command surface: [`../cli/README.md`](../cli/README.md)
- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- backup flow: [`../backup/README.md`](../backup/README.md)
