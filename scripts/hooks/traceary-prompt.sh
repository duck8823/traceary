#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

CLIENT="${1:-}"

if [[ -z "$CLIENT" ]]; then
  echo "usage: traceary-prompt.sh <client>" >&2
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

PROMPT_TEXT="$(traceary_json_get 'prompt')"
if [[ -z "$PROMPT_TEXT" ]]; then
  exit 0
fi

if [[ -n "$SESSION_ID" ]]; then
  WORKSPACE="$(traceary_resolve_effective_workspace "$CLIENT")"
  AGENT="$(traceary_resolve_agent "$CLIENT")"
  COMMAND=("$TRACEARY_CMD" log "$PROMPT_TEXT" --kind prompt --client hook --agent "$AGENT" --session-id "$SESSION_ID")
  if [[ -n "$WORKSPACE" ]]; then
    COMMAND+=(--workspace "$WORKSPACE")
  fi
  if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
    COMMAND+=(--db-path "$TRACEARY_DB_PATH")
  fi
  "${COMMAND[@]}" >/dev/null 2>&1 || true
fi

exit 0
