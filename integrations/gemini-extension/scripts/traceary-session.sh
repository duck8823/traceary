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
REPO_VALUE="$(traceary_resolve_repo "$HOOK_CWD")"

COMMON_ARGS=(session)
if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
  COMMON_ARGS+=(--db-path "$TRACEARY_DB_PATH")
fi
COMMON_ARGS+=("${ACTION/start/start}")

case "$ACTION" in
  start)
    COMMAND=("$TRACEARY_CMD" session start --client hook --agent "$CLIENT")
    if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
      COMMAND+=(--db-path "$TRACEARY_DB_PATH")
    fi
    if [[ -n "$REPO_VALUE" ]]; then
      COMMAND+=(--repo "$REPO_VALUE")
    fi
    if [[ -n "$SESSION_ID" ]]; then
      COMMAND+=(--session-id "$SESSION_ID")
    fi

    ACTUAL_SESSION_ID="$("${COMMAND[@]}" 2>/dev/null | tail -n 1 | tr -d '\r' || true)"
    if [[ -n "$ACTUAL_SESSION_ID" ]]; then
      traceary_write_state "$CLIENT" "$ACTUAL_SESSION_ID"
    fi
    ;;
  end|stop)
    if [[ -z "$SESSION_ID" ]]; then
      SESSION_ID="$(traceary_read_state "$CLIENT")"
    fi
    if [[ -z "$SESSION_ID" ]]; then
      exit 0
    fi

    COMMAND=("$TRACEARY_CMD" session end --client hook --agent "$CLIENT" --session-id "$SESSION_ID")
    if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
      COMMAND+=(--db-path "$TRACEARY_DB_PATH")
    fi
    if [[ -n "$REPO_VALUE" ]]; then
      COMMAND+=(--repo "$REPO_VALUE")
    fi
    "${COMMAND[@]}" >/dev/null 2>&1 || exit 0
    traceary_clear_state "$CLIENT"
    ;;
  *)
    echo "unsupported action: $ACTION" >&2
    exit 64
    ;;
esac

exit 0
