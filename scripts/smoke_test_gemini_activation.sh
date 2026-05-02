#!/bin/bash

# smoke_test_gemini_activation.sh materialises a temp project and runs
# `traceary memory activate --target gemini` end-to-end so an operator
# can manually verify Gemini CLI loads the resulting `.traceary/`
# import path. The script is best-effort: launching Gemini CLI from
# CI is non-deterministic (auth, model availability, hierarchical
# context loading), so the live runtime probe is gated behind
# TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 and falls through to a printed
# manual verification when gemini is not available. The structural
# checks (file paths, marker layout, import-line format, first apply,
# idempotent re-apply, post-apply status convergence, and byte-for-byte
# preservation of `## Gemini Added Memories`) always run regardless of
# gemini availability so v0.13.0-7 keeps a CI-side verification of the
# apply path.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

TMP_PROJECT="$(mktemp -d)"
KEEP_TEMP="${TRACEARY_KEEP_SMOKE_TEMP:-0}"
if [[ "${KEEP_TEMP}" != "1" ]]; then
  trap 'rm -rf "${TMP_PROJECT}"' EXIT
fi

mkdir -p "${TMP_PROJECT}/.git"
echo 'ref: refs/heads/main' > "${TMP_PROJECT}/.git/HEAD"

# Seed GEMINI.md with a pre-existing `## Gemini Added Memories`
# section so the apply path can prove it preserves Gemini's
# save_memory output byte-for-byte.
ADDED_MEMORIES_FIXTURE="$(cat <<'MEMORIES'
# Project notes

- prefer English commit messages

## Gemini Added Memories

- The user prefers concise replies.
- Always cite sources.
MEMORIES
)"
printf '%s\n' "${ADDED_MEMORIES_FIXTURE}" > "${TMP_PROJECT}/GEMINI.md"
ADDED_MEMORIES_BYTES_BEFORE="$(wc -c < "${TMP_PROJECT}/GEMINI.md" | tr -d ' ')"

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

print("ok: traceary memory activate --target gemini --dry-run produced the expected pair plan")
PY

# Materialise the planned files via the live --apply path. v0.13.0-7
# expects `traceary memory activate --target gemini --apply` to update
# the seeded host context import stub and create the external memory
# file using the safe activation writer. Running --apply twice must
# converge to noop; the second run validates that the planner is
# idempotent.
APPLY_FIRST="$(cd "${ROOT_DIR}" && go run . memory activate \
  --target gemini \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-gemini-smoke \
  --apply \
  --json)"
echo "${APPLY_FIRST}" > "${TMP_PROJECT}/apply_first.json"

APPLY_SECOND="$(cd "${ROOT_DIR}" && go run . memory activate \
  --target gemini \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-gemini-smoke \
  --apply \
  --json)"
echo "${APPLY_SECOND}" > "${TMP_PROJECT}/apply_second.json"

STATUS_AFTER="$(cd "${ROOT_DIR}" && go run . memory activate \
  --target gemini \
  --db-path "${TMP_PROJECT}/traceary.db" \
  --root "${TMP_PROJECT}" \
  --workspace github.com/duck8823/traceary-gemini-smoke \
  --status \
  --json)"
echo "${STATUS_AFTER}" > "${TMP_PROJECT}/status_after.json"

python3 - "${TMP_PROJECT}/plan.json" "${TMP_PROJECT}/apply_first.json" "${TMP_PROJECT}/apply_second.json" "${TMP_PROJECT}/status_after.json" "${TMP_PROJECT}" "${ADDED_MEMORIES_FIXTURE}" "${ADDED_MEMORIES_BYTES_BEFORE}" <<'PY'
import json
import os
import sys

plan_path, first_path, second_path, status_path, project, fixture, bytes_before = sys.argv[1:8]
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

# First apply must update host (existing GEMINI.md gains the stub) and
# create the external file. The aggregate action priority (created >
# updated > noop) means the pair action reports "created".
assert first.get("action") == "created", f"first apply action = {first.get('action')!r}, want created"
assert first.get("host_context", {}).get("action") == "updated", "host context first apply action mismatch"
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
assert "<!-- traceary-memory-import:begin:v1 -->" in host_disk, "applied GEMINI.md missing import begin marker"
assert "@./.traceary/memories/gemini.md" in host_disk, "applied GEMINI.md missing relative import line"
assert "<!-- traceary-memories:begin:v1 -->" in external_disk, "applied external memory file missing managed begin marker"

# The pre-existing `## Gemini Added Memories` section must be preserved
# byte-for-byte. Apply may only append the managed import stub at the
# end; it must never mutate the user-authored prefix.
fixture_prefix = fixture if fixture.endswith("\n") else fixture + "\n"
assert host_disk.startswith(fixture_prefix), (
    f"applied GEMINI.md did not preserve the user-authored Gemini Added Memories prefix byte-for-byte:\n--- expected prefix ---\n{fixture_prefix!r}\n--- got ---\n{host_disk[:len(fixture_prefix)]!r}"
)
assert host_disk.count("## Gemini Added Memories") == 1, "applied GEMINI.md duplicated the Gemini Added Memories section"
begin_idx = host_disk.index("<!-- traceary-memory-import:begin:v1 -->")
added_idx = host_disk.index("## Gemini Added Memories")
assert begin_idx > added_idx, "managed import stub must be appended after the Gemini Added Memories section"

print(f"applied pair under {project}")
print(f"  GEMINI.md = {host['path']}")
print(f"  external memory = {external['path']}")
print("ok: --apply materialised the pair, second --apply was noop, post-apply status is in_sync")
print(f"ok: pre-existing `## Gemini Added Memories` section preserved byte-for-byte ({bytes_before} bytes prefix)")
PY

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

# Live probe: ask Gemini CLI to echo back the marker we placed in the
# external memory file. Whether this works depends on the operator's
# Gemini auth state and on whether the hierarchical context load picks
# up the @./.traceary/memories/gemini.md import.
(cd "${TMP_PROJECT}" && gemini --prompt \
  'Quote one bullet from the Traceary-managed memories that you can see in GEMINI.md, verbatim.')
