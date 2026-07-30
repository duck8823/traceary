#!/usr/bin/env bash
# Verify the post-upgrade plugin-version contract without reading prompt,
# transcript, command, or database-event bodies. Every host is version-aligned
# or explicitly skipped by the release-QA operator.
set -euo pipefail

TRACEARY_BIN="${TRACEARY_BIN:-traceary}"
PROJECT_DIR="${PWD}"
HOSTS=(claude codex gemini antigravity grok kimi)
SKIP_HOSTS=()
SKIP_REASONS=()
REPORT_HOSTS=()
REPORT_PATHS=()

usage() {
  cat <<'USAGE'
Usage: scripts/verify-post-upgrade-plugin-refresh.sh [options]

Run body-free plugin-version verification for Claude, Codex, Gemini legacy,
Antigravity, Grok, and Kimi after upgrading a released Traceary binary.

Options:
  --traceary PATH            Traceary binary to inspect (default: traceary)
  --project-dir PATH         Project directory passed to doctor (default: cwd)
  --skip HOST=REASON         Explicitly skip an intentionally unused host.
  --doctor-json HOST=PATH    Test-fixture input only. Requires
                             TRACEARY_PLUGIN_REFRESH_TEST_MODE=1.
  -h, --help                 Show this help.

Claude, Codex, Gemini, Grok, and Kimi require a pass result. Antigravity
requires at least one pass and permits additional skip results for its known
incomplete dual-path twin. warn/fail (including a package behind the running
binary) fails. The script reads only JSON check name/status, never messages,
paths, prompts, transcripts, command output, or database-event bodies.
USAGE
}

parse_assignment() {
  local option="$1" assignment="$2" host value
  [[ "${assignment}" == *=* ]] || { echo "error: ${option} requires HOST=VALUE" >&2; exit 64; }
  host="${assignment%%=*}"
  value="${assignment#*=}"
  case " ${HOSTS[*]} " in *" ${host} "*) ;; *) printf 'error: unknown host %s\n' "${host}" >&2; exit 64 ;; esac
  [[ -n "${value}" ]] || { echo "error: ${option} requires a non-empty value for ${host}" >&2; exit 64; }
  printf '%s\n%s\n' "${host}" "${value}"
}

add_skip() {
  SKIP_HOSTS+=("$1")
  SKIP_REASONS+=("$2")
}

skip_reason_for() {
  local host="$1" index
  for ((index = 0; index < ${#SKIP_HOSTS[@]}; index++)); do
    if [[ "${SKIP_HOSTS[index]}" == "${host}" ]]; then
      printf '%s\n' "${SKIP_REASONS[index]}"
      return 0
    fi
  done
  return 1
}

add_report() {
  REPORT_HOSTS+=("$1")
  REPORT_PATHS+=("$2")
}

report_path_for() {
  local host="$1" index
  for ((index = 0; index < ${#REPORT_HOSTS[@]}; index++)); do
    if [[ "${REPORT_HOSTS[index]}" == "${host}" ]]; then
      printf '%s\n' "${REPORT_PATHS[index]}"
      return 0
    fi
  done
  return 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --traceary) [[ $# -ge 2 ]] || { echo 'error: --traceary requires PATH' >&2; exit 64; }; TRACEARY_BIN="$2"; shift 2 ;;
    --project-dir) [[ $# -ge 2 ]] || { echo 'error: --project-dir requires PATH' >&2; exit 64; }; PROJECT_DIR="$2"; shift 2 ;;
    --skip|--doctor-json)
      [[ $# -ge 2 ]] || { echo "error: $1 requires HOST=VALUE" >&2; exit 64; }
      if [[ "$1" == --doctor-json && "${TRACEARY_PLUGIN_REFRESH_TEST_MODE:-}" != 1 ]]; then
        echo 'error: --doctor-json is restricted to fixture tests; release QA must run live doctor commands' >&2
        exit 64
      fi
      assignment="$(parse_assignment "$1" "$2")"
      host="${assignment%%$'\n'*}"
      value="${assignment#*$'\n'}"
      if [[ "$1" == --skip ]]; then
        add_skip "${host}" "${value}"
      else
        add_report "${host}" "${value}"
      fi
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown option $1" >&2; usage >&2; exit 64 ;;
  esac
done

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-plugin-refresh.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT
actual_passes=0

for host in "${HOSTS[@]}"; do
  if skip_reason="$(skip_reason_for "${host}")"; then
    printf 'SKIP %s: %s\n' "${host}" "${skip_reason}"
    continue
  fi
  if report="$(report_path_for "${host}")"; then
    :
  else
    report="${TMP_DIR}/${host}.json"
  fi
  if ! report_path_for "${host}" >/dev/null && ! "${TRACEARY_BIN}" doctor --client "${host}" --project-dir "${PROJECT_DIR}" --json --warnings-ok >"${report}"; then
    echo "FAIL ${host}: doctor command failed; use --skip ${host}=REASON only when this host is intentionally unused" >&2
    exit 1
  fi
  pass_marker="${TMP_DIR}/${host}.pass"
  python3 - "${host}" "${report}" "${pass_marker}" <<'PY'
import json
import sys

host, report_path, pass_marker = sys.argv[1:]
expected = "grok-plugin" if host == "grok" else f"{host}-plugin-version"
try:
    with open(report_path, encoding="utf-8") as source:
        report = json.load(source)
except (OSError, json.JSONDecodeError) as exc:
    raise SystemExit(f"FAIL {host}: cannot read doctor JSON: {exc}")

# Deliberately retain only public name/status. Doctor messages can contain
# local paths, so this gate never reads or emits them.
statuses = [check.get("status") for check in report.get("checks", []) if check.get("name") == expected]
if not statuses:
    raise SystemExit(f"FAIL {host}: no {expected} check; use --skip {host}=REASON only when the host is intentionally unused")
if host == "antigravity":
    invalid = [status for status in statuses if status not in {"pass", "skip"}]
    if "pass" not in statuses:
        raise SystemExit(f"FAIL {host}: {expected} must include pass; skip is allowed only beside a healthy dual-path copy")
else:
    invalid = [status for status in statuses if status != "pass"]
if invalid:
    raise SystemExit(f"FAIL {host}: {expected} status is {','.join(str(status) for status in invalid)}; refresh the package or use an explicit unused-host skip")
with open(pass_marker, "w", encoding="utf-8") as marker:
    marker.write("1\n" if "pass" in statuses else "0\n")
print(f"PASS {host}: {expected}={','.join(statuses)}")
PY
  if [[ "$(cat "${pass_marker}")" == 1 ]]; then
    actual_passes=$((actual_passes + 1))
  fi
done

if [[ ${actual_passes} -eq 0 ]]; then
  echo 'FAIL: no unskipped host reported an actual plugin-version pass' >&2
  exit 1
fi

echo 'PASS: post-upgrade plugin refresh contract is complete'
