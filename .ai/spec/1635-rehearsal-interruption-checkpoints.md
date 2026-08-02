# #1635 deterministic rehearsal interruption checkpoints

## Requirement and design

Add a rehearsal-only `--stop-after-batches N` boundary. Zero preserves the
existing behavior. A positive value counts only batches reported after a
successful `AdvanceField`; after exactly N committed batches the use case
persists `paused`, releases the handle, and returns success with aggregate
`batch_count`, row/byte counters, and `more_pending=true`. The stop value is
orchestration policy, not codec configuration, so a fresh resume may choose a
new stop count without invalidating the persisted target/config binding.

The application use case owns the boundary. SQLite continues to own the atomic
shadow-row/checkpoint commit. Cancellation or an error before a successful
advance follows the existing pause/error path and cannot be reported as a
deterministic stop. Completion, scrub, rollback, live-target fencing, and the
v0.34 activation prohibition are unchanged.

## Behavior/TDD and rollback

- Stop after one or multiple committed batches and return paused success.
- Resume counts only newly committed batches; persisted checkpoints prevent
  reprocessing and duplicate shadow rows.
- Cancellation before commit does not advance; cancellation after commit is
  reconciled by the atomic checkpoint on resume.
- A stopped run remains compatible with scrub after completion and physical
  rollback while paused.
- Negative limits fail validation; zero is backward compatible.

Rollback is removal of the additive CLI/config/result fields. Persisted schema
and codec data are unchanged. Self-review focuses on counting committed
results, avoiding stop-policy inclusion in `config_hash`, and never returning
`more_pending` for completed work.
