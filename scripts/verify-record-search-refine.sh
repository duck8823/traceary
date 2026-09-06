#!/usr/bin/env bash
# Synthetic record/search/refine verification on a throwaway store.
#
# Real-sized dogfood runs are NOT release gates (#2349): they need ~40 GiB
# transient space and fail with SQLITE_FULL on a typical maintainer machine
# (#2347). This gate replaces them for the read-side guarantee: every seeded
# host records session_started + prompt, and search / session refine / memory
# round-trip on the same synthetic store. All content is fixed synthetic
# tokens, so this gate never reads operator prompts, transcripts, or bodies.
# The store is bounded (fails above 64 MiB) and removed on exit.
set -euo pipefail

TRACEARY_BIN="${TRACEARY_BIN:-traceary}"
MAX_DB_BYTES=$((64 * 1024 * 1024))
TOKEN="rsr-synthetic-token"

usage() {
  cat <<'USAGE'
Usage: scripts/verify-record-search-refine.sh [options]

Seed a throwaway store through per-host hook entrypoints, then verify
record, search, session refine, and memory propose/search on that store.

Options:
  --traceary PATH   Traceary binary to inspect (default: traceary)
  -h, --help        Show this help.

Hosts with a synthetic hook seed pair (session-start + prompt) are verified
for record; claude, codex, gemini, and antigravity have no such pair, so
their record proof stays with scripts/verify-post-upgrade-live-capture.sh
and they report SKIP here with that reason. Search, refine, and memory are
host-independent read paths verified once against the seeded store.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --traceary) [[ $# -ge 2 ]] || { echo 'error: --traceary requires PATH' >&2; exit 64; }; TRACEARY_BIN="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown option $1" >&2; usage >&2; exit 64 ;;
  esac
done

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-record-search-refine.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT
DB="${TMP_DIR}/matrix.db"
STATE_DIR="${TMP_DIR}/state"
mkdir -p "${STATE_DIR}"
# Isolate hook spool/state from the operator store (same pattern as
# verify-post-upgrade-live-capture.sh) and seed from the throwaway dir so
# cwd-dependent workspace resolution cannot divert synthetic events.
export TRACEARY_HOOK_STATE_DIR="${STATE_DIR}"
cd "${TMP_DIR}"

seed_host() {
  local host="$1" session="$2"
  case "${host}" in
    muse)
      printf '{"session_id":"%s"}\n' "${session}" | "${TRACEARY_BIN}" hook muse session-start --db-path "${DB}" >/dev/null
      printf '{"session_id":"%s","prompt":"%s"}\n' "${session}" "${TOKEN}" | "${TRACEARY_BIN}" hook muse user-prompt-submit --db-path "${DB}" >/dev/null
      ;;
    grok)
      printf '{"sessionId":"%s"}\n' "${session}" | "${TRACEARY_BIN}" hook grok session-start --db-path "${DB}" >/dev/null
      printf '{"sessionId":"%s","prompt":"%s"}\n' "${session}" "${TOKEN}" | "${TRACEARY_BIN}" hook grok user-prompt-submit --db-path "${DB}" >/dev/null
      ;;
    kimi)
      printf '{"session_id":"%s"}\n' "${session}" | "${TRACEARY_BIN}" hook kimi session-start --db-path "${DB}" >/dev/null
      printf '{"session_id":"%s","prompt":"%s"}\n' "${session}" "${TOKEN}" | "${TRACEARY_BIN}" hook kimi user-prompt-submit --db-path "${DB}" >/dev/null
      ;;
  esac
}

# Count list events by (kind, agent) without emitting bodies.
kinds_for() {
  local agent="$1"
  "${TRACEARY_BIN}" --db-path "${DB}" list --json --limit 100 | python3 -c \
    'import json,sys; print(" ".join(sorted(set(e.get("kind","") for e in json.load(sys.stdin) if e.get("agent")==sys.argv[1]))))' "${agent}"
}

SEEDED=(muse grok kimi)
for host in "${SEEDED[@]}"; do
  seed_host "${host}" "rsr-${host}"
  kinds="$(kinds_for "${host}")"
  if printf '%s' "${kinds}" | grep -q 'session_started' && printf '%s' "${kinds}" | grep -q 'prompt'; then
    echo "PASS ${host}: record session_started prompt"
  else
    echo "FAIL ${host}: record missing kinds (got: ${kinds})" >&2
    exit 1
  fi
done
for host in claude codex gemini antigravity; do
  echo "SKIP ${host}: no synthetic hook seed pair; record is covered by scripts/verify-post-upgrade-live-capture.sh"
done

# Search must find the synthetic prompt without printing its body.
matches="$("${TRACEARY_BIN}" --db-path "${DB}" search "${TOKEN}" --json | python3 -c \
  'import json,sys; print(sum(1 for e in json.load(sys.stdin).get("events",[]) if e.get("kind")=="prompt"))')"
if [[ "${matches}" -ge 3 ]]; then
  echo "PASS search: prompt matches=${matches}"
else
  echo "FAIL search: expected >=3 prompt matches, got ${matches}" >&2
  exit 1
fi

# Refine one seeded session, then prove the refinement read path.
prompt_id="$("${TRACEARY_BIN}" --db-path "${DB}" list --json --limit 100 | python3 -c \
  'import json,sys; print(next(e["event_id"] for e in json.load(sys.stdin) if e.get("kind")=="prompt" and e.get("session_id")=="rsr-muse"))')"
outcome="$("${TRACEARY_BIN}" --db-path "${DB}" session refine rsr-muse --covers-to "${prompt_id}" --summary "${TOKEN}-refinement" --json | python3 -c \
  'import json,sys; print(json.load(sys.stdin).get("outcome",""))')"
if [[ "${outcome}" == "created" ]]; then
  echo "PASS refine: outcome=created"
else
  echo "FAIL refine: outcome=${outcome}" >&2
  exit 1
fi

# Memory propose + search round-trip in an isolated workspace scope.
if ! "${TRACEARY_BIN}" --db-path "${DB}" memory store propose --fact "${TOKEN}-fact" --type lesson --workspace rsr-test --json >/dev/null; then
  echo 'FAIL memory: propose failed' >&2
  exit 1
fi
found="$("${TRACEARY_BIN}" --db-path "${DB}" memory search "${TOKEN}" --json | python3 -c \
  'import json,sys; print(sum(1 for m in json.load(sys.stdin) if m.get("fact","").startswith("rsr-synthetic-token")) )')"
if [[ "${found}" -ge 1 ]]; then
  echo "PASS memory: propose+search found=${found}"
else
  echo 'FAIL memory: proposed fact not found' >&2
  exit 1
fi

size="$(wc -c <"${DB}")"
if [[ "${size}" -gt "${MAX_DB_BYTES}" ]]; then
  echo "FAIL store: ${size} bytes exceeds ${MAX_DB_BYTES} bound" >&2
  exit 1
fi
echo "PASS store: ${size} bytes within bound"

echo 'PASS: record/search/refine matrix is complete'
