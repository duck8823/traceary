#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

CLIENT="${1:-}"

if [[ -z "$CLIENT" ]]; then
  echo "usage: traceary-audit.sh <client>" >&2
  exit 64
fi

traceary_read_hook_input

TRACEARY_CMD="$(traceary_resolve_bin 2>/dev/null || true)"
if [[ -z "$TRACEARY_CMD" ]]; then
  exit 0
fi

COMMAND_VALUE="$(traceary_json_get 'tool_input.command')"
if [[ -z "$COMMAND_VALUE" ]]; then
  exit 0
fi

SESSION_ID="$(traceary_json_get 'session_id')"
if [[ -z "$SESSION_ID" ]]; then
  SESSION_ID="$(traceary_read_state "$CLIENT")"
fi
if [[ -z "$SESSION_ID" ]]; then
  exit 0
fi

HOOK_CWD="$(traceary_json_get 'cwd')"
REPO_VALUE="$(traceary_resolve_repo "$HOOK_CWD")"
AUDIT_INPUT="$(traceary_json_get 'tool_input' '{}')"
AUDIT_OUTPUT="$(traceary_json_get 'tool_response')"
if [[ -z "$AUDIT_OUTPUT" ]]; then
  AUDIT_OUTPUT="$(traceary_build_failure_output || true)"
fi

AGENT_VALUE="$(traceary_resolve_agent "$CLIENT")"

COMMAND=("$TRACEARY_CMD" audit "$COMMAND_VALUE" "$AUDIT_INPUT" "$AUDIT_OUTPUT" --client hook --agent "$AGENT_VALUE" --session-id "$SESSION_ID")
if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
  COMMAND+=(--db-path "$TRACEARY_DB_PATH")
fi
if [[ -n "$REPO_VALUE" ]]; then
  COMMAND+=(--repo "$REPO_VALUE")
fi

"${COMMAND[@]}" >/dev/null 2>&1 || exit 0

exit 0
