# #1641 bounded codec compatibility migration

## Structure-Behavior Design Note

### Requirement summary
- Migration 36 must add codec columns without scanning retained payload history.
- Four constant-size counters represent non-identity event body, command, input, and output lanes.
- Transactional triggers keep counters exact for insert/update/delete and roll back with the canonical mutation.
- Already-applied development stores retain their partial indexes and use an explicit legacy-index compatibility mode without a backfill scan.
- Unknown mode/state, negative counts, or underflow fail closed. Scoped search remains bounded and payload rehearsal preflight remains capped.

### Conceptual model
| Concept | State | Behavior | Invariant |
|---|---|---|---|
| Codec compatibility mode | `counter` / `legacy_index` | selects constant counter or historical indexed evidence | fresh shape never parses a missing index |
| Four-lane counter | non-negative lane counts, `valid` state | changes transactionally with canonical rows | equals committed non-identity values; cannot underflow |
| Legacy evidence | four partial indexes | answers existence only | no migration-time validation scan |
| Compatibility decision | identity-only / non-identity / invalid | gates search and rehearsal | invalid evidence fails closed |

### Responsibility assignment
| Responsibility | Owner | Not owner |
|---|---|---|
| zero-scan schema and counter triggers | migration 36 | Go query layer |
| old/new shape marker | forward migration 43 | migration runner heuristics |
| mode-specific global evidence | SQLite compatibility helper | search orchestration |
| bounded scope filter | existing search query | global counter helper |

### Boundaries / interfaces
| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| global non-identity inspection | search/rehearsal | mode, indexes, counters | invalid/unknown evidence returns error (fail closed) |
| scoped codec inspection | search | canonical rows in bounded criteria | existing candidate cap remains authoritative |

### Behavior tests
- Migration 36-43 over a large pre-36 fixture completes below the unchanged one-second cap and creates no partial index on the fresh shape.
- Fresh counter mode tracks all lanes through insert/update/delete and transaction rollback.
- Empty/identity/non-identity transitions cannot underflow; corrupted/unknown state fails closed.
- Legacy index-bearing shape selects `legacy_index` and does not reference counter-only assumptions.
- Concurrent committed mutations produce exact counters.
- Rehearsal run creation succeeds on identity-only counter mode and rejects non-identity/invalid evidence.

### TDD plan
Migration fixture -> counter transition tests -> shared mode query -> search and rehearsal integration -> concurrency/rollback -> full regression.

### Risks / rollback
- The change is pre-release schema correction plus a forward compatibility migration. Canonical payload columns are unchanged.
- Existing index-bearing stores remain readable without scan. Fresh stores can roll back by ignoring the constant state while retaining identity payloads.
- Trigger arithmetic is deliberately explicit rather than generalized dynamic SQL so every lane is reviewable.
