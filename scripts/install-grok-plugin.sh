#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLUGIN_DIR="${ROOT_DIR}/integrations/grok-plugin"

command -v grok >/dev/null 2>&1 || {
  echo 'error: grok CLI is not installed' >&2
  exit 69
}

grok plugin validate "${PLUGIN_DIR}"
if grok plugin list --json | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary"'; then
  grok plugin uninstall traceary
fi
grok plugin install --trust "${PLUGIN_DIR}"
grok plugin details traceary
