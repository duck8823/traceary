# Explicit managed native recovery

[日本語](./managed-recovery.ja.md)

## Implementation status

The application coordinator, private recovery journal, management SDK/IPC, `runtime resume` command and ordinary Claude 2.1.318 candidate adapter are implemented in the recovery branch. Actual Daemon/CLI tests passed for completed-context recovery and genuinely interrupted managed input, without replay before new explicit input. Final host qualification, independent implementation review and final-head CI/wave gates remain incomplete. This is not a released or fully host-certified feature. No broker, app-server execution mode, model API or agent wrapper is adopted.

Private per-run startup metadata storage and silent internal SessionStart plumbing are implemented as adapter prerequisites, and are attached only to exact supported source candidates. They neither record primary session logs nor establish recovery authority. Changed, malformed or incomplete latest callbacks invalidate earlier metadata; private temporary cleanup refuses exchanged roots and unknown files.

The private Claude 2.1.318 history reader is also implemented and checked against an existing synthetic native fixture. It opens only the planned private file without following symlinks, checks stable physical identity and the complete bounded JSONL stream, and returns only namespace identity and the last recorded actual model. Native local `<synthetic>` model placeholders are not model evidence. Files above 16 MiB, lines above 4 MiB, missing evidence or mismatched identities are refused; there is no project scan or fallback. This reader alone does not qualify native recovery.

Static source verification preserves the registered executable and checks the pinned native artifact; the known non-print entry also requires its two pinned local routing imports and unchanged native resolution. Alternate auth/provider/injection overrides make recovery unsupported rather than triggering a route change. The separate bounded native `auth status` check requires the existing logged-in first-party subscription class; only the native CLI accesses host auth, and account fields and stderr are discarded. Source/status checks passed on the existing host and are connected to the owned-child recovery path. Normal launch planning never waits on provider status or native history.

The owned-child metadata lifetime component consumes the latest marker and cleans its slot under one lock after actual Wait, rather than reusing an earlier live binding. Its source/history/provider checks produce metadata only; they cannot replace the application's durable exit receipt. Qualified normal launches add a fresh planned `--session-id` and one metadata-only `--settings` handler; resume uses only the recorded `--resume` conversation and `--model`. Qualified paths disable native interrupted-turn continuation and never write input. Unqualified launches retain their existing arguments.

A daemon advertises resume only when a registered source passes static qualification at startup; each operation still revalidates its exact target/source/history/model/provider. Capabilities are fixed for that daemon epoch. A profile registered later needs a new explicitly started daemon epoch before this optional capability becomes available; no automatic restart or PID adoption occurs.

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

An unknown historical request is never rearmed or silently changed to success. If its exact successor later has both a durable owned execution exit and a matching qualified native exit receipt, a **new explicit request selecting that exited successor** may recover the conversation. For an unconfirmed predecessor attempt, every expected binding field including model/provider must match. A saved PID, an execution label alone, a missing native receipt or a foreign binding never releases the reservation. The original request and origin remain inspection-only; loading metadata never starts a process.

After a resumed run has been confirmed and published, the operator may change its model interactively. At actual owned exit, the qualified last actual model may seal the still-unexited recovery snapshot for the next resume; conversation/history/namespace/target/profile/provider remain immutable. An unknown/unpublished resume still requires the originally requested model, and a sealed exit receipt cannot change again. No model is chosen or substituted by Traceary.

Recovery metadata contains bounded bindings and attempt outcomes, not terminal bodies or pending commands. Export exposes revision/counts only. Explicit offline confirmed Forget abandons recovery linkage; it proves neither process death nor permission to resume an uncertain conversation. See [metadata audit and loss](./history-reconciliation.md#explicit-runtime-metadata-audit-and-loss).
