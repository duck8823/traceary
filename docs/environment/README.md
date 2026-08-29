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
| `audit.max_input_bytes` | integer | Default stored input byte limit for command-audit payloads. `0` uses the built-in default, `traceary audit --max-input-bytes` and `TRACEARY_MAX_AUDIT_INPUT_BYTES` override it for CLI/hook processes. |
| `audit.max_output_bytes` | integer | Default stored output byte limit for command-audit payloads. `0` uses the built-in default, `traceary audit --max-output-bytes` and `TRACEARY_MAX_AUDIT_OUTPUT_BYTES` override it for CLI/hook processes. |
| `ui.language` | string | Default operator-facing CLI/TUI language when `TRACEARY_LANG` is not set. Supported values: `en`, `ja`. `TRACEARY_LANG` remains the process-local override. |
| `redact.extra_patterns` | string array | Backward-compatible extra regex patterns for audit and transcript redaction. Each entry is compiled as a Go `regexp` pattern and matched content is replaced with `[REDACTED]`. Applied after the built-in redaction rules in the CLI (`traceary audit`, `traceary log --kind transcript`, Claude Stop-hook transcript capture). |
| `redact.rules` | object array | Structured redaction rules applied alongside `extra_patterns`. Rules may be named, scoped to targets (`audit.command`, `audit.input`, `audit.output`, `log.message`), and may set a custom `replacement`. Supported types: `regex` (`pattern`), `field` (`fields` or dotted JSON `paths`), `url` (`query_params` plus built-in URL credential masking), and `context` (`fields` + secret-shaped values such as JWT / long hex / long base64, with optional `min_length`). Built-in redaction remains a safety floor and cannot be disabled. |
| `read.fields` | string array | Default compact column order for `traceary list` / `list --follow` / `search` text output when `--fields` is omitted. Accepted field names: `ts`, `kind`, `session`, `ws`, `client`, `agent`, `message`, `exit_code`, `id`, `source_hook`. Unknown / empty / duplicate entries are rejected at command runtime; the `--fields` flag always overrides this setting. The config default does not affect `--wide` or JSON without explicit `--fields`; explicit JSON `--fields` controls serialized keys and routes body-free selections through metadata reads. |
| `read.presets` | object | Named saved views for `traceary list` / `list --follow` / `search`, applied via `--preset <name>`. Each entry can set `fields` (same registry as `read.fields`) and `filters` (any of `kind`, `failures`, `workspace`, `session_id`, `client`, `agent`). Explicit CLI flags always override preset values. Entries whose name collides with a built-in preset (`failures`, `prompts-only`, `compact-summaries`) win over the built-in but emit a `[WARN]` line on stderr. |
| `read.color` | string | Default `--color` mode for `traceary list` / `list --follow` / `search` compact text output. Accepted values: `auto`, `always`, `never`. `auto` colorizes only when stdout is a TTY. `NO_COLOR` env and explicit `--color=never` always win over the config. `--wide` and `--json` output are always uncolored regardless of this setting. |
| `consolidation.min_commands` | integer | Stop-hook consolidation work threshold. A **main** session (no `parent_session_id`, empty `subagent_kind`, `agent` without `/`) is asked to refine when `command_executed` events strictly after the current refinement's `covers_to` (or the whole session when none exists) reach this count. Default **20** when the key or config file is absent. Explicit **0 disables** the trigger. When the config file is present but unreadable or malformed, the trigger is also **disabled** (0) rather than defaulted. Sub-agent sessions are never asked. Delivery is per host: Claude / Codex / Kimi `Stop` exit 2 + stderr; Codex also injects plain text on the next `UserPromptSubmit`; Gemini `BeforeAgent` writes `hookSpecificOutput.additionalContext`; Antigravity `Stop` writes `{decision:continue,reason}`; Grok `Stop` writes `{decision:block,reason}` and always exits 0 (host acceptance of that envelope is unverified — docs.x.ai still treats `Stop` as passive). A Stop whose payload carries `stop_hook_active: true` never asks. Check failures fail closed (no ask). Independent of `wake_injection.budget_bytes`. |
| `consolidation.stop_cadence` | integer | Minimum `transcript` events after the last consolidation request before the same session may be asked again. The **first** ask needs no window. Default **8**. Explicit **0 disables** the trigger. Unreadable or malformed config disables (0). Same per-host channel set, sub-agent exclusion, `stop_hook_active` guard, and fail-closed check as `min_commands`. Independent of `wake_injection.budget_bytes`. |
| `consolidation.threshold_bytes` | integer | **Deprecated in v0.48.** Parsed and ignored. Body-byte pressure no longer triggers a request. When the key is explicitly set, Stop-hook processes emit one `[WARN]` on stderr. Removed in the next minor. |
| `wake_injection.budget_bytes` | integer | Maximum bytes of finished session summaries written to hook stdout at wake (#1684). Claude / Codex / Gemini / Grok inject on `SessionStart` stdout; Kimi injects on the first `UserPromptSubmit`. Default **8192** (8 KiB) when the key or config file is absent. Explicit **0 disables** injection entirely. When the config file is present but unreadable or malformed, injection is also **disabled** (budget 0) rather than defaulted — so a broken file cannot re-enable injection the operator turned off. Negative values are treated as disabled. Summaries are taken whole, newest first; if even one does not fit, nothing is written. Independent of `consolidation.min_commands`. Antigravity is out of scope (#1714). |

Example:

```json
{
  "audit": {
    "max_input_bytes": 65536,
    "max_output_bytes": 65536
  },
  "ui": {
    "language": "en"
  },
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

Rule precedence is: built-in safety redactors first, built-in structured URL/context safeguards next for audit/transcript payloads, configured structured rules, then configured regex rules (including `extra_patterns`). `field` and `context` rules parse JSON payloads and redact matching keys at any depth; `paths` use dotted object paths such as `request.headers.authorization`. `url` rules redact basic-auth user info in `http://` / `https://` URLs and configured query parameters.

If the file does not exist, Traceary uses the built-in defaults for every config-backed feature.
If the file exists but is unreadable or invalid JSON, Traceary falls back to the built-in defaults, emits an operator-visible warning when a config-backed feature would have been used, and reports the broken state in `traceary doctor`.

## Runtime assumptions

- Traceary is local-first and stores data in SQLite on the current machine
- the core CLI is actively tested on macOS and Linux
- release archives are currently published for macOS and Linux
- hooks currently assume `bash` and Unix-like shell semantics
- `git` is optional; when available, Traceary prefers a normalized `remote.origin.url`, then falls back to the local git worktree root before giving up on automatic work-context detection
- native Windows PowerShell / `cmd.exe` hook workflows are not supported today; use WSL or another POSIX-compatible environment when you need hooks on Windows

## Privacy posture

- Traceary has no hosted service requirement
- Traceary does not send telemetry to a Traceary-owned backend
- when you run `traceary audit`, payloads are written to your local SQLite store unless redaction or truncation changes them first
- command-audit input/output payloads are truncated before persistence when they exceed the configured limit. Truncated payloads keep head and tail context, include an `original_bytes` marker, and set structured `input_truncated` / `output_truncated` metadata; omitted bytes are not recoverable through `traceary show`
- command strings also pass through the built-in best-effort secret redactors before storage. `input_redacted` / `output_redacted` only report input/output payload redaction; they do not expose a separate command-redaction flag
- `prompt` events (from `UserPromptSubmit` hooks) and `compact_summary` events (from `PostCompact` hooks) are stored as-is without redaction or truncation — this is by design, as recording the user's intent is a core purpose of Traceary
- `transcript` events (from Claude `Stop` hook or `traceary log --kind transcript`) are redacted in the same way as `audit` events (built-in redactors plus `redact.rules` and `redact.extra_patterns`) because assistant transcripts routinely re-state shell output and file contents that include secrets
- secret redaction is best effort, not a complete data-loss-prevention system

## Related docs

- CLI command surface: [`../cli/README.md`](../cli/README.md)
- hooks integration: [`../hooks/README.md`](../hooks/README.md)
- backup flow: [`../backup/README.md`](../backup/README.md)
