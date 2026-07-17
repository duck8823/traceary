#!/usr/bin/env bash
# Emit a ready-to-paste marketplace.json plugin entry for xai-org/plugin-marketplace.
# Usage: ./scripts/generate-grok-marketplace-entry.sh [git-ref]
# Default ref: HEAD. Prefer a release tag commit after tagging vX.Y.Z.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REF="${1:-HEAD}"
SHA="$(git -C "${ROOT_DIR}" rev-parse "${REF}")"
VERSION="$(tr -d '[:space:]' <"${ROOT_DIR}/VERSION" 2>/dev/null || echo "0.0.0")"
export TRACEARY_MARKETPLACE_SHA="${SHA}"
export TRACEARY_MARKETPLACE_VERSION="${VERSION}"

python3 - <<'PY'
import json
import os
import sys

sha = os.environ["TRACEARY_MARKETPLACE_SHA"]
version = os.environ["TRACEARY_MARKETPLACE_VERSION"]
entry = {
  "name": "traceary",
  "description": (
    "Local-first Traceary integration for Grok Build: native session/audit/"
    "transcript/compact hooks, local MCP tools, and shared memory/session skills. "
    "Records work logs and command audits to a private SQLite store without sending "
    "data to Traceary servers."
  ),
  "category": "development",
  "source": {
    "source": "url",
    "url": "https://github.com/duck8823/traceary.git",
    "sha": sha,
    "path": "integrations/grok-plugin",
  },
  "homepage": "https://github.com/duck8823/traceary",
  "keywords": [
    "traceary",
    "session history",
    "command audit",
    "durable memory",
    "local-first",
    "mcp",
  ],
  "version": version,
  "author": {
    "name": "Shunsuke Maeda",
    "email": "duck8823@gmail.com",
  },
}
print(json.dumps(entry, indent=2))
print(
  f"# Pin sha={sha} path=integrations/grok-plugin version={version}",
  file=sys.stderr,
)
print(
  "# Open a PR against https://github.com/xai-org/plugin-marketplace "
  "adding this object to .grok-plugin/marketplace.json plugins[], then run "
  "python3 scripts/generate-plugin-index.py && python3 scripts/validate-catalog.py",
  file=sys.stderr,
)
PY
