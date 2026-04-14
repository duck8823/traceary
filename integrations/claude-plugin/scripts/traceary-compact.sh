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

case "$ACTION" in
  post-compact)
    traceary_run_hook 0 hook compact "$CLIENT" post-compact
    ;;
  session-start-compact)
    traceary_run_hook 1 hook compact "$CLIENT" session-start-compact
    ;;
  *)
    echo "unsupported action: $ACTION" >&2
    exit 64
    ;;
esac
