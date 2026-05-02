#!/bin/bash

# smoke_test_claude_activation.sh materialises a temp project and runs
# `traceary memory activate --target claude --dry-run` so an operator can
# manually verify Claude Code loads the resulting `.traceary/` import
# path. The script is best-effort: launching Claude Code from CI is
# non-deterministic (auth, model availability, first-time approval
# dialog), so the live runtime probe is gated behind
# TRACEARY_ENABLE_CLAUDE_RUNTIME_SMOKE=1 and falls through to a printed
# manual verification when claude is not available. The structural
# checks (file paths, marker layout, import-line format) always run
# regardless of claude availability.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TMP_PROJECT="$(mktemp -d)"
KEEP_TEMP="${TRACEARY_KEEP_SMOKE_TEMP:-0}"
if [[ "${KEEP_TEMP}" != "1" ]]; then
  trap 'rm -rf "${TMP_PROJECT}"' EXIT
fi

mkdir -p "${TMP_PROJECT}/.git"
echo 'ref: refs/heads/main' > "${TMP_PROJECT}/.git/HEAD"

(cd "${ROOT_DIR}" && go run . memory activate \
  --target claude \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-claude-smoke \
  --dry-run \
  --json) > "${TMP_PROJECT}/plan.json"

python3 - "${TMP_PROJECT}/plan.json" <<'PY'
import json
import os
import sys

plan_path = sys.argv[1]
with open(plan_path, encoding="utf-8") as fp:
    plan = json.load(fp)

target = plan.get("target")
assert target == "claude", f"unexpected target: {target}"

host = plan.get("host_context")
external = plan.get("external_memory")
assert host is not None, "host_context component missing"
assert external is not None, "external_memory component missing"

assert host["path"].endswith(os.sep + "CLAUDE.md"), f"host path not CLAUDE.md: {host['path']}"
assert external["path"].endswith(os.path.join(".traceary", "memories", "claude.md")), f"unexpected external path: {external['path']}"

stub = host["markdown"]
assert "<!-- traceary-memory-import:begin:v1 -->" in stub, "missing begin marker"
assert "<!-- traceary-memory-import:end -->" in stub, "missing end marker"
assert "@./.traceary/memories/claude.md" in stub, f"missing relative import line: {stub}"

print("ok: traceary memory activate --target claude --dry-run produced the expected pair plan")
PY

# Materialise the planned files so the operator can launch Claude Code
# in this directory and confirm the import resolves. Artefacts live
# inside ${TMP_PROJECT}; by default the trap rm -rf cleans them up at
# exit. Set TRACEARY_KEEP_SMOKE_TEMP=1 to retain ${TMP_PROJECT} for a
# manual probe (no trap is installed in that mode).
python3 - "${TMP_PROJECT}/plan.json" "${TMP_PROJECT}" <<'PY'
import json
import os
import sys

plan_path = sys.argv[1]
project = sys.argv[2]
with open(plan_path, encoding="utf-8") as fp:
    plan = json.load(fp)

host = plan["host_context"]
external = plan["external_memory"]

os.makedirs(os.path.dirname(external["path"]), exist_ok=True)
with open(external["path"], "w", encoding="utf-8") as fp:
    fp.write(external["markdown"])
with open(host["path"], "w", encoding="utf-8") as fp:
    fp.write(host["markdown"])

print(f"materialised pair under {project}")
print(f"  CLAUDE.md = {host['path']}")
print(f"  external memory = {external['path']}")
PY

if [[ "${TRACEARY_ENABLE_CLAUDE_RUNTIME_SMOKE:-0}" != "1" ]]; then
  if [[ "${KEEP_TEMP}" == "1" ]]; then
    echo "manual runtime probe: cd \"${TMP_PROJECT}\" and run \`claude\` to confirm the @./.traceary/memories/claude.md import loads."
  else
    echo 'set TRACEARY_KEEP_SMOKE_TEMP=1 to retain the temp project for a manual `claude` probe (default: temp project is removed at exit).'
  fi
  echo 'ok: structural smoke test passed (set TRACEARY_ENABLE_CLAUDE_RUNTIME_SMOKE=1 for an authenticated live probe).'
  exit 0
fi

if ! command -v claude >/dev/null 2>&1; then
  echo 'skip: claude binary not found; structural checks already passed.'
  exit 0
fi

# Live probe: ask Claude Code to echo back the marker we placed in the
# external memory file. Whether this works depends on the operator's
# Claude auth state and on whether Claude Code has approved the
# external import (the documented first-time approval dialog).
claude --print --model sonnet --permission-mode plan \
  --working-directory "${TMP_PROJECT}" \
  'Quote one bullet from the Traceary-managed memories that you can see in CLAUDE.md, verbatim.'
