# History reconciliation and maintenance

[日本語](./history-reconciliation.ja.md)

Implementation is in progress. The maintenance-yielding SQLite read capability, domain/application history paging and real SQLite reader/source adapters are implemented; public daemon SDK/CLI transport, post-commit hints, metadata export/forget and their integrated qualification are not yet complete. This document does not claim release readiness or native-host/session recovery certification.

## Store authority and reader lifetime

Committed existing core history remains authoritative. A notification is only a hint; a datetime tail is not a durable commit cursor. Runtime metadata is separate from the core store and Registry remains the sole canonical target registry. No core schema, outbox, commit sequence or daemon spool drainer is added.

`Database.WithYieldingReadScope` is an explicit capability for daemon history reads, separate from ordinary CLI readers. It requires an existing canonical regular single-link store, an initialized supported format state and the required body-free metadata projection. Empty, pre-bootstrap and incompatible stores are rejected without creating or migrating a core store. Benchmark immutable connections and nested ordinary read scopes cannot bypass this policy.

One read has a 250 ms cancellation budget, including store admission/schema checks/query. The callback must honor cancellation and finish/close query resources. The scope closes its physical connections before returning, so it retains no shared store lease across pagination, UI waits, replies or native operations. It does not promise that an unresponsive filesystem or driver can be forcibly interrupted. Git/target validation belongs before and after the read, outside the shared SQLite lease. Caller cancellation, maintenance/busy and unsupported store errors remain distinct.

New daemon history readers yield to pending upgrade/compaction. A marker appearing during a read cancels that read; a later explicit query may retry after maintenance. No reader borrows this process's exclusive maintenance connection. Marker inspection is bounded and no-follow/nonblocking, refuses unsafe or unverifiable files, and does not delete stale markers owned by existing maintenance. Marker PID inspection is not evidence of native agent/session ownership or death. Existing CLI reader and store maintenance behavior is unchanged.

## Remaining delivery contracts

Public daemon transport and CLI must expose the implemented history core with strict correlation, bounded frames and correct adaptive prefix/continuation handling. Post-commit hints must not change capture results, stdout, spool ownership or receipt idempotency. Explicit runtime metadata export/forget must define scope, versions, bounds, consent and partial-uncertainty semantics without deleting Registry, core history, Git data or native conversations. Full upgrade/compaction, spool replay and simultaneous live-PTY qualification, final review and delivery gates remain required. These are incomplete delivery work, not capabilities advertised by the current daemon.

## Committed pages and registered read-only sources

The `History` application service resolves a registered source, reads one bounded body-free page, closes the actual SQLite lease and then revalidates the source. It never retries a busy read, initializes a store or updates Registry. `RegisteredHistorySources` reads the same RegistryRepository; it is not a second registry. A read source verifies unchanged project/worktree/common-directory physical witnesses and the current owned canonical regular single-link store. Store-only inode replacement is visible as `StoreIdentityChanged`, allowing a new history scan after maintenance; it never renews native execution, control or resume authority. Continuations bind the current store witness and fail if it changes.

A requested view is one explicit inclusive-from/exclusive-to window within the registered canonical workspace. Each request returns at most 64 events using the existing descending `(created_at_norm, event_id)` keyset and a limit+1 look-ahead. A page is committed history for one read, not a retained cross-page transaction or durable commit sequence. Invalid/oversized metadata is rejected, never silently skipped. Bodies and command contents are not hydrated. A frame-sized prefix preserves continuation from its last retained row.

After a dropped hint, reconnect or periodic refresh, consumers must restart from the first page and walk the entire requested view while `More` is true, deduplicating event IDs. Re-reading only the first page or moving a durable datetime watermark can miss deep late inserts and timestamp ties. Real SQLite tests cover an initial complete scan followed by earlier/tied commits with no hints, uncommitted-versus-committed visibility, unchanged existing CLI membership, busy/retry and actual lease release, malformed metadata refusal and fresh scans after inode replacement. The inode-replacement fixture is not a claim that full upgrade/compaction or native-host qualification has completed.
