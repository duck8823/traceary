# Search projection rebuild

[日本語](search-projection-rebuild.ja.md)

The search projection is derived: it can always be rebuilt from canonical events and command audits, and projection lifecycle commands never change them. Since v0.34 it is what `traceary search` reads when a generation is complete, with events recorded after the rebuild merged in from the canonical tables so results do not go stale between rebuilds.

Stores that have never built a generation do not need an operator command for the first cutover. Every store open runs one bounded unit of generation work (the same shape as the event-search backfill on initialize): start if idle and source events exist, otherwise resume a matching rebuild. The legacy migration-032 index stays authoritative until the generation reaches `complete`. Before old generation rows are reclaimed, a real session-tier query must succeed against the generation under construction. `status` reports before/after physical bytes for the **bounded_search_projection** family only — never the legacy `event_search_*` family.

Operators can still drive the same machinery explicitly. Start a generation with `traceary store search-projection start`. Resume one durable bounded batch with `resume`, or run multiple independently committed batches:

On a store upgraded from before the projection schema, the first resume
batches inventory historical event identities before any payload is decoded.
This phase is explicit in `status`, uses a stable event-ID cursor, and obeys
the same row, stored-byte, logical-write-byte, wall-time, and lock-time caps.
Restarting the process resumes from the last atomic cursor. A concurrent
canonical mutation invalidates the generation instead of accepting a partial
inventory. Stores populated by the former migration-38 behavior and new empty
stores skip this phase without scanning the canonical table.

```sh
traceary store search-projection resume --until-complete --max-batches 4000 --total-wall-time 8h
```

Row, stored-byte, decoded-byte, logical-write-byte, lock-time, and per-batch wall-time limits still apply to every batch. Cancellation preserves the last committed checkpoint; run the same command again to continue.

Use `traceary store search-projection abort` to idempotently abandon an incomplete generation before restarting with different generation settings. An active completed generation is never abandoned. Inspect `status` for generation lifecycle, checkpoint, high-water, and capacity evidence. Completion alone does not authorize cutover; parity evidence is still required.
