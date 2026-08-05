# Issue #1661: append-only Segment Catalog ledger

## Structure-Behavior Design Note

### Requirement summary

- Add a constant-time migration containing an append-only epoch/range ledger, immutable segment bindings, reservation/release deltas, and a rebuildable current-range cache.
- Make an expected-head compare-and-swap the only epoch publication boundary. Each epoch binds its parent, canonical transition digest, caller evidence digest, and the preceding ledger digest.
- Reconstruct a bounded source set at an old epoch from append-only boundary facts and one indexed latest covering transition with `epoch <= requested_epoch` per elementary range; never replay every epoch or copy the complete Catalog into every epoch.
- Expose only the inventory gate, target reservation/release, bounded epoch reads, current reads, and deterministic cache rebuild in this issue. Proof-specific sealed/verified/authority/eviction ports remain owned by #1651-#1653.

### Conceptual model

| Concept | Invariant |
|---|---|
| Catalog head | Singleton epoch and ledger digest; advances only by expected-head CAS. |
| Epoch | Immutable parent-linked commit binding source high-water, canonical transitions, and evidence. Ordinary head operations verify only the head and parent; a cursor-paged audit verifies the complete chain. |
| Range transition | Append-only change over one closed archive-sequence interval; overlapping transitions in one epoch are invalid. |
| Boundary set | Append-only distinct placement change points whose sorted digest/count is bound into every epoch and ledger digest; `source_high_water + 1` is an epoch-derived endpoint and is never persisted as a redundant boundary. Loss or drift fails closed. |
| Range caps | Internal authenticated change-point scanning has its own hard cap; caller `Ranges` limits only the coalesced returned/current ranges, so redundant historical change points cannot publish an unreadable head. |
| Reservation delta | `reserve` or `release`; release returns placement to `hot` and never creates an `abandoned` placement. |
| Segment binding | Immutable store/lineage, interval, format, manifest, summary, logical/file digest, basename, and storage class facts. |
| Current ranges | Derived cache only; its digest must equal deterministic replay of the ledger. |

### Responsibilities and boundaries

Domain values validate epochs, ranges, placement transitions, digests, and bindings without SQL. Application types define bounded commands/results and typed errors. SQLite owns expected-head transactions, append ordering, indexed replay, cache replacement, and migration identity. Callers own proof evidence; later issues receive separate proof-specific ports and cannot use a generic state setter.

### Behavior tests and TDD plan

1. Prove migration 45 is data-independent and its exact name/digest/class is cataloged.
2. Prove reservation requires an activated, gap-free archive inventory and rejects stale heads, overlap, gaps, lineage drift, invalid digests, reservation-ID reuse, and every transition except `hot -> reserved -> hot`.
3. Prove release appends a delta and restores `hot` without deleting history or inventing `abandoned`.
4. Prove indexed old-epoch reconstruction, cursor-paged full audit, deterministic/idempotent current rebuild, ledger/cache digest agreement, and constant head-path work beyond 10,000 epochs.
5. Prove segment binding mismatch/immutability, boundary-cache deletion detection, canonical reservation-ID whitespace behavior, missing audit-tail detection, and that metadata discovery alone cannot create authority.

### Rollback and risks

Migration 45 is additive and constant-time. An older binary ignores the tables. Ledger rows and bindings are protected from update/delete by triggers. Cache loss is repairable from the ledger; cache drift fails closed until explicit rebuild. Ledger loss, parent/digest drift, unknown transitions, and binding contradictions are not repaired from manifests because that would invent authority. Schema downgrade requires restoring a pre-migration backup.

### Design checkpoint

Approved shape: one append-only hash-chained epoch ledger, interval deltas, immutable bindings, and a non-authoritative current cache. This issue permits only `hot -> reserved -> hot`; later proof-bearing operations add their dedicated transitions.
