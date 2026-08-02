# Search projection rebuild

[日本語](search-projection-rebuild.ja.md)

The search projection is derived and non-authoritative. Canonical events and command audits are never changed by projection lifecycle commands.

Start a generation with `traceary store search-projection start`. Resume one durable bounded batch with `resume`, or run multiple independently committed batches:

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
