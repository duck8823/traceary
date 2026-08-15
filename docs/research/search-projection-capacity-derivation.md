# Search-projection capacity derivation (#1751)

[日本語](./search-projection-capacity-derivation.ja.md)

Scratch-sized measurement of the five derivation defects. Not the live ~36 GiB store.

| Item | Verdict | Size on scratch | Shipped |
|---|---|---|---|
| 1. Re-derivation never retries | Real | Durable miss after `source→eviction` commit | `capacity_rederived` + retry on every eviction apply |
| 2. dbstat + `SUM(decoded_bytes)` not one snapshot | Real | Concurrent delete inflates PPM (physical-before / logical-after) | One read-only transaction |
| 3. Blend + FTS deleted-postings ratchet | Blend: extra query does not change PPM (shared FTS). Ratchet: real if re-derive runs before reclaim | Unmerged deletes leave inverse postings; reclaim does not grow recent bytes | Reclaim then re-derive. No generation-scoped physical walk |
| 4. Over-budget completion has no actuator | Real detector | Status already records `0`; CatchUp stays `already_complete` | `doctor` `search-projection-budget` WARN. No auto-rebuild |
| 5. Upward ceiling cannot re-project prefilter drops | Negligible | Walk ceiling = 4× Start ceiling. Start/re-derive swing on scratch is ≪ 4× | No re-projection pass |

The family-byte budget is still **measured and reported**, not guaranteed. These changes make the *derivation process* retryable and snapshot-consistent; they do not bound rebuild peak bytes.

Tests: `TestSearchProjectionCapacityRederiveRetriesAfterMissedTransition`, `TestSearchProjectionCapacityDerivationUsesConsistentSnapshot`, `TestSearchProjectionCapacityRatchetShrinksAfterFTSReclaim`, `TestSearchProjectionCutoffSlackCoversFourfoldCeilingRaise`, `TestSearchProjectionBudgetDoctorCheck`.
