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

case "$ACTION" in
  start)
    traceary_run_hook 1 hook session "$CLIENT" start
    ;;
  end|stop)
    traceary_run_hook 0 hook session "$CLIENT" "$ACTION"
    ;;
  *)
    echo "unsupported action: $ACTION" >&2
    exit 64
    ;;
esac
