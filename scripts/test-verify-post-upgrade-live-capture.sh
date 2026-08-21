#!/usr/bin/env bash
# Behavior tests for the body-free post-upgrade live capture gate.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY="${ROOT_DIR}/scripts/verify-post-upgrade-live-capture.sh"
REFRESH_TEST="${ROOT_DIR}/scripts/test-verify-post-upgrade-plugin-refresh.sh"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-live-capture-test.XXXXXX")"
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
  printf '{"checks":[{"name":"%s","status":"%s","message":"must not be parsed"}]}\n' "$(check_name_for "${host}")" "${status}" >"${TMP_DIR}/${host}.doctor.json"
}

# Only kind/agent labels are meaningful; message must never be parsed.
write_list() {
  local host="$1" kinds="$2" kind
  {
    printf '['
    local first=1
    for kind in ${kinds}; do
      [[ ${first} -eq 1 ]] || printf ','
      first=0
      printf '{"kind":"%s","agent":"%s","message":"must not be parsed"}' "${kind}" "${host}"
    done
    printf ']\n'
  } >"${TMP_DIR}/${host}.list.json"
}

all_reports() {
  local status="$1" host
  for host in "${HOSTS[@]}"; do write_report "${host}" "${status}"; done
}

all_lists() {
  local kinds="$1" host
  for host in "${HOSTS[@]}"; do write_list "${host}" "${kinds}"; done
}

fixture_args() {
  local host
  args=()
  for host in "${HOSTS[@]}"; do
    args+=(--doctor-json "${host}=${TMP_DIR}/${host}.doctor.json")
    args+=(--list-json "${host}=${TMP_DIR}/${host}.list.json")
  done
}

run_fixture() { TRACEARY_CAPTURE_TEST_MODE=1 "${VERIFY}" "$@"; }

# 1. Fixture inputs are rejected outside test mode.
all_reports pass
all_lists 'session_started prompt'
fixture_args
if "${VERIFY}" "${args[@]}" >/dev/null 2>&1; then
  echo 'error: list JSON fixture mode unexpectedly ran outside its test guard' >&2
  exit 1
fi
echo 'ok: restricts list JSON artifacts to fixture tests'

# 2. The v0.47.0 bug: plugin-version pass with zero captured events must fail.
all_lists ''
if run_fixture "${args[@]}" >/dev/null 2>&1; then
  echo 'error: plugin-version pass with empty capture unexpectedly passed' >&2
  exit 1
fi
echo 'ok: rejects plugin-version pass when capture kinds are missing'

# 3. session_started + prompt with no session_ended passes.
all_lists 'session_started prompt'
run_fixture "${args[@]}" >/dev/null
echo 'ok: accepts session_started and prompt without session_ended'

# 4. Identity is still required: doctor warn fails even when kinds exist.
write_report grok warn
if run_fixture "${args[@]}" >/dev/null 2>&1; then
  echo 'error: capture kinds unexpectedly passed with a failing identity gate' >&2
  exit 1
fi
echo 'ok: rejects capture when plugin-version identity fails'
write_report grok pass

# 5. Explicit unused-host skip: grok SKIP, other hosts capture, gate passes.
skip_output="$(run_fixture --skip 'grok=not installed on this machine' "${args[@]}")"
[[ "${skip_output}" == *'SKIP grok: not installed on this machine'* ]] || {
  echo 'error: expected SKIP line for grok' >&2
  exit 1
}
echo 'ok: explicit unused-host skip is accepted beside real captures'

# 6. Gemini IneligibleTierError skip is accepted and is not a silent pass.
skip_output="$(run_fixture --skip 'gemini=IneligibleTierError' "${args[@]}")"
[[ "${skip_output}" == *'SKIP gemini: IneligibleTierError'* ]] || {
  echo 'error: expected SKIP line for gemini' >&2
  exit 1
}
echo 'ok: IneligibleTierError skip is explicit, not a silent capture pass'

# 7. --require-command grok without command_executed fails; with it passes.
if run_fixture --require-command grok "${args[@]}" >/dev/null 2>&1; then
  echo 'error: missing command_executed unexpectedly passed --require-command' >&2
  exit 1
fi
echo 'ok: rejects --require-command host without command_executed'
write_list grok 'session_started prompt command_executed'
run_fixture --require-command grok "${args[@]}" >/dev/null
echo 'ok: accepts --require-command host with command_executed'

# 8. Extra kinds (session_ended, note, transcript) do not matter.
all_lists 'session_started prompt session_ended note transcript'
run_fixture "${args[@]}" >/dev/null
echo 'ok: ignores extra kinds beyond the required set'

# 9. All explicit skips without an actual capture pass fail.
all_explicit_skips=()
for host in "${HOSTS[@]}"; do all_explicit_skips+=(--skip "${host}=intentionally unused in this fixture"); done
if run_fixture "${all_explicit_skips[@]}" >/dev/null 2>&1; then
  echo 'error: all explicit skips unexpectedly passed without an actual capture' >&2
  exit 1
fi
echo 'ok: rejects all explicit skips without an actual capture pass'

# 10. The plugin-version gate keeps its own contract: empty capture is still
# fine there, because the capture script is the new gate.
all_lists ''
bash "${REFRESH_TEST}" >/dev/null
echo 'ok: plugin-version gate behavior tests still pass'
