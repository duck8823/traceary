#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLUGIN_DIR="${ROOT_DIR}/integrations/grok-plugin"
PLUGIN_NAME="traceary-grok"

command -v grok >/dev/null 2>&1 || {
  echo 'error: grok CLI is not installed' >&2
  exit 69
}

grok plugin validate "${PLUGIN_DIR}"
if grok plugin list --json | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary-grok"'; then
  grok plugin uninstall "${PLUGIN_NAME}"
fi
grok plugin install --trust "${PLUGIN_DIR}"
grok plugin details "${PLUGIN_NAME}"
echo 'installed traceary-grok: 7 native hook boundaries, 1 MCP server, and 3 skills'
echo 'note: the installer intentionally does not uninstall a legacy plugin named traceary because it may belong to another host'
