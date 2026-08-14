# Decision: what `failed=1` means (#1767)

[日本語](./failed-flag-meaning.ja.md)

**Status:** decided. `list --failures` stays.

**Date:** 2026-08-15

**Issue:** #1767

## Decision

`command_audits.failed = 1` is a compatibility flag for structured command failure. On the current write path it is derived from `failure_reason.IsFailure()` and is never set independently. `list --failures` (and the same predicate on `search` / `tail`) matches `failed = 1` **or** a captured non-zero `exit_code`. Hosts still almost never report exit codes, so the flag half is the live surface.

Keep that surface. Do not move it into `doctor`. Do not add a CHECK that forbids `failed=1 AND failure_reason='unknown'`: restore must keep pre-classifier rows. New writes already cannot persist that pair.

## Answers

### 1. Is `unknown` the schema default leaking onto pre-classifier rows?

Yes. Migration `000025_normalize_command_audits.sql` added `failure_reason TEXT NOT NULL DEFAULT 'unknown'`. Before `11691479` (2026-07-22, `feat: normalize command audit outcomes`), hook audits set `Failed` from `hookPayloadFailed` and did not classify a reason, so the DEFAULT landed on those rows. After that commit, a structural hook failure is `host_error`. `b1daa0e7` (same day) set `Failed` from `failureReason.IsFailure()`, so the flag and the reason stay aligned.

`unknown` itself is not a failure reason: `CommandFailureReason.IsFailure()` is false for `unknown` and `none`.

### 2. What changed around 2026-07-21/24, and can anything still write `unknown` with `failed=1`?

The classifier landed on 2026-07-22. A live corpus that stops writing `unknown`+`failed=1` just before that date and starts writing `host_error` just after is the upgrade window, not two coexisting kinds of failure.

Current writes cannot persist the pair:

- `hookPayloadFailureReason`: structured host error → `host_error`; no evidence → `unknown` with `Failed=false`.
- `ClassifyOutcome`: `unknown` + structural failure and no exit code → `host_error`.
- Restore (`CommandAuditFromSnapshot`) is the only path that still accepts `unknown` + `failed=true`, so historical rows remain readable.

### 3. Where does current `host_error` come from?

One mechanism, several hosts, always `client=hook`:

| Host | Structured signal | Reason |
|---|---|---|
| Claude Code | top-level `error` on `PostToolUseFailure` | `host_error` |
| Gemini CLI | `tool_response.error` (spawn/OS-level only; a plain non-zero shell exit is not reported) | `host_error` |
| Codex | no structured failure field | not flagged |
| Grok | `PermissionDenied` / exact `🚫 [hook]` marker | `hook_denied`, not `host_error` |
| Interrupt / timeout markers | `is_interrupt`, `timed_out`, … | `signal` / `timeout` |

This is source contract, not a live-store count.

### 4. Does the operator want these rows on `list --failures`?

Yes. `host_error` is a host-reported tool failure (Claude `PostToolUseFailure`, Gemini spawn error). That is 記録, the same class as `hook_denied` / `signal` / `timeout` / `exit_code`. `doctor` already has retry-loop diagnostics; folding `--failures` into doctor would hide the audit trail. Pre-classifier `unknown`+`failed=1` rows stay visible through the `failed=1` half of the predicate.

### 5. Should `failure_reason` gain a CHECK that makes unclassified failure impossible?

No. The existing CHECK already enumerates allowed reason strings. A new constraint such as `NOT (failed = 1 AND failure_reason = 'unknown')` would reject restore of historical rows. The write path already upgrades unclassified structural failure to `host_error`.

## Non-goals

- Removing or deprecating `list --failures`.
- Querying or rewriting the live store.
- Backfilling historical `unknown` rows to `host_error`.
