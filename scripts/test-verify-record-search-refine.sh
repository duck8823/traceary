#!/usr/bin/env bash
# Behavior tests for the synthetic record/search/refine gate.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY="${ROOT_DIR}/scripts/verify-record-search-refine.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-record-search-refine-test.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

# Build the binary under test so CI runners without an installed traceary
# still exercise the real gate (same pattern as
# verify-grok-plugin-clean-home.sh).
(cd "${ROOT_DIR}" && go build -o "${TMP_DIR}/traceary" .)
RUN=("${VERIFY}" --traceary "${TMP_DIR}/traceary")

if ! "${RUN[@]}" --help | grep -q 'throwaway store'; then
  echo 'error: --help does not describe the matrix' >&2
  exit 1
fi
echo 'ok: describes the matrix in --help'

output="$("${RUN[@]}")"
for line in 'PASS muse: record' 'PASS grok: record' 'PASS kimi: record' \
  'PASS search:' 'PASS refine:' 'PASS memory:' 'PASS store:' \
  'PASS: record/search/refine matrix is complete'; do
  if ! printf '%s\n' "${output}" | grep -qF "${line}"; then
    echo "error: missing expected line: ${line}" >&2
    printf '%s\n' "${output}" >&2
    exit 1
  fi
done
echo 'ok: passes the full matrix on a throwaway store'

if printf '%s\n' "${output}" | grep -q '^FAIL'; then
  echo 'error: unexpected FAIL line' >&2
  exit 1
fi
echo 'ok: reports no FAIL lines'

if "${RUN[@]}" --bogus-flag >/dev/null 2>&1; then
  echo 'error: unknown option unexpectedly accepted' >&2
  exit 1
fi
echo 'ok: rejects unknown options'
