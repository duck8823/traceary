# Retiring the legacy search index

[日本語](search-retirement.ja.md)

Traceary carried two search index families: the full-corpus one introduced in
migration 032 (`event_search_documents`, `event_search_fts`,
`event_search_backfill_state`) and the bounded, tiered projection that replaced
it. Nothing reads the first any more. On a long-lived store it is typically the
single largest object in the file — on the maintainer's 24.7 GiB store it held
16.15 GiB, about 65% of the database.

v0.34 stops maintaining it and gives you one command to remove it.

## What the upgrade does on its own

Migration 052 runs at first startup and drops only constant-cost objects: the
five writer triggers, the `event_search_projection` view, and the
`search_maintenance_control` table. It never touches the large tables.

That split is deliberate. Traceary applies every pending migration
unconditionally when it opens the store, so a multi-GiB `DROP TABLE` inside a
migration would block your first `traceary` invocation after upgrading, with no
way to decline. Startup stays fast; reclaiming the space is your decision.

After the upgrade the family is inert: it holds bytes, and nothing writes to or
reads from it.

## Reclaiming the space

```sh
traceary store search-retire
traceary store compact plan
traceary store compact apply RUN_ID
```

Both steps matter, and the order is fixed.

`store search-retire` drops the three tables in one transaction. It is
idempotent — on a store that no longer carries the family it reports
`already_removed` and exits 0. On a multi-GiB store it can take a couple of
minutes; interrupting it rolls the transaction back cleanly, changing nothing.

The command uses a straight `DROP`, not a row-by-row `DELETE`. Emptying an FTS5
content table appends delete markers into new index segments rather than
reclaiming anything, so deleting first makes the file *larger* before it gets
smaller — measured at +14% file size and +47% index size on the maintainer's
store, and roughly eight times slower than the drop.

`DROP` returns pages to SQLite's free list. It does not shrink the file:
`auto_vacuum` is `NONE`, so the freed pages are reused by future writes but the
file keeps its size on disk. `store compact` (which uses `VACUUM INTO` behind a
preview-then-apply workflow) is what actually returns the space to the
filesystem. See [`safe-compaction.md`](../storage/safe-compaction.md) for that
sequence.

Running them in the other order is refused. `store compact plan` errors out
while the family is still present, because compacting first would copy 16 GiB
of dead index into the new file and bake it in. The check runs before the
source digest, so it fails in seconds rather than after hashing the whole
store. `store compact apply` re-checks behind its exclusive lease.

`traceary doctor` reports the family as a warning while it is resident, naming
the bytes it holds and the command to remove it.

## Rollback

Roll-forward only. There is no `store search-restore`, and reverting the
Traceary version does not bring the index back: the writer triggers are gone,
so a downgraded binary would query an index that stopped receiving writes at
the moment you upgraded, and silently return incomplete results.

If you need the removed data, restore the store from a backup taken before the
retirement. Take one first if that matters to you.

## What search does afterwards

Search is served entirely by the bounded tiered projection, which is what
already answered every query before this change. Results are unchanged.

An incomplete, rebuilding, or drifted projection does not make search
unavailable: the fingerprint index is only a pre-filter, so searches fall back
to decoding each candidate and still return correct results. The cost is work,
not correctness — a search in that state is decode-bound, and if it exhausts
the deep literal search budget it says so rather than truncating the answer.

Two narrowings are worth knowing about, both inherited from the bounded
projection rather than introduced here: deep offsets into a large result set and
searches that exhaust the budget report an explicit limit instead of a partial
page.
