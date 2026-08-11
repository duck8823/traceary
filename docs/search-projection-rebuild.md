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
via `dbstat` — not source text. The default is 1464 MiB (~1.43 GiB).

### What the budget actually bounds

Only one part of the family is **evictable**, and only that part is held to a
ceiling: `search_projection_recent_documents`, its indexes, and the
`search_projection_recent_fts_*` shadow tables. Eviction has rows to take there
because the recent tier is a window — dropping its oldest document is a supported
outcome that leaves search correct, because everything it drops stays reachable
through the session tier.

The rest of the family is **corpus-proportional by design** and no ceiling bounds
it:

| tier | grows with | why it cannot be evicted |
|---|---|---|
| `search_projection_session_summaries`, `_session_keywords`, `_command_aggregates` | number of sessions | it is the fallback the recent tier evicts *into*; dropping it loses the history outright |
| `literal_search_fingerprints` | number of events | a missing fingerprint is a false negative in the pre-filter, not a slower answer |
| `search_projection_source_sequence`, `_exclusions` | number of events | they are the rebuild's own bookkeeping |

These tiers enter the derivation as the **non-recent reserve**, subtracted from the
budget before the source-text ceiling is computed. That keeps the arithmetic honest
about the family total, and it has a consequence operators should expect: as the
corpus grows, the reserve grows and the ceiling shrinks, so the recent window gets
shorter at a fixed budget. On the reference corpus the session tier and fingerprints
were already 80.5% of a 1464 MiB budget at 36% of one walk. When the reserve reaches
the budget the derived ceiling is 0, the recent tier is built empty, and search falls
back entirely to the session tier — reported, not silently degraded (`capacity_evidence`
reads `non-recent reserve at or above index-family budget`).

Raising `--index-family-bytes` buys recent-window length. It does not shrink the
corpus-proportional tiers; nothing in this projection does.

`status.recent_bytes` is deliberately a **different unit**: source text actually
retained in the recent tier. The budget is configured in index bytes; retained
source is reported so the amplification is visible, not so operators re-interpret
the knob as a text ceiling.

`status.recent_oldest_norm` and `status.recent_newest_norm` report the oldest and
newest `created_at_norm` values actually retained in the full-text recent tier for
the generation described by the status row. They are omitted when that tier is
empty. This is the full-text range only; events outside it remain answerable
through the session tier.

What the budget buys is a **variable window**, not a fixed one. Trigram measures
about 2.16× the source text, so 1464 MiB of family is roughly 0.66 GiB of
indexable text. Measured weekly volume on the reference corpus varies **eightfold**
(0.06 to 0.47 GiB per week): about 1.5 to 2 weeks at the median rate, under a week
during a heavy sprint, and four to five weeks during a quiet one. Compression buys
**losslessness, not reach** — the index is built over plaintext, so a compressed
body occupies exactly as much index as an uncompressed one. Everything older than
the window stays reachable through the session tier.

### Guarantee

The budget is enforced **indirectly**, through a source-text ceiling derived from a
*measured but estimated* trigram amplification. Eviction holds the recent tier to
that ceiling exactly. Whether the resulting family actually landed under the
configured budget is a separate question, and it is **measured and reported**, not
guaranteed in advance: when a generation completes, the family is re-measured and
`index_family_within_budget` records `1` (within), `0` (over) or `-1` (not
measurable).

A generation recorded over budget stays that way. Nothing corrects it in place —
the next `CatchUp` sees a complete generation and returns `already_complete`. Check
`traceary store search-projection status` for `index_family_within_budget` and
`capacity_evidence`. A `0` has several possible causes — the amplification estimate
was low for this corpus, the permanently resident objects grew (`search_projection_source_sequence`
gains a row per event and is never reclaimed), or FTS5 has not yet merged away the
pages of deleted documents. The lever is the same regardless: an explicit
`traceary store search-projection start` with a smaller `--index-family-bytes`.

The figure is `dbstat` allocation, not file size: the file shrinks at
`store compact`, and FTS5 returns space from deleted documents only as segments
merge.

**No verdict during a rebuild.** Measurement still happens — `dbstat` is walked at
`Start` and again at the source→eviction transition — but budget conformance is not
decided, because `Start` keeps the previous generation readable until the new one is
verified and a rebuild therefore holds two families at once.

Since v0.34 the new generation's own recent tier **is** bounded while it is being
built. Eviction is interleaved with the source walk: every batch compares the
generation's running source-text total against its derived ceiling and, when the total
is over or a row has aged past the cutoff, evicts its oldest documents before admitting
more. The persisted `recent_source_bytes` counter is what the comparison reads, so the
bound survives a restart. Before v0.34 eviction could not start until the last source
row was walked, and a rebuild peaked at whatever the full age window happened to weigh
— measured at 3.74 GiB against a 1.43 GiB ceiling, 36% of the way through one walk.

What is still **not** bounded by this budget is the sum across generations. The
previous generation stays fully resident — that is what keeps search answerable during
the rebuild — and is reclaimed only in the terminal cleanup phase. So plan free space
for roughly the old family plus the new ceiling, not for one budget.

The source-phase cutoff is a **build-cost bound, not an enforcement mechanism**. It
walks the corpus newest-first over stored envelope bytes, which is not the unit the
projection indexes: `thinking` blocks count toward the walk but are stripped from the
indexed text, so a reasoning-heavy corpus over-counts. The walk therefore runs against
four times the derived ceiling, so it excludes only what is clearly beyond reach and
leaves the exact decision to eviction. What it excludes it excludes irreversibly for
that generation — eviction can drop documents, never re-project them.

When the permanent tiers alone exhaust the budget and the derived ceiling is 0, the
cutoff empties the recent tier by building nothing, rather than building the whole age
window and then evicting all of it — a store already at its budget should not pay the
maximum build cost to retain nothing.

Three numbers must not be conflated:

1. **dbstat allocation** — active b-tree pages attributed to the family (the budget unit)
2. **file size after `store compact`** — shrinks only after `VACUUM`
3. **rebuild disk peak** — two generations coexist until cleanup reclaims the old one
