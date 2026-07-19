#!/bin/bash
# Install the Traceary Kimi plugin into the local Kimi Code plugin directory.
#
# Kimi Code loads managed plugins from $KIMI_CODE_HOME/plugins/managed/<id>/
# (default ~/.kimi-code) and tracks them in $KIMI_CODE_HOME/plugins/
# installed.json. This script mirrors the official local-install behavior
# (schema verified against Kimi Code 0.27.0): the package is staged and
# swapped into the managed copy, and the install record is upserted while
# preserving user-controlled fields (enabled/state and unknown keys) from a
# previous install. Re-running is idempotent.
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

# Validate or initialize the install record BEFORE touching the managed
# copy, so a corrupt file cannot leave a half-updated install behind.
python3 - "${INSTALLED_JSON}" <<'PY'
import json
import pathlib
import shutil
import sys

installed_path = pathlib.Path(sys.argv[1])
if installed_path.exists() and installed_path.stat().st_size > 0:
    try:
        data = json.loads(installed_path.read_text())
        if not isinstance(data, dict) or not isinstance(data.get("plugins"), list):
            raise ValueError("installed.json is not a valid InstalledFile object")
    except (ValueError, json.JSONDecodeError) as exc:
        backup = installed_path.with_suffix(".json.traceary-backup")
        shutil.copy2(installed_path, backup)
        print(f"warning: installed.json is invalid ({exc}); backed up to {backup} and starting fresh", file=sys.stderr)
PY

# Stage the new package and swap it in atomically, keeping the previous
# install intact until the copy succeeds.
STAGING_DIR="${KIMI_HOME}/plugins/managed/.traceary-staging"
rm -rf "${STAGING_DIR}"
mkdir -p "${KIMI_HOME}/plugins/managed"
cp -R "${PLUGIN_DIR}" "${STAGING_DIR}"
rm -rf "${MANAGED_DIR}"
mv "${STAGING_DIR}" "${MANAGED_DIR}"

python3 - "${INSTALLED_JSON}" "${MANAGED_DIR}" <<'PY'
import json
import pathlib
import sys
from datetime import datetime, timezone

installed_path = pathlib.Path(sys.argv[1])
managed_dir = sys.argv[2]

data = {"plugins": []}
if installed_path.exists() and installed_path.stat().st_size > 0:
    try:
        candidate = json.loads(installed_path.read_text())
        if isinstance(candidate, dict) and isinstance(candidate.get("plugins"), list):
            data = candidate
    except (ValueError, json.JSONDecodeError):
        # Validated (and backed up) earlier; fall back to a fresh record.
        data = {"plugins": []}

plugins = []
preserved = {}
for entry in data.get("plugins", []):
    if entry.get("id") == "traceary":
        preserved = entry
    else:
        plugins.append(entry)

record = dict(preserved)
record.update({
    "id": "traceary",
    "root": managed_dir,
    "source": preserved.get("source", "local-path"),
    "enabled": preserved.get("enabled", True),
    "state": preserved.get("state", "ok"),
    "installedAt": datetime.now(timezone.utc).isoformat(),
})
plugins.append(record)
data["plugins"] = plugins

tmp_path = installed_path.with_suffix(".json.tmp")
tmp_path.write_text(json.dumps(data, indent=2) + "\n")
tmp_path.replace(installed_path)
PY

echo "installed Traceary Kimi plugin to ${MANAGED_DIR}"
echo "next: run '/plugins reload' (or start a new session) to activate hooks, skills, and the traceary MCP server"
