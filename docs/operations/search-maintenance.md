# Search maintenance

[日本語](search-maintenance.ja.md)

Traceary v0.34 records normal-search authority independently of projection
table presence. Existing and upgraded stores start in `legacy/active`.
Migration and normal startup never delete, drop, or vacuum legacy search data.

Retirement is an operator-only workflow:

1. Run `traceary store search-maintenance adopt-target` on the actual target.
   This rotates the copied cursor key and marks the projection stale. Then
   complete the bounded search projection and same-head parity-v2 run. A
   copied-store artifact is compatibility evidence, not authorization.
   Combine the passed `fingerprint_eligible` and `bounded_verification`
   artifacts with `store-benchmark --authorize-search-parity MANIFEST`; the
   private 0600 manifest names the actual DB and both private artifacts.
2. Run `traceary store search-maintenance start-retire --evidence ARTIFACT
   --expected-revision COMMIT`. Traceary re-reads projection state, source
   high-water, aggregates, and the store-owned key in one fresh snapshot and
   compares the keyed target binding in constant time.
3. Repeatedly run `resume-retire --rows 128`. Each transaction removes at most
   the requested number of legacy plaintext documents and records progress and
   before/after logical and physical bytes. Interruption is safe to resume.
4. Check `status`. Normal CLI and MCP search use the persisted tiered authority
   and fail closed if its projections become incomplete or stale.

Rollback uses canonical history, including codec decoding:

1. Run `start-restore`.
2. Repeatedly run `resume-restore --rows 128` until status returns
   `legacy/active`.

Authority switches back only after the final canonical batch and legacy writer
triggers are restored in the same transaction. A decode/write failure leaves
the store in `tiered/restoring`, so a partially rebuilt legacy projection can
never become authoritative.

Physical file size may not fall immediately because SQLite retains free pages.
The status report records both logical projection bytes and database physical
bytes; this workflow deliberately does not run `VACUUM` implicitly.

Before production cutover, compare the parity artifact's legacy/tiered latency
fields on synthetic and copied-store datasets. A material recent-search
regression is a rollback trigger even when membership parity passes.
