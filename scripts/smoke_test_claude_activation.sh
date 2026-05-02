#!/bin/bash

# smoke_test_claude_activation.sh materialises a temp project and runs
# `traceary memory activate --target claude` end-to-end so an operator
# can manually verify Claude Code loads the resulting `.traceary/`
# import path. The script is best-effort: launching Claude Code from
# CI is non-deterministic (auth, model availability, first-time
# approval dialog), so the live runtime probe is gated behind
# TRACEARY_ENABLE_CLAUDE_RUNTIME_SMOKE=1 and falls through to a printed
# manual verification when claude is not available. The structural
# checks (file paths, marker layout, import-line format, apply
# idempotence, and post-apply status convergence) always run
# regardless of claude availability so v0.13.0-5 keeps a CI-side
# verification of the apply path.

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

# Materialise the planned files via the live --apply path. v0.13.0-5
# expects `traceary memory activate --target claude --apply` to write
# both the host context import stub and the external memory file using
# the safe activation writer. Running --apply twice must converge to
# noop; the second run validates that the planner is idempotent.
APPLY_FIRST="$(cd "${ROOT_DIR}" && go run . memory activate \
  --target claude \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-claude-smoke \
  --apply \
  --json)"
echo "${APPLY_FIRST}" > "${TMP_PROJECT}/apply_first.json"

APPLY_SECOND="$(cd "${ROOT_DIR}" && go run . memory activate \
  --target claude \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-claude-smoke \
  --apply \
  --json)"
echo "${APPLY_SECOND}" > "${TMP_PROJECT}/apply_second.json"

STATUS_AFTER="$(cd "${ROOT_DIR}" && go run . memory activate \
  --target claude \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-claude-smoke \
  --status \
  --json)"
echo "${STATUS_AFTER}" > "${TMP_PROJECT}/status_after.json"

python3 - "${TMP_PROJECT}/plan.json" "${TMP_PROJECT}/apply_first.json" "${TMP_PROJECT}/apply_second.json" "${TMP_PROJECT}/status_after.json" "${TMP_PROJECT}" <<'PY'
import json
import os
import sys

plan_path, first_path, second_path, status_path, project = sys.argv[1:6]
with open(plan_path, encoding="utf-8") as fp:
    plan = json.load(fp)
with open(first_path, encoding="utf-8") as fp:
    first = json.load(fp)
with open(second_path, encoding="utf-8") as fp:
    second = json.load(fp)
with open(status_path, encoding="utf-8") as fp:
    status = json.load(fp)

host = plan["host_context"]
external = plan["external_memory"]

# First apply must create both files.
assert first.get("action") == "created", f"first apply action = {first.get('action')!r}, want created"
assert first.get("host_context", {}).get("action") == "created", "host context first apply action mismatch"
assert first.get("external_memory", {}).get("action") == "created", "external memory first apply action mismatch"
assert first.get("host_context", {}).get("state") == "in_sync", "host context first apply state mismatch"
assert first.get("external_memory", {}).get("state") == "in_sync", "external memory first apply state mismatch"

# Second apply must converge to noop on both files.
assert second.get("action") == "noop", f"second apply action = {second.get('action')!r}, want noop"
assert second.get("host_context", {}).get("action") == "noop", "host context second apply must be noop"
assert second.get("external_memory", {}).get("action") == "noop", "external memory second apply must be noop"

# Post-apply status must be in_sync for both files.
assert status.get("state") == "in_sync", f"post-apply state = {status.get('state')!r}, want in_sync"
assert status.get("host_context", {}).get("state") == "in_sync", "host context post-apply state mismatch"
assert status.get("external_memory", {}).get("state") == "in_sync", "external memory post-apply state mismatch"

# The on-disk files must match the planned managed regions.
with open(host["path"], encoding="utf-8") as fp:
    host_disk = fp.read()
with open(external["path"], encoding="utf-8") as fp:
    external_disk = fp.read()
assert "<!-- traceary-memory-import:begin:v1 -->" in host_disk, "applied CLAUDE.md missing import begin marker"
assert "@./.traceary/memories/claude.md" in host_disk, "applied CLAUDE.md missing relative import line"
assert "<!-- traceary-memories:begin:v1 -->" in external_disk, "applied external memory file missing managed begin marker"

print(f"applied pair under {project}")
print(f"  CLAUDE.md = {host['path']}")
print(f"  external memory = {external['path']}")
print("ok: --apply created both files, second --apply was noop, post-apply status is in_sync")
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
