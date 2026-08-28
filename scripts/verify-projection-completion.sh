#!/usr/bin/env bash
# Drive the bounded search-projection rebuild to completion on a COPY of a
# Traceary store with a release-candidate binary, and record body-free JSON
# evidence. Never run this against the live store.
set -euo pipefail

TRACEARY_BIN="${TRACEARY_BIN:-traceary}"
SQLITE3_BIN="${SQLITE3_BIN:-sqlite3}"
OUT_PATH="./projection-completion-evidence.json"
MAX_ITERATIONS=200
MAX_WALL=21600
INDEX_FAMILY_BYTES=""
MIN_FREE_RATIO="3"
NOTE=""
FIXTURE_DIR=""
DEFAULT_FAMILY_TARGET=$((1464 << 20))
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  cat <<'USAGE'
Usage: scripts/verify-projection-completion.sh [options]

Drive the bounded search-projection rebuild to completion on a COPY of a
Traceary store with a release-candidate binary, and record body-free JSON
evidence. Never run this against the live store.

Options:
  --traceary PATH          binary to drive (default: traceary); recorded via `traceary -v`
  --out PATH               evidence JSON path (default: ./projection-completion-evidence.json)
  --max-iterations N       upper bound on `store compact --projection-rebuild` calls (default 200)
  --max-wall SECONDS       overall wall bound (default 21600 = 6h); exceeding it FAILs with reason=max_wall
  --index-family-bytes N   forwarded to every rebuild call (default: the binary's own default)
  --sqlite3 PATH           sqlite3 CLI (default: sqlite3); must expose dbstat and -json
  --min-free-ratio R       refuse to start when free bytes < R x copy size (default 3; integer or decimal)
  --note TEXT              free-form operator note copied into the evidence (e.g. a deviation reason)
  --fixture-dir DIR        test-fixture input only; requires TRACEARY_PROJECTION_TEST_MODE=1
  -h, --help

Env:
  TRACEARY_DB_PATH   REQUIRED. Path to a copy. Refused when it resolves to
                     $HOME/.config/traceary/traceary.db or to anything inside
                     $HOME/.config/traceary/.
USAGE
}

require_value() {
  local flag="$1"
  if [[ $# -lt 2 ]]; then
    echo "error: ${flag} requires a value" >&2
    exit 64
  fi
}

positive_int() {
  local flag="$1" value="$2"
  [[ "${value}" =~ ^[1-9][0-9]*$ ]] || {
    echo "error: ${flag} requires a positive integer" >&2
    exit 64
  }
}

positive_number() {
  local flag="$1" value="$2"
  python3 -c 'import sys
v=sys.argv[1]
try:
    n=float(v)
except ValueError:
    sys.exit(1)
sys.exit(0 if n > 0 else 1)' "${value}" || {
    echo "error: ${flag} requires a positive number" >&2
    exit 64
  }
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --traceary)
      require_value "$@"
      TRACEARY_BIN="$2"
      shift 2
      ;;
    --out)
      require_value "$@"
      OUT_PATH="$2"
      shift 2
      ;;
    --max-iterations)
      require_value "$@"
      positive_int "$1" "$2"
      MAX_ITERATIONS="$2"
      shift 2
      ;;
    --max-wall)
      require_value "$@"
      positive_int "$1" "$2"
      MAX_WALL="$2"
      shift 2
      ;;
    --index-family-bytes)
      require_value "$@"
      positive_int "$1" "$2"
      INDEX_FAMILY_BYTES="$2"
      shift 2
      ;;
    --sqlite3)
      require_value "$@"
      SQLITE3_BIN="$2"
      shift 2
      ;;
    --min-free-ratio)
      require_value "$@"
      positive_number "$1" "$2"
      MIN_FREE_RATIO="$2"
      shift 2
      ;;
    --note)
      require_value "$@"
      NOTE="$2"
      shift 2
      ;;
    --fixture-dir)
      require_value "$@"
      if [[ "${TRACEARY_PROJECTION_TEST_MODE:-}" != 1 ]]; then
        echo "error: --fixture-dir is restricted to fixture tests; set TRACEARY_PROJECTION_TEST_MODE=1" >&2
        exit 64
      fi
      FIXTURE_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown option $1" >&2
      usage >&2
      exit 64
      ;;
  esac
done

TEST_MODE=0
if [[ "${TRACEARY_PROJECTION_TEST_MODE:-}" == 1 && -n "${FIXTURE_DIR}" ]]; then
  TEST_MODE=1
fi

resolve_path() {
  local p="$1"
  python3 -c 'import os, sys
p = os.path.expanduser(sys.argv[1])
if not os.path.isabs(p):
    p = os.path.join(os.getcwd(), p)
parent = os.path.dirname(p)
base = os.path.basename(p)
if os.path.isdir(parent):
    print(os.path.join(os.path.realpath(parent), base))
else:
    print(os.path.normpath(p))
' "${p}"
}

refuse_live_path() {
  if [[ -z "${TRACEARY_DB_PATH:-}" ]]; then
    echo "error: TRACEARY_DB_PATH must point at a copy, not the live store" >&2
    exit 64
  fi
  local resolved live_db live_dir
  resolved="$(resolve_path "${TRACEARY_DB_PATH}")"
  live_db="$(resolve_path "${HOME}/.config/traceary/traceary.db")"
  live_dir="$(resolve_path "${HOME}/.config/traceary")"
  if [[ "${resolved}" == "${live_db}" ]]; then
    echo "error: TRACEARY_DB_PATH must point at a copy, not the live store" >&2
    exit 64
  fi
  if [[ "${resolved}" == "${live_dir}"/* ]]; then
    echo "error: TRACEARY_DB_PATH must point at a copy, not the live store" >&2
    exit 64
  fi
  if [[ ! -f "${resolved}" ]]; then
    echo "error: TRACEARY_DB_PATH must point at a copy, not the live store" >&2
    exit 64
  fi
  DB_PATH="${resolved}"
}

python_gate() {
  python3 - "$@" <<'PY'
import json
import os
import sys
import time
from datetime import datetime, timezone

TARGET = 1464 << 20
DOCTOR_NAMES = (
    "search-projection-generation",
    "search-projection-budget",
    "store-size",
)

def now_rfc3339():
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")

def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)

def dump_atomic(path, obj):
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(obj, fh, indent=2, ensure_ascii=True)
        fh.write("\n")
    os.replace(tmp, path)

def empty_doctor():
    return {
        name: {"presence": "absent", "status": None, "message": None}
        for name in DOCTOR_NAMES
    }

def empty_family(target):
    return {
        "total": None,
        "target": target,
        "over_target_bytes": None,
        "ratio_to_target": None,
        "measured_seconds": None,
        "database_btree_total": None,
        "by_object": {},
        "by_table": {},
    }

def normalize_family(raw, target):
    family = empty_family(target)
    if raw is None:
        return family
    if isinstance(raw, list):
        by_object = {}
        by_table = {}
        for row in raw:
            obj = row.get("object") or row.get("name")
            tbl = row.get("tbl") or row.get("tbl_name") or ""
            typ = row.get("type") or "table"
            nbytes = row.get("bytes")
            if obj is None or nbytes is None:
                continue
            by_object[obj] = {"tbl_name": tbl, "type": typ, "bytes": nbytes}
            by_table[tbl] = by_table.get(tbl, 0) + nbytes
        family["by_object"] = by_object
        family["by_table"] = by_table
        family["total"] = sum(by_table.values()) if by_table else 0
        family["database_btree_total"] = None
    elif isinstance(raw, dict):
        family["by_object"] = raw.get("by_object") or {}
        family["by_table"] = raw.get("by_table") or {}
        family["total"] = raw.get("total")
        family["database_btree_total"] = raw.get("database_btree_total")
        family["measured_seconds"] = raw.get("measured_seconds")
        if family["total"] is None and family["by_table"]:
            family["total"] = sum(family["by_table"].values())
    total = family.get("total")
    family["target"] = target
    if total is None:
        family["over_target_bytes"] = None
        family["ratio_to_target"] = None
    else:
        family["over_target_bytes"] = total - target
        family["ratio_to_target"] = (float(total) / float(target)) if target else None
    return family

def extract_doctor(report):
    out = empty_doctor()
    if not isinstance(report, dict):
        return out
    for check in report.get("checks") or []:
        name = check.get("name")
        if name in DOCTOR_NAMES:
            out[name] = {
                "presence": "present",
                "status": check.get("status"),
                "message": check.get("message"),
            }
    return out

def row0(payload):
    if payload is None:
        return None
    if isinstance(payload, list):
        return payload[0] if payload else None
    if isinstance(payload, dict):
        return payload
    return None

def as_list(payload):
    if payload is None:
        return []
    if isinstance(payload, list):
        return payload
    return [payload]

def lifecycle_for(state_row, rows):
    if not state_row:
        return None
    gen = state_row.get("generation_id") or ""
    for row in rows:
        if row.get("generation_id") == gen:
            return row
    return rows[-1] if rows else None

def fail_fields(state_row, lifecycle_row, reason):
    state = ""
    failure_class = ""
    if lifecycle_row:
        state = lifecycle_row.get("state") or ""
        failure_class = lifecycle_row.get("failure_class") or ""
    if not state and state_row:
        state = state_row.get("state") or ""
    if not failure_class and state_row:
        failure_class = state_row.get("failure_class") or ""
    return {"state": state, "failure_class": failure_class, "reason": reason}

def compute_verdict(ev, loop_complete):
    state_row = ev.get("final_state_row") or {}
    rows = ev.get("lifecycle_rows") or []
    lifecycle_row = ev.get("final_lifecycle_row") or lifecycle_for(state_row, rows)
    doctor_gen = (ev.get("doctor") or {}).get("search-projection-generation") or {
        "presence": "absent",
        "status": None,
    }
    singleton_state = (state_row.get("state") or "") if state_row else ""
    active = ((state_row.get("active_generation_id") or "") if state_row else "").strip()
    life_state = (lifecycle_row.get("state") or "") if lifecycle_row else ""
    existing = (ev.get("failure") or {}).get("reason") or ""
    if existing:
        return "FAIL", fail_fields(state_row, lifecycle_row, existing)
    if not loop_complete:
        reason = existing or "terminal_lifecycle: state=%s" % (life_state or singleton_state or "unknown")
        return "FAIL", fail_fields(state_row, lifecycle_row, reason)
    if singleton_state != "complete" or life_state != "complete":
        reason = "terminal_lifecycle: state=%s" % (life_state or singleton_state or "unknown")
        return "FAIL", fail_fields(state_row, lifecycle_row, reason)
    if active == "":
        return "FAIL", fail_fields(state_row, lifecycle_row, "complete_without_active_generation")
    if doctor_gen.get("presence") == "present" and doctor_gen.get("status") != "pass":
        return "FAIL", fail_fields(
            state_row,
            lifecycle_row,
            "doctor_generation_%s" % (doctor_gen.get("status") or "unknown"),
        )
    return "PASS", {"state": "", "failure_class": "", "reason": ""}

def cmd_init(path):
    budget_bytes = int(os.environ.get("GATE_BUDGET_BYTES") or TARGET)
    flags = json.loads(os.environ.get("GATE_BUDGET_FLAGS") or "[]")
    evidence = {
        "gate": "projection-completion",
        "schema": "traceary.projection-completion-evidence.v1",
        "traceary_version": os.environ.get("GATE_TRACEARY_VERSION") or "",
        "note": os.environ.get("GATE_NOTE") or "",
        "db_path": os.environ.get("GATE_DB_PATH") or "",
        "db_bytes_before": json.loads(os.environ.get("GATE_DB_BYTES_BEFORE") or "null"),
        "db_bytes_after": None,
        "preflight": json.loads(os.environ.get("GATE_PREFLIGHT") or "{}"),
        "budget": {"index_family_bytes": budget_bytes, "flags": flags},
        "started_at": os.environ.get("GATE_STARTED_AT") or now_rfc3339(),
        "finished_at": None,
        "wall_seconds": None,
        "source": {"max_sequence": None, "rows": None},
        "iterations": [],
        "transitions": [],
        "final_state_row": None,
        "final_lifecycle_row": None,
        "lifecycle_rows": [],
        "family_bytes": empty_family(TARGET),
        "family_bytes_baseline": {"total": None, "by_table": {}},
        "doctor": empty_doctor(),
        "result": None,
        "failure": {"state": "", "failure_class": "", "reason": ""},
        "_loop_complete": False,
        "_prev_state_phase": None,
    }
    dump_atomic(path, evidence)

def cmd_add_iteration(path):
    ev = load(path)
    payload = json.loads(sys.argv[3])
    n = payload["n"]
    state_after = payload.get("state") or {}
    iteration = {
        "n": n,
        "result_kind": payload.get("result_kind"),
        "generation_id": payload.get("generation_id") or (state_after.get("generation_id") if state_after else None),
        "state_after": state_after.get("state") if state_after else None,
        "phase_after": state_after.get("phase") if state_after else None,
        "high_water_after": state_after.get("high_water") if state_after else None,
        "checkpoint_after": state_after.get("checkpoint") if state_after else None,
        "db_bytes_after": payload.get("db_bytes_after"),
        "disk_free_after_bytes": payload.get("disk_free_after_bytes"),
        "elapsed_ms": payload.get("elapsed_ms"),
    }
    if payload.get("result_kind") == "run":
        iteration["stop_reason"] = payload.get("stop_reason")
        iteration["batches"] = payload.get("batches")
        iteration["progress"] = payload.get("progress")
    ev["iterations"].append(iteration)
    pair = None
    if state_after:
        pair = [state_after.get("state"), state_after.get("phase")]
    if pair is not None and pair != ev.get("_prev_state_phase"):
        ev["transitions"].append({
            "at": now_rfc3339(),
            "iteration": n,
            "state": pair[0],
            "phase": pair[1],
        })
        ev["_prev_state_phase"] = pair
    ev["final_state_row"] = state_after
    ev["db_bytes_after"] = payload.get("db_bytes_after")
    dump_atomic(path, ev)

def cmd_set_failure(path):
    ev = load(path)
    ev["failure"]["reason"] = sys.argv[3]
    dump_atomic(path, ev)

def cmd_mark_loop_complete(path):
    ev = load(path)
    ev["_loop_complete"] = True
    dump_atomic(path, ev)

def cmd_finalize(path):
    ev = load(path)
    extra = json.loads(sys.argv[3])
    started = datetime.fromisoformat(ev["started_at"].replace("Z", "+00:00"))
    finished_at = now_rfc3339()
    finished = datetime.fromisoformat(finished_at.replace("Z", "+00:00"))
    ev["finished_at"] = finished_at
    ev["wall_seconds"] = int((finished - started).total_seconds())
    if extra.get("source") is not None:
        ev["source"] = extra["source"]
    if extra.get("lifecycle_rows") is not None:
        ev["lifecycle_rows"] = extra["lifecycle_rows"]
    state_row = extra.get("final_state_row")
    if state_row is not None:
        ev["final_state_row"] = state_row
    ev["final_lifecycle_row"] = lifecycle_for(ev.get("final_state_row"), ev.get("lifecycle_rows") or [])
    if extra.get("family_bytes") is not None:
        ev["family_bytes"] = normalize_family(extra["family_bytes"], TARGET)
        if extra.get("database_btree_total") is not None:
            ev["family_bytes"]["database_btree_total"] = extra["database_btree_total"]
        if extra.get("measured_seconds") is not None:
            ev["family_bytes"]["measured_seconds"] = extra["measured_seconds"]
    if extra.get("family_bytes_baseline") is not None:
        ev["family_bytes_baseline"] = extra["family_bytes_baseline"]
    if extra.get("doctor") is not None:
        ev["doctor"] = extract_doctor(extra["doctor"]) if "checks" in (extra["doctor"] or {}) else extra["doctor"]
    if extra.get("db_bytes_after") is not None:
        ev["db_bytes_after"] = extra["db_bytes_after"]
    loop_complete = bool(ev.get("_loop_complete"))
    result, failure = compute_verdict(ev, loop_complete)
    ev["result"] = result
    ev["failure"] = failure
    ev.pop("_loop_complete", None)
    ev.pop("_prev_state_phase", None)
    dump_atomic(path, ev)
    family_total = (ev.get("family_bytes") or {}).get("total")
    json.dump({
        "result": result,
        "state": failure.get("state") or "",
        "failure_class": failure.get("failure_class") or "",
        "reason": failure.get("reason") or "",
        "family_total": family_total,
        "wall_seconds": ev.get("wall_seconds"),
    }, sys.stdout)

def cmd_extract_doctor():
    report = json.loads(sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else "{}")
    json.dump(extract_doctor(report), sys.stdout)

def cmd_row0():
    payload = json.loads(sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else "null")
    json.dump(row0(payload), sys.stdout)

def cmd_list():
    payload = json.loads(sys.argv[2] if len(sys.argv) > 2 and sys.argv[2] else "null")
    json.dump(as_list(payload), sys.stdout)

def cmd_parse_rebuild():
    raw = sys.argv[2] if len(sys.argv) > 2 else ""
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError:
        json.dump({"result_kind": None}, sys.stdout)
        return
    if not isinstance(payload, dict):
        json.dump({"result_kind": None}, sys.stdout)
        return
    kind = payload.get("result_kind")
    out = {"result_kind": kind}
    if kind == "generation":
        out["generation_id"] = payload.get("generation_id")
    elif kind == "run":
        out["stop_reason"] = payload.get("stop_reason")
        out["batches"] = payload.get("batches")
        out["progress"] = payload.get("progress")
        out["generation_id"] = None
        progress = payload.get("progress") or {}
        if isinstance(progress, dict):
            out["generation_id"] = progress.get("generation_id")
        if payload.get("elapsed_milliseconds") is not None:
            out["elapsed_ms"] = payload.get("elapsed_milliseconds")
    json.dump(out, sys.stdout)

cmd = sys.argv[1]
if cmd == "init":
    cmd_init(sys.argv[2])
elif cmd == "add_iteration":
    cmd_add_iteration(sys.argv[2])
elif cmd == "set_failure":
    cmd_set_failure(sys.argv[2])
elif cmd == "mark_loop_complete":
    cmd_mark_loop_complete(sys.argv[2])
elif cmd == "finalize":
    cmd_finalize(sys.argv[2])
elif cmd == "extract_doctor":
    cmd_extract_doctor()
elif cmd == "row0":
    cmd_row0()
elif cmd == "list":
    cmd_list()
elif cmd == "parse_rebuild":
    cmd_parse_rebuild()
else:
    sys.stderr.write("unknown python_gate command: %s\n" % cmd)
    sys.exit(2)
PY
}

file_bytes() {
  local path="$1"
  if [[ -f "${path}" ]]; then
    python3 -c 'import os,sys; print(os.path.getsize(sys.argv[1]))' "${path}"
  else
    printf '0'
  fi
}

read_db_bytes() {
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    printf 'null'
    return
  fi
  local total wal shm
  total="$(file_bytes "${DB_PATH}")"
  wal="$(file_bytes "${DB_PATH}-wal")"
  shm="$(file_bytes "${DB_PATH}-shm")"
  python3 -c 'import sys; print(int(sys.argv[1])+int(sys.argv[2])+int(sys.argv[3]))' "${total}" "${wal}" "${shm}"
}

read_free_bytes() {
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    printf 'null'
    return
  fi
  python3 -c 'import os, shutil, sys
path = sys.argv[1]
directory = path if os.path.isdir(path) else os.path.dirname(path)
print(shutil.disk_usage(directory).free)
' "${DB_PATH}"
}

preflight() {
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    PREFLIGHT_JSON="$(python3 -c 'import json,sys
print(json.dumps({
  "copy_bytes": None,
  "disk_free_before_bytes": None,
  "min_free_ratio": float(sys.argv[1]),
  "free_ratio_before": None,
  "sqlite3": None,
  "dbstat_available": None,
}))' "${MIN_FREE_RATIO}")"
    return
  fi
  command -v "${SQLITE3_BIN}" >/dev/null 2>&1 || {
    echo "error: sqlite3 at ${SQLITE3_BIN} not found" >&2
    exit 64
  }
  if ! "${SQLITE3_BIN}" -json :memory: 'select 1 as a' >/dev/null 2>&1; then
    echo "error: sqlite3 at ${SQLITE3_BIN} does not support -json" >&2
    exit 64
  fi
  if ! "${SQLITE3_BIN}" -readonly "${DB_PATH}" 'SELECT 1 FROM dbstat LIMIT 1' >/dev/null 2>&1; then
    echo "error: sqlite3 at ${SQLITE3_BIN} has no dbstat module; install one that does (brew install sqlite) and pass --sqlite3 PATH" >&2
    exit 64
  fi
  local copy_bytes free_bytes
  copy_bytes="$(read_db_bytes)"
  free_bytes="$(read_free_bytes)"
  python3 -c 'import sys
copy_bytes=float(sys.argv[1]); free=float(sys.argv[2]); ratio=float(sys.argv[3])
needed=ratio*copy_bytes
if free < needed:
    sys.stderr.write("error: free bytes %d < --min-free-ratio %s x copy size %d (need %.0f)\n" % (int(free), sys.argv[3], int(copy_bytes), needed))
    sys.exit(64)
' "${copy_bytes}" "${free_bytes}" "${MIN_FREE_RATIO}" || exit 64
  PREFLIGHT_JSON="$(python3 -c 'import json,sys
copy_bytes=int(sys.argv[1]); free=int(sys.argv[2]); ratio=float(sys.argv[3])
print(json.dumps({
  "copy_bytes": copy_bytes,
  "disk_free_before_bytes": free,
  "min_free_ratio": ratio,
  "free_ratio_before": (float(free)/float(copy_bytes)) if copy_bytes else None,
  "sqlite3": sys.argv[4],
  "dbstat_available": True,
}))' "${copy_bytes}" "${free_bytes}" "${MIN_FREE_RATIO}" "${SQLITE3_BIN}")"
  COPY_BYTES="${copy_bytes}"
}

sqlite_json() {
  local sql="$1"
  "${SQLITE3_BIN}" -readonly -json "${DB_PATH}" "${sql}"
}

read_state_row() {
  local n="$1"
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    local f="${FIXTURE_DIR}/state.${n}.json"
    if [[ ! -f "${f}" ]]; then
      printf 'null'
      return
    fi
    python_gate row0 "$(cat "${f}")"
    return
  fi
  python_gate row0 "$(sqlite_json "SELECT state,phase,generation_id,COALESCE(active_generation_id,'') AS active_generation_id,config_hash,high_water,checkpoint,failure_class,origin,index_family_within_budget,cutover_family_bytes_before,cutover_family_bytes_after,updated_at FROM search_projection_state WHERE singleton=1")"
}

read_lifecycle_rows() {
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    python_gate list "$(cat "${FIXTURE_DIR}/lifecycle.json")"
    return
  fi
  python_gate list "$(sqlite_json "SELECT * FROM search_projection_generation_lifecycle ORDER BY rowid")"
}

read_source_totals() {
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    printf '{"max_sequence":null,"rows":null}'
    return
  fi
  python_gate row0 "$(sqlite_json "SELECT COALESCE(MAX(sequence),0) AS max_sequence, COUNT(*) AS rows FROM search_projection_source_sequence")"
}

read_family_bytes() {
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    cat "${FIXTURE_DIR}/family.json"
    return
  fi
  local objects btree
  objects="$(sqlite_json "SELECT s.tbl_name AS tbl, s.name AS object, s.type AS type, SUM(d.pgsize) AS bytes FROM dbstat('main',1) d JOIN sqlite_schema s ON s.name = d.name WHERE s.name GLOB 'search_projection_*' OR s.tbl_name GLOB 'search_projection_*' OR s.name GLOB 'literal_search_*' OR s.tbl_name GLOB 'literal_search_*' GROUP BY 1,2,3 ORDER BY 4 DESC")"
  btree="$(python_gate row0 "$(sqlite_json "SELECT SUM(pgsize) AS database_btree_total FROM dbstat('main',1)")")"
  python3 -c 'import json,sys
objects=json.loads(sys.argv[1])
btree=json.loads(sys.argv[2] or "null")
print(json.dumps({"objects": objects, "database_btree_total": (btree or {}).get("database_btree_total")}))
' "${objects}" "${btree}"
}

read_doctor() {
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    cat "${FIXTURE_DIR}/doctor.json"
    return
  fi
  local rc=0 out
  set +e
  out="$(env -u TRACEARY_RUNTIME_MODE -u TRACEARY_RUNTIME_SESSION_ID -u TRACEARY_WORKSPACE TRACEARY_NO_AUDIT=1 TRACEARY_DB_PATH="${DB_PATH}" "${TRACEARY_BIN}" doctor --json 2>/dev/null)"
  rc=$?
  set -e
  if [[ "${rc}" -ne 0 ]]; then
    printf '{"checks":[]}\n'
    return
  fi
  printf '%s\n' "${out}"
}

run_rebuild_once() {
  local n="$1"
  REBUILD_RC=0
  REBUILD_STDOUT=""
  REBUILD_STDERR=""
  if [[ "${TEST_MODE}" -eq 1 ]]; then
    local f="${FIXTURE_DIR}/rebuild.${n}.json"
    if [[ ! -f "${f}" ]]; then
      REBUILD_RC=1
      REBUILD_STDERR="missing fixture ${f}"
      return
    fi
    REBUILD_STDOUT="$(cat "${f}")"
    return
  fi
  local err
  err="$(mktemp "${TMPDIR:-/tmp}/traceary-proj-err.XXXXXX")"
  local args=(store compact --projection-rebuild)
  if [[ -n "${INDEX_FAMILY_BYTES}" ]]; then
    args+=(--index-family-bytes "${INDEX_FAMILY_BYTES}")
  fi
  set +e
  REBUILD_STDOUT="$(env -u TRACEARY_RUNTIME_MODE -u TRACEARY_RUNTIME_SESSION_ID -u TRACEARY_WORKSPACE TRACEARY_NO_AUDIT=1 TRACEARY_DB_PATH="${DB_PATH}" "${TRACEARY_BIN}" "${args[@]}" 2>"${err}")"
  REBUILD_RC=$?
  set -e
  REBUILD_STDERR="$(python3 -c 'import sys
p=sys.argv[1]
try:
    data=open(p,"rb").read()
except OSError:
    data=b""
sys.stdout.write(data[-2048:].decode("utf-8","replace"))
' "${err}")"
  rm -f "${err}"
}

print_fail() {
  local state="$1" klass="$2" reason="$3"
  echo "FAIL: projection rebuild did not complete (${state}/${klass}): ${reason}" >&2
}

print_pass() {
  local wall="$1" family="$2"
  echo "PASS: projection rebuild reached complete in ${wall}s (family ${family} vs target ${DEFAULT_FAMILY_TARGET})"
}

finalize_and_exit() {
  local extra="$1"
  local summary result state klass reason family wall
  summary="$(python_gate finalize "${OUT_PATH}" "${extra}")"
  result="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["result"])' "${summary}")"
  state="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("state") or "")' "${summary}")"
  klass="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("failure_class") or "")' "${summary}")"
  reason="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("reason") or "")' "${summary}")"
  family="$(python3 -c 'import json,sys; v=json.loads(sys.argv[1]).get("family_total"); print("" if v is None else v)' "${summary}")"
  wall="$(python3 -c 'import json,sys; v=json.loads(sys.argv[1]).get("wall_seconds"); print("" if v is None else v)' "${summary}")"
  if [[ "${result}" == "PASS" ]]; then
    print_pass "${wall}" "${family}"
    exit 0
  fi
  print_fail "${state}" "${klass}" "${reason}"
  exit 1
}

fail_now() {
  local reason="$1"
  python_gate set_failure "${OUT_PATH}" "${reason}"
  local family_raw btree measured source lifecycle doctor db_after
  family_raw="$(read_family_bytes || true)"
  source="$(read_source_totals || true)"
  lifecycle="$(read_lifecycle_rows || true)"
  doctor="$(read_doctor || true)"
  db_after="$(read_db_bytes || true)"
  local extra
  extra="$(python3 -c 'import json,sys
family_raw=sys.argv[1]
source=json.loads(sys.argv[2] or "null")
lifecycle=json.loads(sys.argv[3] or "[]")
doctor=json.loads(sys.argv[4] or "{}")
db_after=json.loads(sys.argv[5] or "null")
payload={"source": source, "lifecycle_rows": lifecycle, "doctor": doctor, "db_bytes_after": db_after}
try:
    parsed=json.loads(family_raw)
except Exception:
    parsed=None
if isinstance(parsed, dict) and "objects" in parsed:
    payload["family_bytes"]=parsed.get("objects")
    payload["database_btree_total"]=parsed.get("database_btree_total")
else:
    payload["family_bytes"]=parsed
print(json.dumps(payload))
' "${family_raw}" "${source}" "${lifecycle}" "${doctor}" "${db_after}")"
  finalize_and_exit "${extra}"
}

refuse_live_path

if [[ "${TEST_MODE}" -eq 1 ]]; then
  TRACEARY_VERSION="fixture"
else
  if ! command -v "${TRACEARY_BIN}" >/dev/null 2>&1 && [[ ! -x "${TRACEARY_BIN}" ]]; then
    echo "error: traceary binary not found: ${TRACEARY_BIN}" >&2
    exit 64
  fi
  TRACEARY_VERSION="$("${TRACEARY_BIN}" -v 2>/dev/null || true)"
fi

COPY_BYTES=""
preflight

STARTED_AT="$(python3 -c 'from datetime import datetime, timezone; print(datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00","Z"))')"
DB_BYTES_BEFORE="$(read_db_bytes)"
BUDGET_BYTES="${INDEX_FAMILY_BYTES:-${DEFAULT_FAMILY_TARGET}}"
if [[ -n "${INDEX_FAMILY_BYTES}" ]]; then
  BUDGET_FLAGS="$(printf '["--index-family-bytes","%s"]' "${INDEX_FAMILY_BYTES}")"
else
  BUDGET_FLAGS='[]'
fi

export GATE_TRACEARY_VERSION="${TRACEARY_VERSION}"
export GATE_NOTE="${NOTE}"
export GATE_DB_PATH="${DB_PATH}"
export GATE_DB_BYTES_BEFORE="${DB_BYTES_BEFORE}"
export GATE_PREFLIGHT="${PREFLIGHT_JSON}"
export GATE_BUDGET_BYTES="${BUDGET_BYTES}"
export GATE_BUDGET_FLAGS="${BUDGET_FLAGS}"
export GATE_STARTED_AT="${STARTED_AT}"

python_gate init "${OUT_PATH}"

START_EPOCH="$(python3 -c 'import time; print(int(time.time()))')"
n=0
generation_restarts=0
LOOP_COMPLETE=0

while true; do
  n=$((n + 1))
  now_epoch="$(python3 -c 'import time; print(int(time.time()))')"
  elapsed=$((now_epoch - START_EPOCH))
  if [[ "${n}" -gt "${MAX_ITERATIONS}" ]]; then
    fail_now "max_iterations: ${MAX_ITERATIONS} calls without completion"
  fi
  if [[ "${elapsed}" -gt "${MAX_WALL}" ]]; then
    fail_now "max_wall: ${elapsed}s elapsed without completion"
  fi
  if [[ "${TEST_MODE}" -ne 1 ]]; then
    free_now="$(read_free_bytes)"
    python3 -c 'import sys
free=int(sys.argv[1]); copy=int(sys.argv[2])
sys.exit(0 if free >= copy * 0.10 else 1)
' "${free_now}" "${COPY_BYTES}" || fail_now "disk_full: ${free_now} free during iteration ${n}"
  fi

  iter_start_ms="$(python3 -c 'import time; print(int(time.time()*1000))')"
  run_rebuild_once "${n}"
  iter_elapsed_ms="$(python3 -c 'import time,sys; print(int(time.time()*1000)-int(sys.argv[1]))' "${iter_start_ms}")"
  if [[ "${REBUILD_RC}" -ne 0 ]]; then
    fail_now "rebuild_exit_${REBUILD_RC} at iteration ${n}: ${REBUILD_STDERR}"
  fi
  parsed="$(python_gate parse_rebuild "${REBUILD_STDOUT}")"
  kind="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("result_kind") or "")' "${parsed}")"
  case "${kind}" in
    generation)
      generation_restarts=$((generation_restarts + 1))
      if [[ "${generation_restarts}" -ge 2 ]]; then
        fail_now "generation_restart_loop: budget hash keeps changing"
      fi
      ;;
    run)
      ;;
    *)
      fail_now "unknown result_kind: ${kind}"
      ;;
  esac
  stop_reason="$(python3 -c 'import json,sys; print(json.loads(sys.argv[1]).get("stop_reason") or "")' "${parsed}")"
  state_json="$(read_state_row "${n}")"
  db_after="$(read_db_bytes)"
  free_after="$(read_free_bytes)"
  iter_payload="$(python3 -c 'import json,sys
parsed=json.loads(sys.argv[1])
state=json.loads(sys.argv[2] or "null")
payload={
  "n": int(sys.argv[3]),
  "result_kind": parsed.get("result_kind"),
  "generation_id": parsed.get("generation_id"),
  "stop_reason": parsed.get("stop_reason"),
  "batches": parsed.get("batches"),
  "progress": parsed.get("progress"),
  "state": state,
  "db_bytes_after": json.loads(sys.argv[4] or "null"),
  "disk_free_after_bytes": json.loads(sys.argv[5] or "null"),
  "elapsed_ms": parsed.get("elapsed_ms") if parsed.get("elapsed_ms") is not None else int(sys.argv[6]),
}
print(json.dumps(payload))
' "${parsed}" "${state_json}" "${n}" "${db_after}" "${free_after}" "${iter_elapsed_ms}")"
  python_gate add_iteration "${OUT_PATH}" "${iter_payload}"
  if [[ "${kind}" == "run" && "${stop_reason}" == "complete" ]]; then
    LOOP_COMPLETE=1
    python_gate mark_loop_complete "${OUT_PATH}"
    break
  fi
done

family_raw="$(read_family_bytes)"
source="$(read_source_totals)"
lifecycle="$(read_lifecycle_rows)"
doctor="$(read_doctor)"
db_after="$(read_db_bytes)"
extra="$(python3 -c 'import json,sys
family_raw=json.loads(sys.argv[1] or "null")
source=json.loads(sys.argv[2] or "null")
lifecycle=json.loads(sys.argv[3] or "[]")
doctor=json.loads(sys.argv[4] or "{}")
db_after=json.loads(sys.argv[5] or "null")
payload={"source": source, "lifecycle_rows": lifecycle, "doctor": doctor, "db_bytes_after": db_after}
if isinstance(family_raw, dict) and "objects" in family_raw:
    payload["family_bytes"]=family_raw.get("objects")
    payload["database_btree_total"]=family_raw.get("database_btree_total")
else:
    payload["family_bytes"]=family_raw
print(json.dumps(payload))
' "${family_raw}" "${source}" "${lifecycle}" "${doctor}" "${db_after}")"
finalize_and_exit "${extra}"
