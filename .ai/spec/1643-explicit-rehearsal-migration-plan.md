# #1643 explicit rehearsal migration journal plan

## Structure-Behavior Design Note

### Requirement summary
- Determine migration work from embedded versions versus `schema_migrations`, never from WAL existence or a rehearsal table heuristic.
- Normalize both exact clone and copied target to verified WAL mode in a separately timed, cancellable operation capped at one second.
- Preflight executes the exact pending set in one `BEGIN IMMEDIATE` through `COMMIT`, measures elapsed time, and observes a non-zero live WAL while its connection remains open.
- Actual execution occurs iff the measured plan says pending and uses the same journal mode and WAL reservation. Any failure after target mutation restores the verified physical backup.

### Conceptual model
| Concept | State | Behavior | Invariant |
|---|---|---|---|
| Migration plan | pending, WAL bytes, elapsed, journal mode | binds clone evidence to target execution | pending implies WAL >= one frame |
| Journal normalization | DELETE/WAL/other | switches and verifies mode under its own deadline | success reports exactly `wal` |
| Exact migration transaction | embedded missing versions | BEGIN IMMEDIATE, migrate, COMMIT | measured duration covers lock through commit |
| Recovery boundary | target unmodified/mutated/restored | restores backup after post-mutation failure | failed start leaves no rehearsal objects |

### Responsibility assignment
- Migration inventory and clone measurement: `payload_rehearsal_migration.go`.
- WAL reservation and transactional execution: existing budgeted mutation session.
- Workflow recovery and evidence publication: payload rehearsal adapter.
- Public aggregate evidence: application payload rehearsal types.

### Behavior tests
- DELETE-journal source with pending versions yields `Pending=true`, verified WAL mode, non-zero WAL, and exact migration elapsed time.
- Current schema yields explicit no-pending skip.
- Timeout/cancellation/mode mismatch fails before run creation.
- WAL zero for pending migration fails closed.
- Any post-target mutation failure restores the backup and leaves zero rehearsal objects.
- Preview followed by stop-after-one produces a paused resumable run while live/source identities remain unchanged.

### Risks / rollback
- Journal switching mutates the copied target, so it is inside the physical-backup recovery boundary.
- Source/live paths remain read-only and are rechecked before every target mutation.
- Rollback restores the verified independent backup; no schema downgrade is attempted in place.
