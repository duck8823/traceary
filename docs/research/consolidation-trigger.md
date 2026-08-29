# Consolidation trigger (work + cadence)

[日本語](./consolidation-trigger.ja.md)

Stop-hook consolidation asks a **main** session to write a refinement when it has done
enough **work** since the cover boundary, bounded by a **Stop cadence**. Body-byte
pressure is not a trigger (#2274).

## Rule

```
enabled := min_commands > 0 && stop_cadence > 0
due     := enabled
        && isMainSession
        && command_executed since covers_to >= min_commands   (default 20)
        && (no prior request || transcripts since last request.at_event_id >= stop_cadence)  (default 8)
        && !stop_hook_active
```

- Work is `COUNT(events.kind='command_executed')`. No `command_audits` join, no body reads.
- Turns are `transcript` rows (one per recorded Stop).
- Cadence is the gap **between** requests. The first ask needs no window.
- Sub-agents (`parent_session_id` set, `subagent_kind` non-empty, or `agent` containing `/`) are never asked.
- `consolidation.threshold_bytes` is deprecated: parsed, ignored, one `[WARN]` per process when set.

## Measurement

First-ask share (cadence ignored) is `COUNT(command_executed)` on August 2026
main sessions. The shipped replay SQL is
`infrastructure/sqlite/testdata/consolidation_replay.sql`.

## Rollback

Set `"consolidation": {"min_commands": 0}` in `config.json`. No rebuild.
