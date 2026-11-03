# History reconciliation and maintenance

[日本語](./history-reconciliation.ja.md)

Implementation is in progress. The maintenance-yielding SQLite read capability, domain/application history paging and real SQLite reader/source adapters are implemented; public daemon SDK/CLI transport and coarse hint transport are implemented. Automatic post-commit hint production/routing, metadata export/forget and integrated qualification are not yet complete. This document does not claim release readiness or native-host/session recovery certification.

## Store authority and reader lifetime

Committed existing core history remains authoritative. A notification is only a hint; a datetime tail is not a durable commit cursor. Runtime metadata is separate from the core store and Registry remains the sole canonical target registry. No core schema, outbox, commit sequence or daemon spool drainer is added.

`Database.WithYieldingReadScope` is an explicit capability for daemon history reads, separate from ordinary CLI readers. It requires an existing canonical regular single-link store, an initialized supported format state and the required body-free metadata projection. Empty, pre-bootstrap and incompatible stores are rejected without creating or migrating a core store. Benchmark immutable connections and nested ordinary read scopes cannot bypass this policy.

One read has a 250 ms cancellation budget, including store admission/schema checks/query. The callback must honor cancellation and finish/close query resources. The scope closes its physical connections before returning, so it retains no shared store lease across pagination, UI waits, replies or native operations. It does not promise that an unresponsive filesystem or driver can be forcibly interrupted. Git/target validation belongs before and after the read, outside the shared SQLite lease. Caller cancellation, maintenance/busy and unsupported store errors remain distinct.

New daemon history readers yield to pending upgrade/compaction. A marker appearing during a read cancels that read; a later explicit query may retry after maintenance. No reader borrows this process's exclusive maintenance connection. Marker inspection is bounded and no-follow/nonblocking, refuses unsafe or unverifiable files, and does not delete stale markers owned by existing maintenance. Marker PID inspection is not evidence of native agent/session ownership or death. Existing CLI reader and store maintenance behavior is unchanged.

## Remaining delivery contracts

Public daemon transport and CLI now expose the history core with strict correlation, bounded frames and adaptive prefix/continuation handling. Post-commit hints must not change capture results, stdout, spool ownership or receipt idempotency. Explicit runtime metadata export/forget must define scope, versions, bounds, consent and partial-uncertainty semantics without deleting Registry, core history, Git data or native conversations. Full upgrade/compaction, spool replay and simultaneous live-PTY qualification, final review and delivery gates remain required. These are incomplete delivery work, not capabilities advertised by the current daemon.

## Committed pages and registered read-only sources

The `History` application service resolves a registered source, reads one bounded body-free page, closes the actual SQLite lease and then revalidates the source. It never retries a busy read, initializes a store or updates Registry. `RegisteredHistorySources` reads the same RegistryRepository; it is not a second registry. A read source verifies unchanged project/worktree/common-directory physical witnesses and the current owned canonical regular single-link store. Store-only inode replacement is visible as `StoreIdentityChanged`, allowing a new history scan after maintenance; it never renews native execution, control or resume authority. Continuations bind the current store witness and fail if it changes.

A requested view is one explicit inclusive-from/exclusive-to window within the registered canonical workspace. Each request returns at most 64 events using the existing descending `(created_at_norm, event_id)` keyset and a limit+1 look-ahead. A page is committed history for one read, not a retained cross-page transaction or durable commit sequence. Invalid/oversized metadata is rejected, never silently skipped. Bodies and command contents are not hydrated. A frame-sized prefix preserves continuation from its last retained row.

After a dropped hint, reconnect or periodic refresh, consumers must restart from the first page and walk the entire requested view while `More` is true, deduplicating event IDs. Re-reading only the first page or moving a durable datetime watermark can miss deep late inserts and timestamp ties. Real SQLite tests cover an initial complete scan followed by earlier/tied commits with no hints, uncommitted-versus-committed visibility, unchanged existing CLI membership, busy/retry and actual lease release, malformed metadata refusal and fresh scans after inode replacement. The inode-replacement fixture is not a claim that full upgrade/compaction or native-host qualification has completed.

## Public daemon SDK and CLI

`daemon serve` composes history metadata services without opening a core store at startup. Custom daemons without HistoryFactory explicitly refuse history. The separate HistoryClient reads an existing owner-private Unix endpoint; it never starts a daemon or selects arbitrary SQL, executable, profile or store path.

```sh
traceary runtime history page --runtime-dir /absolute/private/runtime --epoch ORIGINAL_EPOCH --worktree REGISTERED_WORKTREE_ID --from 2026-11-04T00:00:00Z --to 2026-11-05T00:00:00Z --limit 64
```

Times must be canonical UTC RFC3339Nano and limit is 1..64. A continuation supplies all three of `--before-at`, `--before-id` and `--source-witness` from the previous last-retained row/source. All pages must keep the same epoch/worktree/window and source witness. After a hint, gap, reconnect or periodic refresh, restart and rewalk the requested view; the cursor is not durable commit order.

JSON exposes body-free events, more/next, physical source witness, store_identity_changed, advisory hint_revision/hint_overflow, authority=`committed_page_not_commit_cursor`, consistency=`single_page_only` and native_target_revalidated=false. The response validates original request correlation and source identity. Actual escaped frame bytes are bounded to 64 KiB: trailing rows are deferred with a new exact next anchor, not dropped. An oversized single row/header is an explicit limit error. Missing/malformed/mixed/null/cross-operation replies never become valid history or old launch/control success.

The distinct empty `history_hint` arm uses observations.sock and only increments a saturating in-memory generation. It needs no native ObservationReceiver, fabricated native IDs or queue admission; it does no SQL or native work. Its ACK proves neither commit nor display. The SDK exposes HintHistory for optional producers, but automatic EventUsecase post-commit notification and managed-root routing are still pending. No capture, receipt, spool ownership or native authority is changed by providing this transport.
