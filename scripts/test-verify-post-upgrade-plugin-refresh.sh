#!/usr/bin/env bash
# Behavior tests for the body-free post-upgrade plugin refresh gate.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY="${ROOT_DIR}/scripts/verify-post-upgrade-plugin-refresh.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-plugin-refresh-test.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT
HOSTS=(claude codex gemini antigravity grok kimi)

check_name_for() {
  if [[ "$1" == grok ]]; then
    printf '%s\n' 'grok-plugin'
  else
    printf '%s-plugin-version\n' "$1"
  fi
}

write_report() {
  local host="$1" status="$2"
  printf '{"checks":[{"name":"%s","status":"%s","message":"must not be parsed"}]}\n' "$(check_name_for "${host}")" "${status}" >"${TMP_DIR}/${host}.json"
}

for host in "${HOSTS[@]}"; do write_report "${host}" pass; done
args=()
for host in "${HOSTS[@]}"; do args+=(--doctor-json "${host}=${TMP_DIR}/${host}.json"); done
run_fixture() { TRACEARY_PLUGIN_REFRESH_TEST_MODE=1 "${VERIFY}" "$@"; }

if "${VERIFY}" "${args[@]}" >/dev/null 2>&1; then
  echo 'error: doctor JSON fixture mode unexpectedly ran outside its test guard' >&2
  exit 1
fi
echo 'ok: restricts doctor JSON artifacts to fixture tests'

run_fixture "${args[@]}" >/dev/null
echo 'ok: accepts PASS for every host without reading report messages'

write_report grok warn
if run_fixture "${args[@]}" >/dev/null 2>&1; then
  echo 'error: stale installed package unexpectedly passed' >&2
  exit 1
fi
echo 'ok: rejects a stale installed package'

skipped_args=(--skip 'grok=intentionally not installed on this release-QA machine')
for host in "${HOSTS[@]}"; do skipped_args+=(--doctor-json "${host}=${TMP_DIR}/${host}.json"); done
run_fixture "${skipped_args[@]}" >/dev/null
echo 'ok: explicit unused-host skip is accepted'

for host in "${HOSTS[@]}"; do write_report "${host}" skip; done
if run_fixture "${args[@]}" >/dev/null 2>&1; then
  echo 'error: skip-only reports unexpectedly passed' >&2
  exit 1
fi
echo 'ok: rejects all-skip reports'

all_explicit_skips=()
for host in "${HOSTS[@]}"; do all_explicit_skips+=(--skip "${host}=intentionally unused in this fixture"); done
if run_fixture "${all_explicit_skips[@]}" >/dev/null 2>&1; then
  echo 'error: all explicit skips unexpectedly passed without an actual version check' >&2
  exit 1
fi
echo 'ok: rejects all explicit skips without an actual pass'

write_report claude skip
for host in codex gemini antigravity grok kimi; do write_report "${host}" pass; done
if run_fixture "${args[@]}" >/dev/null 2>&1; then
  echo 'error: non-Antigravity skip unexpectedly passed' >&2
  exit 1
fi
echo 'ok: rejects a non-Antigravity skip'

for host in "${HOSTS[@]}"; do write_report "${host}" pass; done
write_report antigravity skip
if run_fixture "${args[@]}" >/dev/null 2>&1; then
  echo 'error: Antigravity skip-only report unexpectedly passed' >&2
  exit 1
fi
echo 'ok: rejects an Antigravity skip-only report'

printf '{"checks":[{"name":"antigravity-plugin-version","status":"pass"},{"name":"antigravity-plugin-version","status":"skip"}]}' >"${TMP_DIR}/antigravity.json"
run_fixture "${args[@]}" >/dev/null
echo 'ok: accepts Antigravity pass plus incomplete-twin skip'

printf '{"checks":[{"name":"grok-plugin-version","status":"pass","message":"must not be parsed"}]}\n' >"${TMP_DIR}/grok.json"
if run_fixture "${args[@]}" >/dev/null 2>&1; then
  echo 'error: legacy Grok check unexpectedly passed' >&2
  exit 1
fi
echo 'ok: rejects a legacy grok-plugin-version report'
