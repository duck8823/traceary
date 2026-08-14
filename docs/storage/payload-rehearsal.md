# Payload compression rehearsal (v0.34)

[日本語](payload-rehearsal.ja.md)

> Removed in v0.35.0 (#1872). `store payload-rehearsal` is an unknown command.
> Encoding happens during `traceary store compact`.

v0.34 provided a copied-store rehearsal only. It never activated compressed
canonical writes and never modifies the configured live store.

Codec compatibility preflight uses constant-size transactional counters for
the event-body, command, input, and output lanes on newly upgraded stores.
Adding codec columns therefore does not scan retained history. Development
stores that already applied the former migration keep their partial indexes
under an explicit legacy compatibility mode. Unknown, invalid, or inconsistent
compatibility evidence fails closed before a rehearsal run is created.

1. Stop writers and create a checkpointed, single-file SQLite copy. The copied
   target must have no `-wal` or `-shm` sidecars.
2. Run `traceary store payload-rehearsal preview --target COPY --live-db LIVE`.
   Preview opens the copy immutable/query-only and fails unless DB/WAL/SHM
   snapshots are identical before and after inspection.

Migration readiness is derived from embedded versions versus
`schema_migrations`. Before a run, Traceary switches an exact clone to verified
WAL mode under a separate one-second cap, executes the exact pending set in one
`BEGIN IMMEDIATE`/`COMMIT`, and records its live WAL bytes and lock duration.
The copied target must reproduce that pending state, journal mode, and WAL
reservation. Any failure after target journal mutation restores the verified
physical backup; only an explicit no-pending plan skips migration execution.
3. Run `... run --target COPY --live-db LIVE --backup ROLLBACK`. Compression is
   written only to `payload_rehearsal_rows`; canonical event and audit payloads
   remain unchanged. Required row, byte, time, and lock caps are always active.
4. If interrupted or capped, use `... resume` with the identical configuration
   and rollback artifact. Checkpoints and shadow inserts commit atomically.
5. Use `... scrub` to decode and checksum the bounded shadow range, then
   `... rollback --backup ROLLBACK` to rehearse physical restoration.

Targets that alias the live store by path, symlink, hardlink, or file identity
are rejected. Output contains aggregate metrics and opaque hashes only. There
is deliberately no activation command in v0.34; activation belongs to v0.35.
