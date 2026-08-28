#!/usr/bin/env bash
# Behavior tests for the body-free projection-completion release gate.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY="${ROOT_DIR}/scripts/verify-projection-completion.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-projection-completion-test.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

COPY_DB="${TMP_DIR}/copy.db"
: >"${COPY_DB}"

run_live_env() {
  env TRACEARY_DB_PATH="${COPY_DB}" TRACEARY_PROJECTION_TEST_MODE=1 "${VERIFY}" "$@"
}

write_json() {
  local path="$1" body="$2"
  printf '%s\n' "${body}" >"${path}"
}

complete_rebuilds() {
  local dir="$1"
  write_json "${dir}/rebuild.1.json" '{"result_kind":"generation","generation_id":"g1"}'
  write_json "${dir}/rebuild.2.json" '{"result_kind":"run","stop_reason":"total_wall_time","batches":100,"progress":{"selected":1,"written":1,"evicted":0,"cleaned":0,"stored_bytes":1,"decoded_bytes":1,"written_bytes":1,"cleanup_bytes":0,"generation_id":"g1"}}'
  write_json "${dir}/rebuild.3.json" '{"result_kind":"run","stop_reason":"complete","batches":10,"progress":{"selected":1,"written":1,"evicted":0,"cleaned":1,"stored_bytes":1,"decoded_bytes":1,"written_bytes":1,"cleanup_bytes":1,"generation_id":"g1"}}'
}

complete_states() {
  local dir="$1"
  write_json "${dir}/state.1.json" '{"state":"rebuilding","phase":"source","generation_id":"g1","active_generation_id":"","config_hash":"v4:1:1:1:1:1","high_water":10,"checkpoint":10,"failure_class":"","origin":"operator","index_family_within_budget":0,"cutover_family_bytes_before":0,"cutover_family_bytes_after":0,"updated_at":"2026-08-29T00:00:00Z"}'
  write_json "${dir}/state.2.json" '{"state":"rebuilding","phase":"source","generation_id":"g1","active_generation_id":"","config_hash":"v4:1:1:1:1:1","high_water":50,"checkpoint":50,"failure_class":"","origin":"operator","index_family_within_budget":0,"cutover_family_bytes_before":0,"cutover_family_bytes_after":0,"updated_at":"2026-08-29T00:00:01Z"}'
  write_json "${dir}/state.3.json" '{"state":"complete","phase":"complete","generation_id":"g1","active_generation_id":"g1","config_hash":"v4:1:1:1:1:1","high_water":100,"checkpoint":100,"failure_class":"","origin":"operator","index_family_within_budget":1,"cutover_family_bytes_before":0,"cutover_family_bytes_after":0,"updated_at":"2026-08-29T00:00:02Z"}'
}

complete_lifecycle() {
  local dir="$1" state="${2:-complete}" klass="${3:-}"
  write_json "${dir}/lifecycle.json" "$(printf '{"generation_id":"g1","state":"%s","config_hash":"v4:1:1:1:1:1","source_revision":1,"high_water":100,"abandoned_at":"","failure_class":"%s","terminal_at":"","reclaimed_at":"","reclaimed_rows":0,"reclaimed_logical_bytes":0}' "${state}" "${klass}")"
}

complete_family() {
  local dir="$1" total="${2:-1000}"
  write_json "${dir}/family.json" "$(printf '{"total":%s,"by_table":{"search_projection_session_keywords":%s},"by_object":{"search_projection_session_keywords":{"tbl_name":"search_projection_session_keywords","type":"table","bytes":%s}},"database_btree_total":%s,"measured_seconds":0.0}' "${total}" "${total}" "${total}" "${total}")"
}

complete_doctor() {
  local dir="$1" status="${2:-pass}" extra="${3:-}"
  python3 -c 'import json,sys
status=sys.argv[1]
extra=sys.argv[2]
checks=[
  {"name":"search-projection-generation","status":status,"message":"must not be parsed"},
  {"name":"store-size","status":"warn","message":"must not be parsed"},
  {"name":"search-projection-budget","status":"pass","message":"must not be parsed"},
]
if extra:
    checks.append({"name":"unrelated-check","status":"pass","message":extra})
print(json.dumps({"checks": checks}))
' "${status}" "${extra}" >"${dir}/doctor.json"
}

setup_complete() {
  local dir="$1"
  mkdir -p "${dir}"
  complete_rebuilds "${dir}"
  complete_states "${dir}"
  complete_lifecycle "${dir}" complete ""
  complete_family "${dir}" 1000
  complete_doctor "${dir}" pass
}

assert_exit() {
  local expected="$1"
  local rc=0
  shift
  "$@" >/dev/null 2>"${TMP_DIR}/stderr.txt" || rc=$?
  if [[ "${rc}" -ne "${expected}" ]]; then
    echo "error: expected exit ${expected}, got ${rc}" >&2
    cat "${TMP_DIR}/stderr.txt" >&2
    exit 1
  fi
}

# 1. Live path is refused (file need not exist).
HOME="${TMP_DIR}" TRACEARY_DB_PATH="${TMP_DIR}/.config/traceary/traceary.db" \
  assert_exit 64 "${VERIFY}" --out "${TMP_DIR}/ignored.json"
grep -q 'copy' "${TMP_DIR}/stderr.txt" || {
  echo 'error: live-path refusal did not mention copy' >&2
  cat "${TMP_DIR}/stderr.txt" >&2
  exit 1
}
echo 'ok: refuses the default live path'

# 2. Unset env is a usage error.
(unset TRACEARY_DB_PATH; assert_exit 64 "${VERIFY}" --out "${TMP_DIR}/ignored.json")
echo 'ok: refuses unset TRACEARY_DB_PATH'

# 3. Fixture inputs are test-only.
assert_exit 64 "${VERIFY}" --fixture-dir "${TMP_DIR}/nope" --out "${TMP_DIR}/ignored.json"
echo 'ok: restricts fixture inputs to test mode'

# 4. A completing run passes.
FIX4="${TMP_DIR}/fix4"
setup_complete "${FIX4}"
OUT4="${TMP_DIR}/out4.json"
run_live_env --fixture-dir "${FIX4}" --out "${OUT4}" >/dev/null
python3 -c 'import json,sys
e=json.load(open(sys.argv[1]))
assert e["result"]=="PASS", e
assert len(e["transitions"])>=2, e["transitions"]
assert len(e["iterations"])==3, e["iterations"]
assert e["family_bytes"]["by_table"], e["family_bytes"]
' "${OUT4}"
echo 'ok: passes when the run reaches complete'

# 5. An abandoned generation fails.
FIX5="${TMP_DIR}/fix5"
setup_complete "${FIX5}"
write_json "${FIX5}/rebuild.1.json" '{"result_kind":"generation","generation_id":"g1"}'
write_json "${FIX5}/rebuild.2.json" '{"result_kind":"run","stop_reason":"complete","batches":1,"progress":{"generation_id":"g1"}}'
write_json "${FIX5}/state.1.json" '{"state":"rebuilding","phase":"source","generation_id":"g1","active_generation_id":"","failure_class":""}'
write_json "${FIX5}/state.2.json" '{"state":"idle","phase":"source","generation_id":"g1","active_generation_id":"","failure_class":""}'
complete_lifecycle "${FIX5}" abandoned cleanup_no_progress
OUT5="${TMP_DIR}/out5.json"
set +e
run_live_env --fixture-dir "${FIX5}" --out "${OUT5}" >/dev/null
rc5=$?
set -e
[[ "${rc5}" -eq 1 ]] || { echo "error: abandoned case exit ${rc5}" >&2; exit 1; }
python3 -c 'import json,sys
e=json.load(open(sys.argv[1]))
assert e["result"]=="FAIL", e
assert e["failure"]["state"]=="abandoned", e["failure"]
assert e["failure"]["failure_class"]=="cleanup_no_progress", e["failure"]
' "${OUT5}"
echo 'ok: fails when the generation is abandoned'

# 6. Iteration bound fails loudly.
FIX6="${TMP_DIR}/fix6"
setup_complete "${FIX6}"
write_json "${FIX6}/rebuild.1.json" '{"result_kind":"generation","generation_id":"g1"}'
write_json "${FIX6}/rebuild.2.json" '{"result_kind":"run","stop_reason":"total_wall_time","batches":100}'
rm -f "${FIX6}/rebuild.3.json"
write_json "${FIX6}/state.1.json" '{"state":"rebuilding","phase":"source","generation_id":"g1","active_generation_id":""}'
write_json "${FIX6}/state.2.json" '{"state":"rebuilding","phase":"source","generation_id":"g1","active_generation_id":""}'
OUT6="${TMP_DIR}/out6.json"
set +e
run_live_env --fixture-dir "${FIX6}" --out "${OUT6}" --max-iterations 2 >/dev/null
rc6=$?
set -e
[[ "${rc6}" -eq 1 ]] || { echo "error: max-iterations case exit ${rc6}" >&2; exit 1; }
python3 -c 'import json,sys
e=json.load(open(sys.argv[1]))
assert e["result"]=="FAIL", e
assert (e["failure"]["reason"] or "").startswith("max_iterations"), e["failure"]
' "${OUT6}"
echo 'ok: fails when max iterations is reached without completion'

# 7. doctor contradiction fails.
FIX7="${TMP_DIR}/fix7"
setup_complete "${FIX7}"
complete_doctor "${FIX7}" warn
OUT7="${TMP_DIR}/out7.json"
set +e
run_live_env --fixture-dir "${FIX7}" --out "${OUT7}" >/dev/null
rc7=$?
set -e
[[ "${rc7}" -eq 1 ]] || { echo "error: doctor contradiction exit ${rc7}" >&2; exit 1; }
python3 -c 'import json,sys
e=json.load(open(sys.argv[1]))
assert e["result"]=="FAIL", e
assert e["failure"]["reason"].startswith("doctor_generation_"), e["failure"]
' "${OUT7}"
echo 'ok: fails when doctor does not report pass even if state is complete'

# 8. Family bytes are recorded, not gated.
FIX8="${TMP_DIR}/fix8"
setup_complete "${FIX8}"
complete_family "${FIX8}" 3000000000
OUT8="${TMP_DIR}/out8.json"
run_live_env --fixture-dir "${FIX8}" --out "${OUT8}" >/dev/null
python3 -c 'import json,sys
e=json.load(open(sys.argv[1]))
assert e["result"]=="PASS", e
fb=e["family_bytes"]
assert fb["total"]==3000000000, fb
assert fb["target"]==1535115264, fb
assert "over_target_bytes" in fb, fb
assert fb["over_target_bytes"]==3000000000-1535115264, fb
' "${OUT8}"
echo 'ok: records family bytes and does not gate on the budget'

# 9. No body parsing.
FIX9="${TMP_DIR}/fix9"
setup_complete "${FIX9}"
complete_doctor "${FIX9}" pass "must-not-be-parsed-sentinel"
OUT9="${TMP_DIR}/out9.json"
run_live_env --fixture-dir "${FIX9}" --out "${OUT9}" >/dev/null
python3 -c 'import json,sys
raw=open(sys.argv[1],encoding="utf-8").read()
assert "must-not-be-parsed-sentinel" not in raw, raw
e=json.load(open(sys.argv[1]))
for name in ("search-projection-generation","search-projection-budget","store-size"):
    assert e["doctor"][name]["message"]=="must not be parsed", e["doctor"][name]
' "${OUT9}"
echo 'ok: never parses message bodies'

# 10. Path inside the live store directory is refused.
HOME="${TMP_DIR}" TRACEARY_DB_PATH="${TMP_DIR}/.config/traceary/copies/x.db" \
  assert_exit 64 "${VERIFY}" --out "${TMP_DIR}/ignored.json"
echo 'ok: rejects a path inside the live store directory'
