# Archive-before-GC dogfood decision log (v0.28.0)

[日本語](./archive-dogfood-v0.28.ja.md)

Part of #1373 · epic #1309 · parent #1360.

## Environment

| Item | Value |
|---|---|
| Date (UTC) | 2026-07-17 |
| Host | local dogfood machine (multi-agent Traceary store) |
| Live store path | `~/.config/traceary/traceary.db` (~2.5 GiB before exercise) |
| Exercise path | **copy only** under implementer scratch (live DB not mutated for archive delete) |
| Binary | post-#1371 archive CLI + #1386 IN-list chunk fix |

## Exercises

### 1. Dry-run eligibility

| keep-days | Result |
|---|---|
| 90 | 0 candidates (all hot data within window) |
| 30 | **13 667** candidates (events 8775, command_audits 4881, memories 3, …) |
| 14 | **131 418** candidates after #1386 chunk fix (pre-fix: SQL “too many variables”) |

### 2. Verify-before-delete apply (copy, keep-days=30)

```text
Wrote archive: archive-30d.trcaryar
Rows: 13667
Deleted after verify: 13667
verify: Archive OK
Wall time: ~24.5s
Archive size: ~57 MiB
DB file size after: ~2.2 GiB (from ~2.5 GiB)
```

Notes:

- Delete path ran VACUUM; freelist reclaim is partial on this SQLite build/file layout — still a measurable shrink and correct row removal.
- Package integrity re-verified after apply.

### 3. Stale-active-sessions on the same copy

| Step | Result |
|---|---|
| dry-run `session gc --stale-after 24h` | 623 |
| apply | closed 623 |
| Opportunistic GC #1363 | landed; soft-deadline detach + doctor `--fix` available on live hosts |

### 4. Opt-in automatic mode (#1372)

**Decision for dogfood / default install: keep `retention.mode=disabled`.**

Rationale:

- Fail-closed default matches design note and #1372 non-goal of not flipping default to archive_then_gc.
- Manual path (`store archive create --delete-after-verify`) is proven on multi-GB class stores with #1386.
- Operators who want auto may set `archive_then_gc` + optional `passphrase_env` after reviewing dry-run counts.

No production dogfood machine was left with automatic mode enabled by this exercise.

## Follow-ups filed and disposition

| Issue | Title | Disposition |
|---|---|---|
| #1386 | chunk archive IN queries past SQLite variable limit | Implementation PR (dogfood blocker for short keep-days) |

## Multi-agent notes (this wave)

| Host | Notes |
|---|---|
| Grok | Clean-home plugin smoke passed (#1301 scripts). Marketplace catalog PR to xai-org is external (generator ready at release tag). |
| Claude / Codex / Antigravity / Gemini | Plugin version hygiene docs + dual-path soft-skip (#1361). Live refresh remains operator post-`brew upgrade` step. |
| All hooks | Session GC + optional archive auto piggyback on session start/end; soft-deadline isolated for maintenance. |

## Opt-in decision (authoritative)

1. **Manual archive-before-GC is release-blocking and shipped** (#1370–#1371).
2. **Automatic archive-then-gc ships opt-in only** (#1372); dogfood leaves it **disabled**.
3. **Default production GC without archive** remains available (`store gc`); operators who need cold history use archive first.
4. **No deferral** of #1372 from v0.28.0; deferred-only language on older epic text is superseded by this log and the shipped code.

## Related artifacts (local scratch)

Implementer captures under goal scratch `v028-dogfood/` (dry-run, apply, verify, session-gc logs). Not committed (contains size/path of local store only).
