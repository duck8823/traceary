#!/bin/bash
# Install the Traceary Kimi plugin into the local Kimi Code plugin directory.
#
# Kimi Code loads managed plugins from $KIMI_CODE_HOME/plugins/managed/<id>/
# (default ~/.kimi-code) and tracks them in $KIMI_CODE_HOME/plugins/
# installed.json. This script mirrors the official local-install behavior
# (schema verified against Kimi Code 0.27.0): the package is staged as a
# generation directory and the managed symlink is flipped with a single
# atomic rename, then the install record is upserted while preserving
# user-controlled fields (enabled/state and unknown keys) from a previous
# install. Re-running is idempotent.
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
# copy, so a corrupt file cannot leave a half-updated install behind. Every
# entry must be an object with an id, or the merge below would fail after
# the swap.
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
        if any(not isinstance(entry, dict) or "id" not in entry for entry in data["plugins"]):
            raise ValueError("installed.json contains a plugin entry without an id")
    except (ValueError, json.JSONDecodeError) as exc:
        backup = installed_path.with_suffix(".json.traceary-backup")
        shutil.copy2(installed_path, backup)
        print(f"warning: installed.json is invalid ({exc}); backed up to {backup} and starting fresh", file=sys.stderr)
PY

# Stage the new package as a unique generation directory and flip the
# managed symlink with a single rename, so the managed path never points at
# a missing or half-copied package. Kimi Code resolves the symlink
# (verified against 0.27.0).
MANAGED_ROOT="${KIMI_HOME}/plugins/managed"
mkdir -p "${MANAGED_ROOT}"
GEN_DIR="$(mktemp -d "${MANAGED_ROOT}/.traceary-gen-XXXXXXXX")"
cp -R "${PLUGIN_DIR}/." "${GEN_DIR}/"
python3 - "${MANAGED_DIR}" "${GEN_DIR}" <<'PY'
import os
import shutil
import sys

managed_dir, gen_dir = sys.argv[1], sys.argv[2]
tmp_link = managed_dir + ".traceary-tmp"
if os.path.lexists(tmp_link):
    os.unlink(tmp_link)
os.symlink(gen_dir, tmp_link)
backup_dir = managed_dir + ".traceary-backup"
try:
    if os.path.lexists(backup_dir):
        shutil.rmtree(backup_dir)
    if os.path.islink(managed_dir) or not os.path.lexists(managed_dir):
        # Single rename: the managed path flips to the new generation atomically.
        os.replace(tmp_link, managed_dir)
    else:
        # One-time migration from a direct-copy install: move the real
        # directory aside (single rename), flip the symlink, then clean up.
        os.replace(managed_dir, backup_dir)
        try:
            os.replace(tmp_link, managed_dir)
        except BaseException:
            os.replace(backup_dir, managed_dir)
            raise
        shutil.rmtree(backup_dir)
except BaseException:
    if os.path.lexists(tmp_link):
        os.unlink(tmp_link)
    raise
PY

# Prune superseded generations (the current one stays linked).
for old in "${MANAGED_ROOT}"/.traceary-gen-*; do
  [ -e "${old}" ] || continue
  [ "${old}" = "${GEN_DIR}" ] && continue
  rm -rf "${old}"
done

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
    if not isinstance(entry, dict):
        continue
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
