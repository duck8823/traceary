#!/usr/bin/env bash
# Behavior tests for the wave E2E gate (#2341).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WAVE="${ROOT_DIR}/scripts/run-wave-e2e.sh"
cd "${ROOT_DIR}"

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-wave-e2e-test.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

PASS_RUNNER="${TMP_DIR}/smoke-pass.sh"
FAIL_RUNNER="${TMP_DIR}/smoke-fail.sh"
printf '#!/usr/bin/env bash\necho "PASS stub: smoke passed"\n' > "${PASS_RUNNER}"
printf '#!/usr/bin/env bash\necho "FAIL stub: capture failed"\nexit 1\n' > "${FAIL_RUNNER}"
chmod +x "${PASS_RUNNER}" "${FAIL_RUNNER}"

if ! "${WAVE}" --help | grep -q -- '--wave ID'; then
  echo 'error: --help does not describe --wave' >&2
  exit 1
fi
echo 'ok: describes --wave in --help'

if "${WAVE}" --wave 'bad/id' --smoke "${PASS_RUNNER}" >/dev/null 2>&1; then
  echo 'error: path-traversal wave ID unexpectedly accepted' >&2
  exit 1
fi
echo 'ok: rejects unsafe wave IDs'

if "${WAVE}" --smoke "${PASS_RUNNER}" >/dev/null 2>&1; then
  echo 'error: missing --wave unexpectedly accepted' >&2
  exit 1
fi
echo 'ok: requires --wave'

SHA="$(git rev-parse HEAD)"
out="$("${WAVE}" --wave probe1 --smoke "${PASS_RUNNER}" --evidence-dir "${TMP_DIR}/ev1")"
if ! printf '%s\n' "${out}" | grep -q "PASS wave probe1 at ${SHA}"; then
  echo 'error: passing smoke did not pass the wave' >&2
  printf '%s\n' "${out}" >&2
  exit 1
fi
EVIDENCE="${TMP_DIR}/ev1/wave-probe1-${SHA}.log"
if [ ! -f "${EVIDENCE}" ] || ! grep -q "sha: ${SHA}" "${EVIDENCE}"; then
  echo 'error: evidence missing or not bound to the head SHA' >&2
  exit 1
fi
echo 'ok: passing smoke completes the wave with SHA-bound evidence'

if "${WAVE}" --wave probe2 --smoke "${FAIL_RUNNER}" --evidence-dir "${TMP_DIR}/ev2" >/dev/null 2>&1; then
  echo 'error: failing smoke unexpectedly completed the wave' >&2
  exit 1
fi
echo 'ok: failing smoke fails the wave'
