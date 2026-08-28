#!/usr/bin/env bash
# Verify the post-upgrade live-capture contract without reading prompt,
# transcript, command, or database-event bodies. Plugin-version identity is
# necessary but not sufficient: every unskipped host must also record
# session_started and prompt into an isolated throwaway store.
set -euo pipefail

TRACEARY_BIN="${TRACEARY_BIN:-traceary}"
PROJECT_DIR="${PWD}"
HOSTS=(claude codex gemini antigravity grok kimi)
SKIP_HOSTS=()
SKIP_REASONS=()
DOCTOR_HOSTS=()
DOCTOR_PATHS=()
LIST_HOSTS=()
LIST_PATHS=()
REQUIRE_COMMAND_HOSTS=()
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REFRESH_GATE="${SCRIPT_DIR}/verify-post-upgrade-plugin-refresh.sh"
PROBE_TIMEOUT_SECONDS=300
PROBE_STATUS_REPORTED=70
PROBE_ENV_COMMON=(-u TRACEARY_NO_AUDIT -u TRACEARY_RUNTIME_MODE -u TRACEARY_RUNTIME_SESSION_ID -u TRACEARY_WORKSPACE)

usage() {
  cat <<'USAGE'
Usage: scripts/verify-post-upgrade-live-capture.sh [options]

Run body-free live-capture verification for Claude, Codex, Gemini legacy,
Antigravity, Grok, and Kimi after upgrading a released Traceary binary. A
plugin-version PASS alone is not capture: every unskipped host must record at
least one session_started and one prompt event into an isolated throwaway
store created by this script.

Options:
  --traceary PATH            Traceary binary to inspect (default: traceary)
  --project-dir PATH         Project directory used for the Codex probe
                             (default: cwd)
  --skip HOST=REASON         Explicitly skip a host that is intentionally unused
                             or not authenticated on this machine, or a Gemini
                             account rejected with IneligibleTierError.
  --doctor-json HOST=PATH    Test-fixture input only. Requires
                             TRACEARY_CAPTURE_TEST_MODE=1. Forwarded to the
                             plugin-version identity gate.
  --list-json HOST=PATH      Test-fixture input only. Requires
                             TRACEARY_CAPTURE_TEST_MODE=1. A `traceary list
                             --json` array; only `kind` (and optionally
                             `agent`) is read, never message or event bodies.
  --require-command HOST     Additionally require command_executed for HOST
                             (repeatable). Live mode runs a second probe that
                             executes a trivial shell command.
  -h, --help                 Show this help.

Identity is verified first by running verify-post-upgrade-plugin-refresh.sh
with the same --traceary, --project-dir, --skip, and --doctor-json arguments;
an identity failure fails this gate before any capture probe runs. Live probes
always write to a fresh mktemp store via TRACEARY_DB_PATH and `--db-path`,
never the default home store, and session_ended is never required or
synthesized. Gemini has no headless probe in this gate; skip it with an
explicit reason. Claude uses claude --print and Antigravity uses the agy
binary. The script reads only JSON check name/status and list kind counts,
never messages, paths, prompts, transcripts, command output, or
database-event bodies.
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

add_doctor() {
  DOCTOR_HOSTS+=("$1")
  DOCTOR_PATHS+=("$2")
}

add_list() {
  LIST_HOSTS+=("$1")
  LIST_PATHS+=("$2")
}

list_path_for() {
  local host="$1" index
  for ((index = 0; index < ${#LIST_HOSTS[@]}; index++)); do
    if [[ "${LIST_HOSTS[index]}" == "${host}" ]]; then
      printf '%s\n' "${LIST_PATHS[index]}"
      return 0
    fi
  done
  return 1
}

command_required_for() {
  local host="$1" index
  for ((index = 0; index < ${#REQUIRE_COMMAND_HOSTS[@]}; index++)); do
    if [[ "${REQUIRE_COMMAND_HOSTS[index]}" == "${host}" ]]; then
      return 0
    fi
  done
  return 1
}

host_binary_for() {
  case "$1" in
    antigravity) printf 'agy\n' ;;
    *) printf '%s\n' "$1" ;;
  esac
}

# timeout(1) is not present on a stock macOS; gtimeout is when coreutils is
# installed. Without either, agy still self-bounds via --print-timeout and the
# Claude probe is unbounded — documented, not fatal.
run_with_timeout() {
  local seconds="$1"
  shift
  if command -v timeout >/dev/null 2>&1; then
    timeout "${seconds}" "$@"
  elif command -v gtimeout >/dev/null 2>&1; then
    gtimeout "${seconds}" "$@"
  else
    "$@"
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --traceary) [[ $# -ge 2 ]] || { echo 'error: --traceary requires PATH' >&2; exit 64; }; TRACEARY_BIN="$2"; shift 2 ;;
    --project-dir) [[ $# -ge 2 ]] || { echo 'error: --project-dir requires PATH' >&2; exit 64; }; PROJECT_DIR="$2"; shift 2 ;;
    --require-command)
      [[ $# -ge 2 ]] || { echo 'error: --require-command requires HOST' >&2; exit 64; }
      case " ${HOSTS[*]} " in *" $2 "*) ;; *) printf 'error: unknown host %s\n' "$2" >&2; exit 64 ;; esac
      REQUIRE_COMMAND_HOSTS+=("$2")
      shift 2
      ;;
    --skip|--doctor-json|--list-json)
      [[ $# -ge 2 ]] || { echo "error: $1 requires HOST=VALUE" >&2; exit 64; }
      if [[ "$1" != --skip && "${TRACEARY_CAPTURE_TEST_MODE:-}" != 1 ]]; then
        echo "error: $1 is restricted to fixture tests; release QA must run live doctor and capture probes" >&2
        exit 64
      fi
      assignment="$(parse_assignment "$1" "$2")"
      host="${assignment%%$'\n'*}"
      value="${assignment#*$'\n'}"
      case "$1" in
        --skip) add_skip "${host}" "${value}" ;;
        --doctor-json) add_doctor "${host}" "${value}" ;;
        --list-json) add_list "${host}" "${value}" ;;
      esac
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "error: unknown option $1" >&2; usage >&2; exit 64 ;;
  esac
done

# Headless Claude stays in --permission-mode plan and Antigravity stays in
# --mode plan --sandbox, so a second turn cannot reliably execute a shell
# command without a permission prompt. Do not leave a path that reports a
# false "missing kinds: command_executed".
for required_host in "${REQUIRE_COMMAND_HOSTS[@]+"${REQUIRE_COMMAND_HOSTS[@]}"}"; do
  case "${required_host}" in
    claude|antigravity)
      echo "error: --require-command ${required_host} is not supported; the headless probe stays in plan/sandbox mode and cannot reliably execute a shell command" >&2
      exit 64
      ;;
  esac
done

# Plugin-version identity is a precondition. Reuse the sibling gate so the
# name/status contract (grok-plugin vs *-plugin-version) stays in one place.
refresh_args=(--traceary "${TRACEARY_BIN}" --project-dir "${PROJECT_DIR}")
for ((index = 0; index < ${#SKIP_HOSTS[@]}; index++)); do
  refresh_args+=(--skip "${SKIP_HOSTS[index]}=${SKIP_REASONS[index]}")
done
for ((index = 0; index < ${#DOCTOR_HOSTS[@]}; index++)); do
  refresh_args+=(--doctor-json "${DOCTOR_HOSTS[index]}=${DOCTOR_PATHS[index]}")
done
if [[ "${TRACEARY_CAPTURE_TEST_MODE:-}" == 1 ]]; then
  export TRACEARY_PLUGIN_REFRESH_TEST_MODE=1
fi
if ! "${REFRESH_GATE}" "${refresh_args[@]}"; then
  echo 'FAIL: plugin-version identity gate failed; live capture was not evaluated' >&2
  exit 1
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/traceary-live-capture.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT
capture_passes=0

probe_host() {
  local host="$1" db="$2" log="$3" err="$4" state="$5"
  case "${host}" in
    claude)
      # Body-free capture probe. --permission-mode plan keeps the probe
      # read-only. Never pass --bare (skips hooks) or any permission bypass.
      # Do not use --working-directory; cd into PROJECT_DIR instead.
      ( cd "${PROJECT_DIR}" && run_with_timeout "${PROBE_TIMEOUT_SECONDS}" \
          env "${PROBE_ENV_COMMON[@]}" \
              TRACEARY_DB_PATH="${db}" TRACEARY_HOOK_STATE_DIR="${state}" \
              claude --print --permission-mode plan \
              'Reply with the single word ok.' ) >"${log}" 2>"${err}" </dev/null || return $?
      ;;
    antigravity)
      # agy can exit 0 with empty stdout when a hook is auto-denied with no TTY
      # to prompt, so permission wording on stderr is diagnosed regardless of
      # exit status (verify-antigravity-headless-markers.sh).
      local agy_status=0
      # agy 1.1+ treats the argument after --print as the prompt
      # (docs/ai-cli: "prompt is the --print argument"). Putting --mode
      # after --print makes agy treat "--mode" as the prompt and exit 2.
      # Combined --mode plan --sandbox on agy 1.1.22 returns a reply but
      # writes no capture events; keep plan and omit sandbox.
      ( cd "${PROJECT_DIR}" && run_with_timeout "${PROBE_TIMEOUT_SECONDS}" \
          env "${PROBE_ENV_COMMON[@]}" \
              TRACEARY_DB_PATH="${db}" TRACEARY_HOOK_STATE_DIR="${state}" \
              agy --mode plan --print-timeout 120s \
              --print 'Reply with the single word ok.' ) >"${log}" 2>"${err}" </dev/null || agy_status=$?
      if grep -qi permission "${err}"; then
        echo "FAIL antigravity: scoped hook permission is absent or shadowed (permission prompt on stderr); grant the Traceary hook permission for Antigravity or use --skip antigravity=REASON only when this host is intentionally unused" >&2
        return "${PROBE_STATUS_REPORTED}"
      fi
      return "${agy_status}"
      ;;
    grok)
      TRACEARY_DB_PATH="${db}" grok --permission-mode plan --no-subagents --max-turns 1 \
        -p "Reply with the single word ok." >"${log}" 2>"${err}" </dev/null
      if command_required_for grok; then
        TRACEARY_DB_PATH="${db}" grok --no-subagents --max-turns 2 \
          -p "Run the shell command true. Then reply with the single word done." >>"${log}" 2>>"${err}" </dev/null
      fi
      ;;
    kimi)
      # No --auto / --yolo: default -p permission mode. SessionEnd is not
      # required and is never synthesized from process exit.
      TRACEARY_DB_PATH="${db}" kimi -p "Reply with the single word ok." >"${log}" 2>"${err}" </dev/null
      if command_required_for kimi; then
        TRACEARY_DB_PATH="${db}" kimi -p "Run the shell command true. Then reply with the single word done." >>"${log}" 2>>"${err}" </dev/null
      fi
      ;;
    codex)
      # codex exec requires a trusted git directory; --project-dir must be
      # one. Do not bypass with --skip-git-repo-check or -a never (#2238).
      TRACEARY_DB_PATH="${db}" codex exec -C "${PROJECT_DIR}" -s read-only \
        'Reply with the single word ok.' >"${log}" 2>"${err}" </dev/null
      ;;
  esac
}

evaluate_kinds() {
  local host="$1" list_json="$2" require_command=0
  command_required_for "${host}" && require_command=1
  python3 - "${host}" "${list_json}" "${require_command}" <<'PY'
import json
import sys

host, list_path, require_command = sys.argv[1], sys.argv[2], sys.argv[3] == "1"
try:
    with open(list_path, encoding="utf-8") as source:
        events = json.load(source)
except (OSError, json.JSONDecodeError) as exc:
    raise SystemExit(f"FAIL {host}: cannot read list JSON: {exc}")
if not isinstance(events, list):
    raise SystemExit(f"FAIL {host}: list JSON is not an array")

# Deliberately retain only public kind/agent labels. Event messages and
# bodies can contain local paths or prompt text, so this gate never reads
# or emits them.
counts = {}
for event in events:
    if not isinstance(event, dict):
        continue
    kind = event.get("kind")
    if isinstance(kind, str) and kind:
        counts[kind] = counts.get(kind, 0) + 1

required = ["session_started", "prompt"]
if require_command:
    required.append("command_executed")
missing = [kind for kind in required if counts.get(kind, 0) == 0]
if missing:
    raise SystemExit(f"FAIL {host}: missing kinds: {', '.join(missing)}")
print(f"PASS {host}: " + " ".join(f"{kind}={counts[kind]}" for kind in required))
PY
}

for host in "${HOSTS[@]}"; do
  if skip_reason="$(skip_reason_for "${host}")"; then
    printf 'SKIP %s: %s\n' "${host}" "${skip_reason}"
    continue
  fi
  if [[ "${TRACEARY_CAPTURE_TEST_MODE:-}" == 1 ]]; then
    if ! list_json="$(list_path_for "${host}")"; then
      echo "FAIL ${host}: test mode requires --list-json ${host}=PATH" >&2
      exit 1
    fi
  else
    case "${host}" in
      claude|antigravity|grok|kimi|codex) ;;
      gemini)
        echo "FAIL gemini: IneligibleTierError must not count as capture; use --skip gemini='IneligibleTierError' or another explicit reason" >&2
        exit 1
        ;;
    esac
    binary="$(host_binary_for "${host}")"
    command -v "${binary}" >/dev/null 2>&1 || {
      echo "FAIL ${host}: ${binary} binary not found; install it or use --skip ${host}=REASON only when this host is intentionally unused" >&2
      exit 1
    }
    capture_dir="${TMP_DIR}/${host}"
    mkdir -p "${capture_dir}/state"
    capture_db="${capture_dir}/traceary.db"
    # Never aim a probe at the default home store or any pre-existing file.
    if [[ "${capture_db}" == "${HOME}/.config/traceary/traceary.db" || -e "${capture_db}" ]]; then
      echo "FAIL ${host}: refusing to reuse a non-throwaway store path" >&2
      exit 1
    fi
    probe_status=0
    probe_host "${host}" "${capture_db}" "${capture_dir}/probe.log" \
      "${capture_dir}/probe.err" "${capture_dir}/state" || probe_status=$?
    case "${probe_status}" in
      0) ;;
      "${PROBE_STATUS_REPORTED}") exit 1 ;;
      124)
        echo "FAIL ${host}: headless probe timed out after ${PROBE_TIMEOUT_SECONDS}s; investigate the host CLI or use --skip ${host}=REASON only when this host is intentionally unused" >&2
        exit 1
        ;;
      *)
        echo "FAIL ${host}: headless probe failed; investigate the host CLI or use --skip ${host}=REASON only when this host is intentionally unused" >&2
        exit 1
        ;;
    esac
    list_json="${capture_dir}/list.json"
    if ! "${TRACEARY_BIN}" --db-path "${capture_db}" list --json --limit 50 >"${list_json}"; then
      echo "FAIL ${host}: traceary list against the throwaway store failed" >&2
      exit 1
    fi
  fi
  evaluate_kinds "${host}" "${list_json}"
  capture_passes=$((capture_passes + 1))
done

if [[ ${capture_passes} -eq 0 ]]; then
  echo 'FAIL: no unskipped host reported an actual live capture pass' >&2
  exit 1
fi

echo 'PASS: post-upgrade live capture contract is complete'
