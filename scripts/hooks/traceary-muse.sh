#!/bin/sh
set -eu

# Wired set is the intersection of Muse HookEventKind names that have a
# Traceary lifecycle row in #2350 findings §4:
#   SessionStart          -> session_started
#   UserPromptSubmit      -> prompt
#   PreToolUse            -> validation-only (same grok/kimi boundary; no extra lifecycle row)
#   PostToolUse and
#   PostToolUseFailure    -> command_executed
#   Stop                  -> transcript
#   PreCompact/PostCompact -> compact_summary
#   SessionEnd            -> session_ended: HookEventKind exists and durable
#                            session.end is a log event, not hook proof. Live
#                            muse exec (echo, 2026-09-06) dispatched SessionStart,
#                            UserPromptSubmit, and Stop only; SessionEnd was
#                            absent, so it stays unwired with no shim.
# consolidation_request and the eight extra host hooks (PermissionRequest,
# PreLLMCall, PostLLMCall, SubagentStart, SubagentStop, Notification,
# PostToolBatch, StopFailure) stay unwired with no shims.
action="${1:?missing Muse hook action}"
case "${action}" in
  session-start|user-prompt-submit|pre-tool-use|post-tool-use|post-tool-use-failure|stop|pre-compact|post-compact) ;;
  *)
    printf 'unsupported Muse hook action: %s\n' "${action}" >&2
    exit 64
    ;;
esac

exec traceary hook muse "${action}"
