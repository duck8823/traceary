#!/usr/bin/env bash
# Wave E2E gate (#2341): run the integration suite against one integrated
# head and bind the evidence to the wave. A wave is complete only when this
# gate passes on its head; an unrun or failed gate never counts as success.
set -euo pipefail

WAVE=""
REF="HEAD"
PROJECT_DIR=""
EVIDENCE_DIR=""
SMOKE=""

usage() {
  cat <<'USAGE'
Usage: scripts/run-wave-e2e.sh --wave ID [options]

Options:
  --wave ID           Wave identifier the evidence binds to (required)
  --ref REV           Integrated head to verify (default: HEAD)
  --project-dir DIR   Checkout to verify (default: current directory)
  --evidence-dir DIR  Where to write the evidence log (default: mktemp)
  --smoke PATH        Smoke runner (default: scripts/smoke_test_integrations.sh all)
  -h, --help          Show this help.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --wave) [ $# -ge 2 ] || { echo 'error: --wave requires ID' >&2; exit 64; }; WAVE="$2"; shift 2 ;;
    --ref) [ $# -ge 2 ] || { echo 'error: --ref requires REV' >&2; exit 64; }; REF="$2"; shift 2 ;;
    --project-dir) [ $# -ge 2 ] || { echo 'error: --project-dir requires DIR' >&2; exit 64; }; PROJECT_DIR="$2"; shift 2 ;;
    --evidence-dir) [ $# -ge 2 ] || { echo 'error: --evidence-dir requires DIR' >&2; exit 64; }; EVIDENCE_DIR="$2"; shift 2 ;;
    --smoke) [ $# -ge 2 ] || { echo 'error: --smoke requires PATH' >&2; exit 64; }; SMOKE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown option $1" >&2; usage >&2; exit 64 ;;
  esac
done

if [ -z "${WAVE}" ]; then
  echo 'error: --wave ID is required' >&2
  usage >&2
  exit 64
fi
case "${WAVE}" in
  *[!A-Za-z0-9._-]*)
    echo 'error: --wave ID may only contain letters, digits, dot, underscore, hyphen' >&2
    exit 64
    ;;
esac

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -z "${PROJECT_DIR}" ]; then
  PROJECT_DIR="$(pwd)"
fi
if [ -z "${SMOKE}" ]; then
  SMOKE="${ROOT_DIR}/scripts/smoke_test_integrations.sh all"
fi
if [ -z "${EVIDENCE_DIR}" ]; then
  EVIDENCE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-wave-e2e.XXXXXX")"
else
  mkdir -p "${EVIDENCE_DIR}"
fi

SHA="$(git -C "${PROJECT_DIR}" rev-parse "${REF}" 2>/dev/null || true)"
if [ -z "${SHA}" ]; then
  echo "error: cannot resolve ref ${REF} in ${PROJECT_DIR}" >&2
  exit 64
fi
STAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
EVIDENCE="${EVIDENCE_DIR}/wave-${WAVE}-${SHA}.log"

{
  echo "wave: ${WAVE}"
  echo "sha: ${SHA}"
  echo "ref: ${REF}"
  echo "project: ${PROJECT_DIR}"
  echo "started: ${STAMP}"
  # shellcheck disable=SC2086
  (cd "${PROJECT_DIR}" && ${SMOKE})
  echo "exit: $?"
} > "${EVIDENCE}" 2>&1 || true

EXIT_LINE="$(grep -E '^exit: ' "${EVIDENCE}" | tail -1)"
if [ "${EXIT_LINE}" = "exit: 0" ] && ! grep -qE '^FAIL' "${EVIDENCE}"; then
  echo "PASS wave ${WAVE} at ${SHA}"
  echo "evidence: ${EVIDENCE}"
  exit 0
fi
echo "FAIL wave ${WAVE} at ${SHA}" >&2
echo "evidence: ${EVIDENCE}" >&2
exit 1
