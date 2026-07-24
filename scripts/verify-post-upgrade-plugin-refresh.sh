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
  --doctor-json HOST=PATH    Read an existing doctor --json report instead of
                             invoking doctor for HOST (fixture/QA use).
  -h, --help                 Show this help.

Each unskipped host must report its <host>-plugin-version check as pass or
skip. warn/fail (including a package behind the running binary) fails. The
script reads only JSON check name/status, never messages, paths, prompts,
transcripts, command output, or database-event bodies.
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

all_hosts_skipped=true
for host in "${HOSTS[@]}"; do
  if ! skip_reason_for "${host}" >/dev/null; then
    all_hosts_skipped=false
    break
  fi
done
if [[ "${all_hosts_skipped}" == true ]]; then
  echo 'error: refusing to skip every host; verify at least one installed package' >&2
  exit 64
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-plugin-refresh.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

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
  python3 - "${host}" "${report}" <<'PY'
import json
import sys

host, report_path = sys.argv[1:]
expected = f"{host}-plugin-version"
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
invalid = [status for status in statuses if status not in {"pass", "skip"}]
if invalid:
    raise SystemExit(f"FAIL {host}: {expected} status is {','.join(str(status) for status in invalid)}; refresh the package or use an explicit unused-host skip")
print(f"PASS {host}: {expected}={','.join(statuses)}")
PY
done

echo 'PASS: post-upgrade plugin refresh contract is complete'
