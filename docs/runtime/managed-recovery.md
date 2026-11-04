# Explicit managed native recovery

[日本語](./managed-recovery.ja.md)

## Implementation status

The application coordinator, private recovery journal, management SDK/IPC and `runtime resume` command are implemented in the recovery branch. The normal native CLI adapter and interrupted-turn no-replay qualification remain incomplete. The default PTY launcher therefore does not advertise native resume and rejects unsupported attempts. This is not a released or host-certified recovery feature. No broker, app-server execution mode, model API or agent wrapper is adopted.

Private per-run startup metadata storage and silent internal SessionStart plumbing are implemented as adapter prerequisites, but are not attached to normal launches yet. They neither record primary session logs nor establish recovery authority. Changed, malformed or incomplete latest callbacks invalidate earlier metadata; private temporary cleanup refuses exchanged roots and unknown files.

The private Claude 2.1.318 history reader is also implemented and checked against an existing synthetic native fixture. It opens only the planned private file without following symlinks, checks stable physical identity and the complete bounded JSONL stream, and returns only namespace identity and the last recorded actual model. Native local `<synthetic>` model placeholders are not model evidence. Files above 16 MiB, lines above 4 MiB, missing evidence or mismatched identities are refused; there is no project scan or fallback. This reader alone does not qualify native recovery.

## Safety contract

An operator may resume the same verified native conversation only after Traceary has durably recorded the exact old managed AI's actual Wait exit. A saved PID, a lost daemon, an interrupted lifecycle label or a client-supplied verified flag is not exit proof. The initial implementation does not recover missing exit proof through global process scans or PID adoption. External CLI actors and all-descendant inactivity are outside the guarantee.

The original canonical worktree/store/profile, pinned native source/version and conversation/model/provider must match. The native adapter must qualify ordinary CLI resume without automatically replaying interrupted input, tools or approvals. After native Start may have committed, Traceary retains the actual child even if the caller cancels or confirmation fails. New input and observations remain blocked until native identity confirmation and durable result publication; explicit human Stop remains available against the actual owned child. No automatic kill or approval action is introduced.

## Explicit command shape

The following is a placeholder example, not a command for an existing runtime:

```sh
traceary runtime resume --runtime-dir /absolute/private/runtime \
  --epoch CURRENT_DAEMON_EPOCH --previous-epoch ORIGINAL_AI_EPOCH \
  --run ORIGINAL_RUN --generation ORIGINAL_GENERATION \
  --worktree ORIGINAL_WORKTREE_ID --profile ORIGINAL_PROFILE_ID \
  --request-id EXPLICIT_NON_SECRET_REQUEST_ID --acknowledge-work-loss
```

All flags shown are required. `--epoch` selects the current daemon; `--previous-epoch` binds the original managed AI and may equal the current epoch. No input is read from stdin and no positional payload, argv/environment, native conversation, model/provider, tool or approval override is accepted. Host authentication and approval remain host-owned.

JSON preserves the original request key and old selection, recorded phase and any known successor selection. `actual_owner_handle` indicates an actual child handle held by this Runtime, not current liveness. A stored confirmed result after daemon restart has no reconstructed handle and grants no control authority. Output explicitly reports `external_actors_coordinated=false`, `descendant_inactivity_proven=false` and `pending_input_replay=never_requested`.

## Unknown results and retention

Exactly one new durable intent can reach one native Resume call. Repeating an identical request inspects its existing operation/result; changing its original selection is rejected. Stored reserved/started/unknown attempts after restart never launch again. Post-send transport loss, mismatched replies or ambiguous creation remain unknown, not a known no-start result. Do not automatically choose a new request key or replay input to work around uncertainty.

Recovery metadata contains bounded bindings and attempt outcomes, not terminal bodies or pending commands. Export exposes revision/counts only. Explicit offline confirmed Forget abandons recovery linkage; it proves neither process death nor permission to resume an uncertain conversation. See [metadata audit and loss](./history-reconciliation.md#explicit-runtime-metadata-audit-and-loss).
