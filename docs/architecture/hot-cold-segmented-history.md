# Hot/cold segmented history architecture

[日本語](hot-cold-segmented-history.ja.md)

Status: simplified v0.34.0 decision. Implementation is split across #1649–#1654 and cleanup #1669.

## Decision

Traceary keeps recent data in the writable Hot SQLite database and copies older history into immutable, compressed SQLite segments. A lightweight Catalog in Hot identifies candidate segments. Historical readers search Hot plus selected segments and deduplicate records by stable event ID.

Archive processing is deliberately at-least-once. Concurrent reads may temporarily be partial and a record may physically exist in both Hot and a segment. Traceary does not provide a database-wide snapshot, exactly-once publication, generation-wide resume, or post-eviction rollback in v0.34.0.

Active/latest/handoff remain Hot-only and do not open archive segments.

## Minimal invariants

- A segment is immutable after it is published.
- A segment file is completely written, synced, and installed before the Catalog references it.
- Hot records are not deleted before the referenced segment is durable.
- Logical results deduplicate overlapping Hot and segment records by event ID.
- Missing or corrupt required segments produce an explicit partial result, never a false complete result.
- Canonical events and command audits are retained during normal operation; compression changes their representation.

## Components

| Component | Responsibility |
|---|---|
| Hot SQLite | Recent events/audits, sessions, active/latest/handoff, and the Catalog. |
| Segment writer | Copies one bounded closed range into a compressed immutable segment and verifies it can be read. |
| Catalog | Stores one row per published segment: content-addressed basename, sequence and time bounds, counts, and digests. |
| Search router (future, #1652) | Searches Hot and Catalog-selected segments, merges stable ordering, deduplicates IDs, and reports partial coverage. |
| Evictor (future, #1653) | Deletes only Hot identities covered by a durable Catalog entry. Physical compaction is ordinary maintenance. |

The v0.34.0 path does not use an offline Hot candidate, database-wide atomic exchange, a durable migration-run journal, a rollback state machine, or full-history projection generation.

## Archive flow

1. Select a bounded old range using the current Hot contents. New concurrent writes remain Hot.
2. Build a segment in a private temporary file.
3. Verify its manifest, counts, digest, and decoding.
4. Sync the file, install it with an atomic rename, and sync the directory.
5. Add or confirm the idempotent Catalog entry. Registration itself re-verifies the installed sealed file against the manifest and re-syncs the directory, so a Catalog row cannot precede durable installation.
6. In a later pass, delete matching Hot IDs. A crash may leave duplicates, which readers deduplicate.

If a crash happens before step 5, the temporary or orphan file is disposable. If it happens after step 5 but before step 6, Hot and the segment overlap safely. Work restarts at segment granularity; no durable per-step workflow is required.

## Search behavior

Recent reads query Hot only. Historical literal search uses Catalog sequence and time bounds to choose segments, opens only those candidates, applies exact filtering inside each source, and merges results using the existing event order. A segment whose time bounds are incomplete fails open and is selected.

Unavailable or corrupt candidates are counted in aggregate diagnostics and make the result partial. Concurrent archival may change coverage between requests; v0.34.0 does not promise a cross-request global snapshot.

## Observable behavior tests

- A published segment can be read and its manifest/digest validates.
- Catalog publication never precedes durable segment installation.
- Eviction never selects IDs without durable segment coverage.
- Hot and segment overlap produces one logical result per event ID.
- A missing or corrupt selected segment produces a partial result.
- active/latest/handoff do not open segments.
- Existing primary CLI and MCP search behavior remains compatible.

Tests for database-wide atomic exchange, durable migration phase transitions, exact crash-boundary resume, post-eviction reconstruction, and generation-wide projection are outside the adopted design and are removed with those implementations.

## Delivery split

- #1649 supplies immutable compressed segment format and validation.
- #1669 removes the abandoned strict migration protocol and reduces the #1650 Catalog to a minimal registration table. Store initialization fails closed on a migration ledger written by the abandoned unreleased catalog instead of silently skipping the replacement Catalog migration.
- #1652 supplies minimal Hot/segment search and ID deduplication.
- #1653 supplies covered-ID eviction and ordinary space reclaim.
- #1654 supplies minimal operations and documentation.
- #1620 and #1621 verify representative behavior and prepare the release.

The design is intentionally not generalized into a migration framework. Stronger snapshot, recovery, or rollback guarantees require a later issue and are not v0.34.0 release conditions.
