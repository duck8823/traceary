#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

CLIENT="${1:-}"
ACTION="${2:-}"

if [[ -z "$CLIENT" || -z "$ACTION" ]]; then
  echo "usage: traceary-compact.sh <client> <post-compact|session-start-compact>" >&2
  exit 64
fi

traceary_read_hook_input

TRACEARY_CMD="$(traceary_resolve_bin 2>/dev/null || true)"
if [[ -z "$TRACEARY_CMD" ]]; then
  exit 0
fi

SESSION_ID="$(traceary_json_get 'session_id')"
if [[ -z "$SESSION_ID" ]]; then
  SESSION_ID="$(traceary_read_state "$CLIENT")"
fi

case "$ACTION" in
  post-compact)
    # Record that a compact happened
    if [[ -n "$SESSION_ID" ]]; then
      COMMAND=("$TRACEARY_CMD" log "compact triggered" --client hook --agent "$CLIENT" --session-id "$SESSION_ID")
      if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
        COMMAND+=(--db-path "$TRACEARY_DB_PATH")
      fi
      "${COMMAND[@]}" >/dev/null 2>&1 || true
    fi
    ;;
  session-start-compact)
    # Output context pointer to stdout for injection into new context
    COMMAND=("$TRACEARY_CMD" compact-summary)
    if [[ -n "$SESSION_ID" ]]; then
      COMMAND+=(--session-id "$SESSION_ID")
    fi
    if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
      COMMAND+=(--db-path "$TRACEARY_DB_PATH")
    fi
    "${COMMAND[@]}" 2>/dev/null || true
    ;;
  *)
    echo "unsupported action: $ACTION" >&2
    exit 64
    ;;
esac

exit 0
