# Evidence-backed one-shot session repair

[日本語](./one-shot-repair.ja.md)

`traceary session repair-one-shot` repairs historical sessions whose supervising process completed but whose terminal boundary was not recorded. It is intentionally narrower than `session gc`: repair requires explicit per-session process-exit evidence and assigns a typed terminal reason. It never infers one-shot execution from transcript text, idle time, workspace membership, or a missing end hook.

## Evidence manifest

The command accepts schema `one-shot-repair-evidence/v1`:

```json
{
  "schema_version": "one-shot-repair-evidence/v1",
  "entries": [
    {
      "session_id": "session-example",
      "runtime_mode": "one_shot",
      "terminal_reason": "success",
      "completed_at": "2026-07-21T10:00:00Z",
      "evidence_source": "operator_attested_process_exit",
      "evidence_ref": "batch-run:42"
    }
  ]
}
```

Every entry must explicitly assert `runtime_mode=one_shot`, use a non-legacy terminal reason, provide a completion timestamp, and identify authoritative process-exit evidence. Accepted evidence sources are:

- `supervised_process_exit`
- `codex_exec_exit`
- `batch_runner_exit`
- `operator_attested_process_exit`

The evidence reference is printed for review but is not copied into event bodies. Keep it non-sensitive and stable, such as a run ID or SHA-256 digest.

## Dry-run first

Dry-run is the default:

```sh
traceary session repair-one-shot \
  --evidence-file ./one-shot-evidence.json \
  --stale-after 24h \
  --json > one-shot-repair-preview.json
```

The result includes global before/after counts (`active`, `stale`, successful `completed`, and typed `failed`) plus one explanation per manifest entry. Candidate decisions include:

| Decision | Meaning |
|---|---|
| `eligible` | active, stale, and completion is not earlier than stored activity |
| `missing_session` | the manifest ID is absent |
| `already_terminal` | the session is already closed; rerun changes nothing |
| `recently_active` | activity exists inside `--stale-after` |
| `completion_before_start` | evidence timestamp precedes session start |
| `completion_before_latest_activity` | later stored activity contradicts the proposed completion |

An old row stored as `interactive` is not selected automatically. It participates only when its exact session ID has an authoritative one-shot manifest entry. Sessions absent from the manifest are never changed.

## Apply and rollback

Apply requires a new backup path:

```sh
traceary session repair-one-shot \
  --evidence-file ./one-shot-evidence.json \
  --stale-after 24h \
  --apply \
  --backup ./traceary-before-one-shot-repair.db \
  --json > one-shot-repair-applied.json
```

Traceary creates the backup before store initialization and before opening the repair transaction. Dry-run opens the existing database read-only and never creates or migrates a store. All eligible changes and terminal events commit together; an error or interruption before commit rolls back the complete run. Re-running the same evidence after a successful commit reports `already_terminal` and appends no duplicate events.

To roll back the committed repair, stop Traceary writers and restore the mandatory backup:

```sh
traceary store backup restore \
  --input ./traceary-before-one-shot-repair.db \
  --db-path ~/.config/traceary/traceary.db \
  --force --yes
```

Restoring replaces the complete store snapshot, so events written after the backup are also removed. Prefer a maintenance window and retain the apply JSON as an audit record.
