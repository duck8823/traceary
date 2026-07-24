#!/usr/bin/env bash
# Contract tests for the safe Grok local-repository migration (#1538).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALLER="${ROOT_DIR}/scripts/install-grok-plugin.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-grok-installer-test.XXXXXX")"
cleanup() { rm -rf "${TMP_DIR}"; }
trap cleanup EXIT

make_fake_grok() {
  local scenario="$1"
  local bin_dir="${TMP_DIR}/${scenario}/bin"
  mkdir -p "${bin_dir}"
  cat >"${bin_dir}/grok" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_GROK_LOG}"
case "$1 ${2:-} ${3:-}" in
  'plugin validate '*) exit 0 ;;
  'plugin list --json') printf '%s\n' "${FAKE_GROK_LIST}"; exit 0 ;;
  'plugin uninstall --confirm') exit 0 ;;
  'plugin uninstall traceary-grok') exit 0 ;;
  'plugin install --trust') exit 0 ;;
  'plugin details traceary-grok') exit 0 ;;
  *) printf 'unexpected fake grok arguments: %s\n' "$*" >&2; exit 64 ;;
esac
SCRIPT
  chmod +x "${bin_dir}/grok"
  export PATH="${bin_dir}:${ORIGINAL_PATH}"
  export FAKE_GROK_LOG="${TMP_DIR}/${scenario}.log"
}

assert_contains() {
  local needle="$1" haystack="$2"
  grep -F -- "${needle}" "${haystack}" >/dev/null || {
    printf 'expected %q in %s\n' "${needle}" "${haystack}" >&2
    cat "${haystack}" >&2
    exit 1
  }
}

assert_not_contains() {
  local needle="$1" haystack="$2"
  if grep -F -- "${needle}" "${haystack}" >/dev/null; then
    printf 'did not expect %q in %s\n' "${needle}" "${haystack}" >&2
    cat "${haystack}" >&2
    exit 1
  fi
}

ORIGINAL_PATH="${PATH}"
plugin_dir="${ROOT_DIR}/integrations/grok-plugin"

# Clean homes and canonical packages install from repository#subdirectory.
make_fake_grok clean-home
export FAKE_GROK_LIST='[]'
"${INSTALLER}"
assert_contains "plugin install --trust ${ROOT_DIR}#integrations/grok-plugin" "${FAKE_GROK_LOG}"

# Invalid host inventory must fail closed before an install or uninstall.
make_fake_grok invalid-inventory
export FAKE_GROK_LIST='not-json'
if "${INSTALLER}" >"${TMP_DIR}/invalid-inventory.out" 2>"${TMP_DIR}/invalid-inventory.err"; then
  echo 'expected invalid inventory to fail closed' >&2
  exit 1
else
  status=$?
  [[ "${status}" -eq 65 ]] || { echo "expected exit 65, got ${status}" >&2; exit 1; }
fi
assert_not_contains 'plugin uninstall' "${FAKE_GROK_LOG}"
assert_not_contains 'plugin install' "${FAKE_GROK_LOG}"

make_fake_grok canonical
export FAKE_GROK_LIST='[{"name":"traceary-grok","repo_key":"grok-plugin-a","source":"/repo"}]'
"${INSTALLER}"
assert_contains 'plugin uninstall traceary-grok' "${FAKE_GROK_LOG}"
assert_contains "plugin install --trust ${ROOT_DIR}#integrations/grok-plugin" "${FAKE_GROK_LOG}"

# A previous install from the plugin subdirectory must not be removed implicitly.
make_fake_grok local-repository
export FAKE_GROK_LIST="[{\"name\":\"traceary\",\"repo_key\":\"grok-plugin-4d1bd2fe\",\"source\":\"${plugin_dir}\"}]"
if "${INSTALLER}" >"${TMP_DIR}/local-repository.out" 2>"${TMP_DIR}/local-repository.err"; then
  echo 'expected local repository identity to require explicit migration' >&2
  exit 1
else
  status=$?
  [[ "${status}" -eq 78 ]] || { echo "expected exit 78, got ${status}" >&2; exit 1; }
fi
assert_not_contains 'plugin uninstall' "${FAKE_GROK_LOG}"
assert_contains '--migrate-local-repo-identity' "${TMP_DIR}/local-repository.err"

# The explicit flag removes only the exact checkout-local identity, then uses
# the canonical source selector. An unrelated legacy traceary package remains.
make_fake_grok migrate
export FAKE_GROK_LIST="[{\"name\":\"traceary\",\"repo_key\":\"grok-plugin-4d1bd2fe\",\"source\":\"${plugin_dir}\"},{\"name\":\"traceary\",\"repo_key\":\"marketplace-traceary\",\"source\":\"duck8823/traceary\"}]"
"${INSTALLER}" --migrate-local-repo-identity
assert_contains 'plugin uninstall --confirm traceary' "${FAKE_GROK_LOG}"
assert_contains "plugin install --trust ${ROOT_DIR}#integrations/grok-plugin" "${FAKE_GROK_LOG}"

# A legacy package from a different source is never selected for migration.
make_fake_grok legacy
export FAKE_GROK_LIST='[{"name":"traceary","repo_key":"marketplace-traceary","source":"duck8823/traceary"}]'
"${INSTALLER}"
assert_not_contains 'plugin uninstall traceary' "${FAKE_GROK_LOG}"
assert_contains "plugin install --trust ${ROOT_DIR}#integrations/grok-plugin" "${FAKE_GROK_LOG}"

echo 'OK: Grok installer separates package names from local-repository identities'
