# History reconciliation and maintenance

[日本語](./history-reconciliation.ja.md)

Implementation is in progress. The maintenance-yielding SQLite read capability is implemented; public runtime history paging, post-commit hints, metadata export/forget and their integrated qualification are not yet complete. This document does not claim release readiness or native-host/session recovery certification.

## Store authority and reader lifetime

Committed existing core history remains authoritative. A notification is only a hint; a datetime tail is not a durable commit cursor. Runtime metadata is separate from the core store and Registry remains the sole canonical target registry. No core schema, outbox, commit sequence or daemon spool drainer is added.

`Database.WithYieldingReadScope` is an explicit capability for daemon history reads, separate from ordinary CLI readers. It requires an existing canonical regular single-link store, an initialized supported format state and the required body-free metadata projection. Empty, pre-bootstrap and incompatible stores are rejected without creating or migrating a core store. Benchmark immutable connections and nested ordinary read scopes cannot bypass this policy.

One read has a 250 ms cancellation budget, including store admission/schema checks/query. The callback must honor cancellation and finish/close query resources. The scope closes its physical connections before returning, so it retains no shared store lease across pagination, UI waits, replies or native operations. It does not promise that an unresponsive filesystem or driver can be forcibly interrupted. Git/target validation belongs before and after the read, outside the shared SQLite lease. Caller cancellation, maintenance/busy and unsupported store errors remain distinct.

New daemon history readers yield to pending upgrade/compaction. A marker appearing during a read cancels that read; a later explicit query may retry after maintenance. No reader borrows this process's exclusive maintenance connection. Marker inspection is bounded and no-follow/nonblocking, refuses unsafe or unverifiable files, and does not delete stale markers owned by existing maintenance. Marker PID inspection is not evidence of native agent/session ownership or death. Existing CLI reader and store maintenance behavior is unchanged.

## Remaining delivery contracts

History paging must reuse existing body-free keyset queries, explicitly bound each page and re-walk the requested view after lost hints or late insertions. Cross-page reads cannot claim a frozen transaction or durable commit order. Store replacement during compaction must be visible without renewing native execution/control/resume authority. Post-commit hints must not change capture results, stdout, spool ownership or receipt idempotency. Explicit runtime metadata export/forget must define scope, versions, bounds, consent and partial-uncertainty semantics without deleting Registry, core history, Git data or native conversations. These are required remaining implementation and qualification work, not features provided by the current reader capability.
