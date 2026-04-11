#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

CLIENT="${1:-}"
ACTION="${2:-}"

if [[ -z "$CLIENT" || -z "$ACTION" ]]; then
  echo "usage: traceary-session.sh <client> <start|end|stop>" >&2
  exit 64
fi

traceary_read_hook_input

TRACEARY_CMD="$(traceary_resolve_bin 2>/dev/null || true)"
if [[ -z "$TRACEARY_CMD" ]]; then
  exit 0
fi

HOOK_CWD="$(traceary_json_get 'cwd')"
SESSION_ID="$(traceary_json_get 'session_id')"
REPO_VALUE="$(traceary_resolve_workspace "$HOOK_CWD")"

COMMON_ARGS=(session)
if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
  COMMON_ARGS+=(--db-path "$TRACEARY_DB_PATH")
fi
COMMON_ARGS+=("${ACTION/start/start}")

AGENT_VALUE="$(traceary_resolve_agent "$CLIENT")"

case "$ACTION" in
  start)
    COMMAND=("$TRACEARY_CMD" session start --client hook --agent "$AGENT_VALUE")
    if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
      COMMAND+=(--db-path "$TRACEARY_DB_PATH")
    fi
    if [[ -n "$REPO_VALUE" ]]; then
      COMMAND+=(--workspace "$REPO_VALUE")
    fi
    if [[ -n "$SESSION_ID" ]]; then
      COMMAND+=(--session-id "$SESSION_ID")
    fi
    PARENT_SESSION_ID="${TRACEARY_PARENT_SESSION_ID:-}"
    if [[ -n "$PARENT_SESSION_ID" ]]; then
      COMMAND+=(--parent-session-id "$PARENT_SESSION_ID")
    fi

    ACTUAL_SESSION_ID="$("${COMMAND[@]}" 2>/dev/null | tail -n 1 | tr -d '\r' || true)"
    if [[ -n "$ACTUAL_SESSION_ID" ]]; then
      traceary_write_state "$CLIENT" "$ACTUAL_SESSION_ID"
      traceary_clear_session_end_marker "$CLIENT" "$ACTUAL_SESSION_ID"
      if [[ -n "$REPO_VALUE" ]]; then
        traceary_write_repo_state "$CLIENT" "$REPO_VALUE"
      fi
    fi
    ;;
  end|stop)
    if [[ -z "$SESSION_ID" ]]; then
      SESSION_ID="$(traceary_read_state "$CLIENT")"
    fi
    if [[ -z "$SESSION_ID" ]]; then
      exit 0
    fi

    if traceary_session_end_already_recorded "$CLIENT" "$SESSION_ID"; then
      traceary_clear_state "$CLIENT"
      traceary_clear_repo_state "$CLIENT"
      exit 0
    fi

    # Use repo from session state if available (prevents CWD drift)
    REPO_STATE="$(traceary_read_repo_state "$CLIENT")"
    if [[ -n "$REPO_STATE" ]]; then
      REPO_VALUE="$REPO_STATE"
    fi

    COMMAND=("$TRACEARY_CMD" session end --client hook --agent "$AGENT_VALUE" --session-id "$SESSION_ID")
    if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
      COMMAND+=(--db-path "$TRACEARY_DB_PATH")
    fi
    if [[ -n "$REPO_VALUE" ]]; then
      COMMAND+=(--workspace "$REPO_VALUE")
    fi
    "${COMMAND[@]}" >/dev/null 2>&1 || exit 0
    traceary_clear_state "$CLIENT"
    traceary_clear_repo_state "$CLIENT"
    traceary_mark_session_ended "$CLIENT" "$SESSION_ID"
    ;;
  *)
    echo "unsupported action: $ACTION" >&2
    exit 64
    ;;
esac

exit 0
