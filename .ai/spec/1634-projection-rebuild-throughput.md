## Structure-Behavior Design Note

### Requirement summary
- A single bounded CLI command may durably execute multiple projection batches, reducing the measured 453,161-row/100-row-per-resume workflow from an estimated 5.8 hours of manual invocations.
- Every batch retains existing row, byte, lock, and wall-time limits and commits its own checkpoint.
- Cancellation or interruption stops between batches and a later invocation resumes from the durable checkpoint.
- An incomplete derived generation can be explicitly and idempotently abandoned, then restarted with a new immutable configuration.
- The active generation is never incomplete or abandoned; canonical event and audit history is never mutated.
- Concurrent resume attempts are fenced by persisted generation/checkpoint/revision compare-and-set.
- Status exposes lifecycle and throughput evidence needed to decide resume, abandon, restart, parity, and cutover eligibility.

### Conceptual model
| Concept | State | Behavior | Invariant |
|---|---|---|---|
| Generation | rebuilding, complete, drifted, failed, abandoned | transitions through bounded batches | only complete generation may become active |
| Batch runner | batch count, elapsed time, accumulated progress | repeats one-batch Resume | stops at command bounds or terminal state; each batch is independently durable |
| Resume fence | generation ID, source revision, phase, checkpoint | rejects stale concurrent apply | no checkpoint regression or duplicate authority transition |
| Abandon command | expected incomplete generation | marks generation abandoned and schedules bounded derived cleanup | idempotent; never changes canonical tables or active complete generation |
| Restart | new config hash and generation ID | starts after abandon | config is immutable within a generation |
| Eligibility | completion, parity evidence, active identity | permits cutover consideration | abandoned/incomplete generation is never eligible |

### Responsibility assignment
| Responsibility | Owner | Not owner |
|---|---|---|
| Multi-batch bounds and stop policy | application use case | CLI does not loop persistence directly |
| One-batch selection/apply and CAS fence | SQLite adapter | use case does not know SQL/table names |
| Lifecycle transition and active-authority invariant | application plan + SQLite atomic transition | presentation does not infer state |
| Flag parsing/output | CLI | CLI does not mutate projection state itself |
| Canonical history | existing event/audit stores | projection lifecycle never writes it |

### State transitions
`idle|complete|failed|drifted|abandoned -> Start(new generation, immutable config) -> rebuilding -> source -> eviction -> cleanup -> complete(active)`.

`rebuilding|failed|drifted -> Abandon -> abandoned/cleanup -> abandoned`; repeated abandon returns the same terminal observation.

Concurrent Resume uses the existing generation/revision/checkpoint/phase CAS. A loser receives drift/no-progress and cannot overwrite the winner.

### Behavior tests / TDD
| Behavior | Red | Green |
|---|---|---|
| bounded multi-batch | runner test with max batches and terminal completion | `ResumeUntil` orchestration accumulates progress and stops exactly at bound |
| interruption | canceled context after committed batch | return durable partial result; next call continues checkpoint |
| abandon/restart | incomplete generation with old config | idempotent abandon, then new generation/config; old never active |
| fencing | two resumes from same checkpoint | one commit succeeds, stale apply rejected |
| observability | running/stopped/abandoned states | status/result includes batches, checkpoint, reason, elapsed |
| parity/cutover | incomplete/abandoned/complete generation | only completed active generation can be eligible |
| canonical safety | hash/count canonical rows before lifecycle operations | unchanged afterward |

### Rollback / migration
- Schema change is additive. Existing one-batch `resume` remains the primitive and compatibility path.
- Multi-batch mode can be disabled without changing durable checkpoints.
- Abandon only changes derived projection state and deletes derived rows through existing bounded cleanup; it never deletes events/audits.
- Roll back CLI exposure if cancellation, fencing, or active-authority tests fail. Existing generations remain resumable one batch at a time.

### Self-review checkpoint
- Adopt application-owned multi-batch orchestration, not a CLI loop.
- Keep one transaction per batch; never widen transaction/lock duration to total command wall time.
- Reuse persisted CAS fence and byte ledgers.
- Add an explicit abandon transition rather than overloading Start or a boolean restart flag.
- Do not infer cutover eligibility from row counts alone; require completed active identity and parity evidence.
