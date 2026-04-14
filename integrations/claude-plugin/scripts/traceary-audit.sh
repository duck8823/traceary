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

traceary_run_hook 0 hook audit "$CLIENT"
