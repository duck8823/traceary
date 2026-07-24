#!/usr/bin/env bash
# Behavior tests for the body-free post-upgrade plugin refresh gate.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY="${ROOT_DIR}/scripts/verify-post-upgrade-plugin-refresh.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-plugin-refresh-test.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT
HOSTS=(claude codex gemini antigravity grok kimi)

write_report() {
  local host="$1" status="$2"
  printf '{"checks":[{"name":"%s-plugin-version","status":"%s","message":"must not be parsed"}]}\n' "${host}" "${status}" >"${TMP_DIR}/${host}.json"
}

for host in "${HOSTS[@]}"; do write_report "${host}" pass; done
args=()
for host in "${HOSTS[@]}"; do args+=(--doctor-json "${host}=${TMP_DIR}/${host}.json"); done
"${VERIFY}" "${args[@]}" >/dev/null
echo 'ok: accepts PASS for every host without reading report messages'

write_report grok warn
if "${VERIFY}" "${args[@]}" >/dev/null 2>&1; then
  echo 'error: stale installed package unexpectedly passed' >&2
  exit 1
fi
echo 'ok: rejects a stale installed package'

skipped_args=(--skip 'grok=intentionally not installed on this release-QA machine')
for host in "${HOSTS[@]}"; do skipped_args+=(--doctor-json "${host}=${TMP_DIR}/${host}.json"); done
"${VERIFY}" "${skipped_args[@]}" >/dev/null
echo 'ok: explicit unused-host skip is accepted'
