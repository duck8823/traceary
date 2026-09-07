#!/usr/bin/env bash
# Behavior tests for the staged test selector (#2341).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SELECTOR="${ROOT_DIR}/scripts/test-select-staged.sh"
cd "${ROOT_DIR}"

PROBE_DOCS="docs/probe-2341-selector.md"
PROBE_PKG="application/probe2341"
PROBE_SH="scripts/probe-2341-nobehavior.sh"

cleanup() {
  for probe in "${PROBE_DOCS}" "${PROBE_PKG}/probe.go" "${PROBE_SH}"; do
    git restore --staged -- "${probe}" 2>/dev/null || true
  done
  rm -rf "${ROOT_DIR}/${PROBE_DOCS}" "${ROOT_DIR}/${PROBE_PKG}" "${ROOT_DIR}/${PROBE_SH}"
}
trap cleanup EXIT

if ! "${SELECTOR}" --help | grep -q 'staged changes'; then
  echo 'error: --help does not describe staged selection' >&2
  exit 1
fi
echo 'ok: describes staged selection in --help'

if "${SELECTOR}" --bogus-flag >/dev/null 2>&1; then
  echo 'error: unknown option unexpectedly accepted' >&2
  exit 1
fi
echo 'ok: rejects unknown options'

printf '# probe\n' > "${PROBE_DOCS}"
git add "${PROBE_DOCS}"
if [ "$("${SELECTOR}")" != "DOCS-CHECKS" ]; then
  echo 'error: docs-only change did not select DOCS-CHECKS' >&2
  exit 1
fi
echo 'ok: docs-only change selects DOCS-CHECKS'
git restore --staged "${PROBE_DOCS}"
rm -f "${PROBE_DOCS}"

mkdir -p "${PROBE_PKG}"
printf 'package probe2341\n' > "${PROBE_PKG}/probe.go"
git add "${PROBE_PKG}/probe.go"
selected="$("${SELECTOR}" 2>/dev/null)"
if ! printf '%s\n' "${selected}" | grep -qx 'github.com/duck8823/traceary/application/probe2341'; then
  echo 'error: new Go package not selected' >&2
  printf '%s\n' "${selected}" >&2
  exit 1
fi
if printf '%s\n' "${selected}" | grep -qx 'github.com/duck8823/traceary/infrastructure/sqlite'; then
  echo 'error: unrelated sqlite package selected for isolated change' >&2
  exit 1
fi
echo 'ok: new Go package selects itself without unrelated packages'
git restore --staged "${PROBE_PKG}/probe.go"
rm -rf "${PROBE_PKG}"

printf '#!/usr/bin/env bash\nexit 0\n' > "${PROBE_SH}"
git add "${PROBE_SH}"
full="$(go list ./... 2>/dev/null | sort)"
selected="$("${SELECTOR}" 2>/dev/null | sort)"
if [ "${selected}" != "${full}" ]; then
  echo 'error: unclassifiable script change did not expand to the full suite' >&2
  exit 1
fi
echo 'ok: unclassifiable change expands to the full suite with a reason'
