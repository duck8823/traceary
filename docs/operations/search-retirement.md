# Retiring the legacy search index

[日本語](search-retirement.ja.md)

Traceary carried two search index families: the full-corpus one introduced in
migration 032 (`event_search_documents`, `event_search_fts`,
`event_search_backfill_state`) and the bounded, tiered projection that replaced
it. Nothing reads the first any more. On a long-lived store it is typically the
single largest object in the file — on the maintainer's 24.7 GiB store it held
16.15 GiB, about 65% of the database.

v0.34 stopped maintaining it. v0.35 drops it during `traceary store compact`.

## What the upgrade does on its own

Migration 052 runs at first startup and drops only constant-cost objects: all
eight writer triggers, the `event_search_projection` view, and the
`search_maintenance_control` table. It never touches the large tables.

The three triggers on `event_search_documents` go too, not just the five on
`events` and `command_audits`. `event_search_documents.event_id` is declared
`ON DELETE CASCADE`, so every `traceary store gc` and retention pass still
reached the index through that foreign key. Leaving those triggers in place
would have
made each deleted event append an FTS5 delete marker — growing the very index
this retirement removes.

That split is deliberate. Traceary applies every pending migration
unconditionally when it opens the store, so a multi-GiB `DROP TABLE` inside a
migration would block your first `traceary` invocation after upgrading, with no
way to decline. Startup stays fast; reclaiming the space is your decision.

After the upgrade the family is inert: it holds bytes, and nothing writes to or
reads from it.

## Reclaiming the space

```sh
traceary store compact
```

`store compact` copies the store, `DROP`s the three tables on the work copy,
then `VACUUM INTO` a candidate. The family is never copied into the new file.
`store search-retire` and `store compact plan` / `apply` are gone.

The drop uses a straight `DROP`, not a row-by-row `DELETE`. Emptying an FTS5
content table appends delete markers into new index segments rather than
reclaiming anything, so deleting first would make the file *larger* before it
gets smaller — measured at +14% file size and +47% index size on the
maintainer's store, and roughly eight times slower than the drop.

`DROP` alone returns pages to SQLite's free list. It does not shrink the file:
`auto_vacuum` is `NONE`. Compact's `VACUUM INTO` is what returns the space to
the filesystem. See [`safe-compaction.md`](../storage/safe-compaction.md).

`traceary doctor` reports the family as a warning while it is resident, naming
the bytes it holds and `traceary store compact`.

## Rollback

Roll-forward only. There is no `store search-restore`, and reverting the
Traceary version does not bring the index back: the writer triggers are gone,
so a downgraded binary would query an index that stopped receiving writes at
the moment you upgraded, and silently return incomplete results.

If you need the removed data, restore the store from a backup taken before the
retirement. Take one first if that matters to you.

## What search does afterwards

Literal search is authoritative through the newest-first decode walk over the
canonical events and command audits. The projection supplies the fingerprint
pre-filter and the session tier; retiring migration-032 changed no result
because that family was already unread. Results are unchanged.

An incomplete, rebuilding, or drifted projection does not make search
unavailable: the fingerprint index is only a pre-filter, so searches fall back
to decoding each candidate and still return correct results. The cost is work,
not correctness — a search in that state is decode-bound, and if it exhausts
the deep literal search budget it says so rather than truncating the answer.

Two narrowings are worth knowing about, both inherited from the bounded
projection rather than introduced here: deep offsets into a large result set and
searches that exhaust the budget report an explicit limit instead of a partial
page.
