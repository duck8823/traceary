# Search projection rebuild

[日本語](search-projection-rebuild.ja.md)

The search projection is derived: it can always be rebuilt from canonical events and command audits, and projection lifecycle commands never change them. Since v0.34 it is what `traceary search` reads when a generation is complete, with events recorded after the rebuild merged in from the canonical tables so results do not go stale between rebuilds.

Stores that have never built a generation do not need an operator command. Every store open runs one bounded unit of generation work: start if idle and source events exist, otherwise resume a matching rebuild. Search works throughout — a generation that is not yet `complete` only means the fingerprint pre-filter is unavailable, so candidates are decoded directly and results stay correct. Before old generation rows are reclaimed, a real session-tier query must succeed against the generation under construction. `status` reports before/after physical bytes for the **bounded_search_projection** family only.

Operators can still drive the same machinery explicitly. Start a generation with `traceary store search-projection start`. Resume one durable bounded batch with `resume`, or run multiple independently committed batches:

On a store upgraded from before the projection schema, the first resume
batches inventory historical event identities before any payload is decoded.
This phase is explicit in `status`, uses a stable event-ID cursor, and obeys
the same row, stored-byte, logical-write-byte, wall-time, and lock-time caps.
Restarting the process resumes from the last atomic cursor. Concurrent
**updates or deletes** of historical rows invalidate the generation instead of
accepting a partial inventory. Live **inserts** do not: the events insert
trigger registers the new identity into `search_projection_source_sequence`
unconditionally, so inventory has no extra work for that row and hooks that
write on every store open can still reach `complete`. Stores populated by the
former migration-38 behavior and new empty stores skip this phase without
scanning the canonical table.

If an operator starts a generation with a non-default budget and leaves it
incomplete, automatic catch-up on store open skips rather than hijacking that
budget. Skips are logged at warning level with the reason; resume or abort with
the matching budget to unblock progress.

A generation that **failed** is parked, not retried. Every failure class the
store records is deterministic — an oversize row exceeds the same budget on
every open, `session_tier_unverified` fails the same query, and `abandoned` is
an operator decision — so restarting automatically would fail identically and
append a lifecycle row per open. Automatic catch-up skips with a warning naming
the class. Neither `resume` nor `abort` clears it — `resume` rejects a failed
generation and `abort` leaves the row failed as `abandoned` — so recovery is an
explicit `traceary store search-projection start`.

The before/after family byte figures are diagnostic and are measured outside the
transactions that start and complete a generation, under their own short
deadline, on a context detached from the batch. A measurement that cannot run
never fails a generation: `status` reports `cutover_before_evidence.status` and
`cutover_after_evidence.status` as `unavailable` with a reason, so a zero byte
figure is never mistaken for a genuinely empty family. The two are separate
because they are measured at different times against families of different
sizes; an empty status means no measurement has been attempted yet.

```sh
traceary store search-projection resume --until-complete --max-batches 4000 --total-wall-time 8h
```

Row, stored-byte, decoded-byte, logical-write-byte, lock-time, and per-batch wall-time limits still apply to every batch. Cancellation preserves the last committed checkpoint; run the same command again to continue.

Use `traceary store search-projection abort` to idempotently abandon an incomplete generation before restarting with different generation settings. An active completed generation is never abandoned. Inspect `status` for generation lifecycle, checkpoint, high-water, and capacity evidence.

Since v0.34 this projection is the only search index: the full-corpus migration-032 family it once ran beside is retired, so there is no cutover to authorize and no second index to compare against. See [search retirement](operations/search-retirement.md).

## Index-family budget

The operator-facing budget is **physical bytes of the bounded search index family**
(`search_projection_*` + `literal_search_*`), measured as active b-tree allocation
via `dbstat` — not source text. The default is 1464 MiB (~1.43 GiB), the residual of
the 4 GiB store gate after every other Wave 3 removal.

`status.recent_bytes` is deliberately a **different unit**: source text actually
retained in the recent tier. The budget is configured in index bytes; retained
source is reported so the amplification is visible, not so operators re-interpret
the knob as a text ceiling.

What the budget buys is a **variable window**, not a fixed one. Trigram measures
about 2.16× the source text, so 1464 MiB of family is roughly 0.66 GiB of
indexable text. Measured weekly volume on the reference corpus varies **eightfold**
(0.06 to 0.47 GiB per week): about 1.5 to 2 weeks at the median rate, under a week
during a heavy sprint, and four to five weeks during a quiet one. Compression buys
**losslessness, not reach** — the index is built over plaintext, so a compressed
body occupies exactly as much index as an uncompressed one. Everything older than
the window stays reachable through the session tier.

### Guarantee

After a generation completes and the previous generation has been reclaimed, the
measured active b-tree allocation of the bounded search index family is at or below
the configured budget. The figure is `dbstat` allocation, not file size: the file
shrinks at `store compact`, and FTS5 returns space from deleted documents only as
segments merge.

**Not guaranteed during a rebuild.** `Start` keeps the previous generation readable
until the new one is verified, so a rebuild transiently holds two families. The
source-phase cutoff keeps the new one from being built at the full age window, but the
transient peak is not bounded by this budget.

Three numbers must not be conflated:

1. **dbstat allocation** — active b-tree pages attributed to the family (the budget unit)
2. **file size after `store compact`** — shrinks only after `VACUUM`
3. **rebuild disk peak** — two generations coexist until cleanup reclaims the old one
