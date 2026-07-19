#!/bin/bash
# Install the Traceary Kimi plugin into the local Kimi Code plugin directory.
#
# Kimi Code loads managed plugins from $KIMI_CODE_HOME/plugins/managed/<id>/
# (default ~/.kimi-code) and tracks them in $KIMI_CODE_HOME/plugins/
# installed.json. This script mirrors the official local-install behavior:
# the packaged directory is copied and the install record is upserted, so
# edits here only take effect after a reinstall.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLUGIN_DIR="${ROOT_DIR}/integrations/kimi-plugin"
KIMI_HOME="${KIMI_CODE_HOME:-${HOME}/.kimi-code}"
MANAGED_DIR="${KIMI_HOME}/plugins/managed/traceary"
INSTALLED_JSON="${KIMI_HOME}/plugins/installed.json"

command -v kimi >/dev/null 2>&1 || {
  echo 'error: kimi CLI is not installed' >&2
  exit 69
}
command -v traceary >/dev/null 2>&1 || {
  echo 'error: traceary is not on PATH (plugin hooks and the MCP server invoke it)' >&2
  exit 69
}
command -v python3 >/dev/null 2>&1 || {
  echo 'error: python3 is required to update the plugin install record' >&2
  exit 69
}

mkdir -p "${KIMI_HOME}/plugins/managed"
rm -rf "${MANAGED_DIR}"
cp -R "${PLUGIN_DIR}" "${MANAGED_DIR}"

python3 - "${INSTALLED_JSON}" "${MANAGED_DIR}" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

installed_path = pathlib.Path(sys.argv[1])
managed_dir = sys.argv[2]

if installed_path.exists():
    data = json.loads(installed_path.read_text())
else:
    data = {"plugins": []}

plugins = [entry for entry in data.get("plugins", []) if entry.get("id") != "traceary"]
plugins.append({
    "id": "traceary",
    "root": managed_dir,
    "source": "local-path",
    "enabled": True,
    "state": "ok",
    "installedAt": datetime.now(timezone.utc).isoformat(),
})
data["plugins"] = plugins

tmp_path = installed_path.with_suffix(".json.tmp")
tmp_path.write_text(json.dumps(data, indent=2) + "\n")
tmp_path.replace(installed_path)
PY

echo "installed Traceary Kimi plugin to ${MANAGED_DIR}"
echo "next: run '/plugins reload' (or start a new session) to activate hooks, skills, and the traceary MCP server"
