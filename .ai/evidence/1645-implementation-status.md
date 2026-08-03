# #1645 implementation status

## Implemented prerequisites

- Shared `PreparedStoreUpgradeRun` aggregate with compaction compatibility aliases.
- Existing compaction JSON fields and durable phase strings, including
  `copy_retry_intent`, preserved in a legacy golden journal.
- Retry cleanup authority is journaled before deletion; a legacy retry intent
  with an already absent candidate remains resumable.
- Shared prepared-upgrade journal/files ports, bounded active lookup, exact
  inode orientation, mode/link identity, and a constant-count publication
  recheck.
- Exact migration 35-43 classification/body manifest and prefix planner.
- `canonical-event-audit/v1` framing, codec normalization, accumulator, golden
  hashes, and read-only source/candidate verifier.
- Unsupported platforms return one stable fail-closed error and compile without
  a multi-rename fallback.

## Policy-blocked production changes

The local Guardian returned `policy_denied` for each of the following changes.
They were not retried or implemented indirectly:

1. Finishing `swapped -> rollback_publish_intent -> rollback_ready` under the
   lease before a compaction rollback request.
2. Rewiring normal `Database.migrate` to consume the new shared embedded
   migration catalog.
3. Adding the writable offline candidate builder (copy, migrate, resource
   observation, checkpoint, close, sync).

These denials prevent safe completion of candidate publication, payload
rehearsal handoff, atomic rollback, and process crash-matrix wiring. The
existing payload rehearsal in-place migration path is intentionally unchanged;
there is no partial routing to the new publisher and no offline fallback.

## Required authorization boundary

Direct authorization is required before the three denied production changes
can be applied. After authorization, all remaining behavior tests from the
approved design checkpoint must pass before rehearsal wiring is enabled,
including the over-one-second offline build / under-one-second publication
lease test and process-level exchange/rollback crash matrix.
