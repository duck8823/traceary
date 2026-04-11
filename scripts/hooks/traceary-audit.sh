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

# For Bash tools, use tool_input.command; for MCP/other tools, use tool_name
COMMAND_VALUE="$(traceary_json_get 'tool_input.command')"
if [[ -z "$COMMAND_VALUE" ]]; then
  COMMAND_VALUE="$(traceary_json_get 'tool_name')"
fi
if [[ -z "$COMMAND_VALUE" ]]; then
  exit 0
fi

SESSION_ID="$(traceary_resolve_session_id "$CLIENT")"
if [[ -z "$SESSION_ID" ]]; then
  exit 0
fi

WORKSPACE_VALUE="$(traceary_resolve_effective_workspace "$CLIENT")"
AUDIT_INPUT="$(traceary_json_get 'tool_input' '{}')"
# For MCP tools, ensure tool_input includes at least the tool name when empty
if [[ "$AUDIT_INPUT" == "{}" || -z "$AUDIT_INPUT" ]]; then
  TOOL_NAME="$(traceary_json_get 'tool_name')"
  if [[ -n "$TOOL_NAME" ]]; then
    AUDIT_INPUT="{\"tool_name\":\"$TOOL_NAME\"}"
  fi
fi
AUDIT_OUTPUT="$(traceary_json_get 'tool_response')"
if [[ -z "$AUDIT_OUTPUT" ]]; then
  AUDIT_OUTPUT="$(traceary_build_failure_output || true)"
fi

AGENT_VALUE="$(traceary_resolve_agent "$CLIENT")"

# Extract exit code from tool_response if available
EXIT_CODE="$(traceary_json_get 'tool_response.exitCode')"

COMMAND=("$TRACEARY_CMD" audit "$COMMAND_VALUE" "$AUDIT_INPUT" "$AUDIT_OUTPUT" --client hook --agent "$AGENT_VALUE" --session-id "$SESSION_ID")
if [[ -n "$EXIT_CODE" ]]; then
  COMMAND+=(--exit-code "$EXIT_CODE")
fi
if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
  COMMAND+=(--db-path "$TRACEARY_DB_PATH")
fi
if [[ -n "$WORKSPACE_VALUE" ]]; then
  COMMAND+=(--workspace "$WORKSPACE_VALUE")
fi

"${COMMAND[@]}" >/dev/null 2>&1 || exit 0

exit 0
