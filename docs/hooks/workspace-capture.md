# Capture-time workspace routing

[日本語](./workspace-capture.ja.md)

Hook delivery records retain the resolved workspace before their first handler runs.
Retrying a delivery after `TRACEARY_WORKSPACE`, repository configuration or hook workspace state changes does not move that delivery to another workspace.
This is a CLI capture/replay guarantee, not a daemon or a current-agent-state API.

## Routing contract

The private spool envelope carries optional `resolved_workspace` acquisition metadata.
It is separate from the host payload: a host field with the same name cannot supply a trusted pin.
The same pin reaches the first attempt and replay through the existing local reader/context boundary, including native adapter normalization and prompt/transcript consolidation paths.
Native delivery identity, raw cwd evidence and local receipt deduplication remain separate.

- A nonempty captured value takes precedence over later environment, Git detection and hook state.
- A captured empty string remains unknown; it is not filled from a later environment.
- Older records without the optional field retain their existing resolution behavior. Their original workspace cannot be recovered by inference.
- Session-start fallback rules and Antigravity's pending tool cwd precedence remain unchanged.
- Usage-only hooks remain body/path-free. Usage is attributed through the stored session identity and canonical workspace, not a new workspace argument.
- Memory extraction jobs retain the workspace resolved by their parent hook at enqueue time.

If capture-time resolution fails, the spool retains `workspace_capture_failed` without recording the underlying error or payload in that marker.
The current handler can still produce its existing pre-resolution diagnostics, but workspace resolution fails with a fixed error.
Replay uses the existing bounded retry/dead-letter mechanism instead of reassigning the delivery under a later environment.
A transient capture failure therefore does not automatically recover merely because a different workspace later becomes available.
Inspect the existing hook-spool diagnostics; this change does not add an automatic rebind or discard operation.

## Compatibility and limits

There is no SQLite migration, Session lifecycle change, new runtime entity, agent wrapper, PATH shim or public command.
The change does not expand existing session-ID/database routing guarantees or reconstruct missing native callbacks.
Legacy records are not backfilled and stored history is not rewritten.

The spool format change is additive, but older binaries ignore the new fields and cannot enforce their routing guarantee.
Assess pending/dead-letter records before downgrade; do not replay new pinned records with an older binary while assuming the pin is respected.
Retain unresolved records for explicit diagnosis rather than deleting or silently rebinding them.

## Verification

Regression fixtures cover environment and repository changes, captured unknown versus legacy absent values, host payload lookalikes, native normalization, first-attempt/replay consistency, failed acquisition retention and isolation from another delivery's context.
Existing receipt, cancellation, usage and hook suites remain applicable.
All fixtures use isolated hook-state directories; native host execution is a separate integration checkpoint.
