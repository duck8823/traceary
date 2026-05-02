#!/bin/bash

# smoke_test_gemini_activation.sh runs
# `traceary memory activate --target gemini --dry-run` and materialises
# that returned plan into a temp project without using the product
# `--apply` path. This gives operators a concrete GEMINI.md +
# `.traceary/memories/gemini.md` pair for manual runtime probes while
# keeping v0.13.0-6 itself read-only. The real `--apply` path is wired
# in v0.13.0-7 (#895), so this script intentionally confirms `--apply`
# is still refused today. When #895 lands, this script must be
# extended end-to-end (apply creates both files, second apply converges
# to noop, post-apply status is in_sync) so Gemini inherits the same
# CI-side regression coverage Claude already has.
#
# Launching Gemini CLI from CI is non-deterministic (auth state, model
# availability, hierarchical context load order), so the live runtime
# probe is gated behind TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 and
# falls through to a printed manual verification when gemini is not
# available. The structural checks (file paths, marker layout,
# import-line format) always run regardless of gemini availability.

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
  --target gemini \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-gemini-smoke \
  --dry-run \
  --json) > "${TMP_PROJECT}/plan.json"

python3 - "${TMP_PROJECT}" "${TMP_PROJECT}/plan.json" <<'PY'
import json
import os
import sys

tmp_project = os.path.realpath(sys.argv[1])
plan_path = sys.argv[2]
with open(plan_path, encoding="utf-8") as fp:
    plan = json.load(fp)

target = plan.get("target")
assert target == "gemini", f"unexpected target: {target}"

host = plan.get("host_context")
external = plan.get("external_memory")
assert host is not None, "host_context component missing"
assert external is not None, "external_memory component missing"

assert host["path"].endswith(os.sep + "GEMINI.md"), f"host path not GEMINI.md: {host['path']}"
assert external["path"].endswith(os.path.join(".traceary", "memories", "gemini.md")), f"unexpected external path: {external['path']}"

stub = host["markdown"]
assert "<!-- traceary-memory-import:begin:v1 -->" in stub, "missing begin marker"
assert "<!-- traceary-memory-import:end -->" in stub, "missing end marker"
assert "@./.traceary/memories/gemini.md" in stub, f"missing relative import line: {stub}"
assert "--target gemini" in stub, f"DO NOT EDIT warning must reference --target gemini: {stub}"

for component in (host, external):
    component_path = os.path.realpath(component["path"])
    assert os.path.commonpath([tmp_project, component_path]) == tmp_project, (
        f"refusing to materialise path outside temp project: {component['path']}"
    )
    os.makedirs(os.path.dirname(component_path), exist_ok=True)
    with open(component_path, "w", encoding="utf-8") as fp:
        fp.write(component["markdown"])

print("ok: traceary memory activate --target gemini --dry-run produced and materialised the expected pair plan")
PY

(cd "${ROOT_DIR}" && go run . memory activate \
  --target gemini \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-gemini-smoke \
  --status \
  --json) > "${TMP_PROJECT}/status.json"

python3 - "${TMP_PROJECT}/status.json" <<'PY'
import json
import sys

status_path = sys.argv[1]
with open(status_path, encoding="utf-8") as fp:
    status = json.load(fp)

assert status.get("target") == "gemini", f"unexpected target: {status.get('target')}"
assert status.get("state") == "in_sync", f"status after materialising plan must be in_sync: {status}"
assert "apply_command" not in status, f"gemini status must not advertise apply until #895: {status}"
assert status.get("host_context", {}).get("state") == "in_sync", f"host not in_sync: {status}"
assert status.get("external_memory", {}).get("state") == "in_sync", f"external not in_sync: {status}"

print("ok: materialised gemini dry-run plan is recognised as in_sync by --status")
PY

# v0.13.0-6 ships read-only surfaces. The apply path lands in #895, so
# this script only calls `traceary memory activate --target gemini
# --apply` to assert the command is refused today; #895 must replace
# this refusal assertion with the same end-to-end checks the Claude
# smoke script performs (first apply creates both files, second apply
# converges to noop, post-apply status is in_sync), and must
# additionally assert that any pre-existing `## Gemini Added Memories`
# section in GEMINI.md survives byte-for-byte after apply.

# Confirm `--apply` is currently refused for gemini so a regression
# that silently enables apply is caught immediately.
if (cd "${ROOT_DIR}" && go run . memory activate \
  --target gemini \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-gemini-smoke \
  --apply >/dev/null 2>&1); then
  echo 'fail: gemini --apply must be refused until #895 lands.' >&2
  exit 1
fi

if [[ "${TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE:-0}" != "1" ]]; then
  if [[ "${KEEP_TEMP}" == "1" ]]; then
    echo "manual runtime probe: cd \"${TMP_PROJECT}\" and run \`gemini\` to confirm the @./.traceary/memories/gemini.md import loads."
  else
    echo 'set TRACEARY_KEEP_SMOKE_TEMP=1 to retain the temp project for a manual `gemini` probe (default: temp project is removed at exit).'
  fi
  echo 'ok: structural smoke test passed (set TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 for an authenticated live probe).'
  exit 0
fi

if ! command -v gemini >/dev/null 2>&1; then
  echo 'skip: gemini binary not found; structural checks already passed.'
  exit 0
fi

# Live probe: v0.13.0-6 leaves Gemini launch as an operator-run
# readiness check because authenticated Gemini CLI startup is
# environment-dependent. The materialised temp project is ready for
# that manual probe when TRACEARY_KEEP_SMOKE_TEMP=1.
echo 'skip: automated live gemini probe is reserved for #895; structural materialisation already passed.'
