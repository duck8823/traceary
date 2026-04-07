#!/bin/bash

set -euo pipefail

traceary_read_hook_input() {
  if [[ -n "${TRACEARY_HOOK_INPUT:-}" ]]; then
    export TRACEARY_HOOK_INPUT
    return 0
  fi

  TRACEARY_HOOK_INPUT="$(cat 2>/dev/null || true)"
  export TRACEARY_HOOK_INPUT
}

traceary_json_get() {
  local path="${1:-}"
  local default_value="${2:-}"

  python3 - "$path" "$default_value" <<'PY'
import json
import os
import sys

path = sys.argv[1]
default_value = sys.argv[2]
raw = os.environ.get("TRACEARY_HOOK_INPUT", "")

if not raw.strip():
    sys.stdout.write(default_value)
    raise SystemExit(0)

try:
    current = json.loads(raw)
except json.JSONDecodeError:
    sys.stdout.write(default_value)
    raise SystemExit(0)

for part in path.split("."):
    if not part:
        continue
    if isinstance(current, dict) and part in current:
        current = current[part]
        continue

    sys.stdout.write(default_value)
    raise SystemExit(0)

if current is None:
    sys.stdout.write(default_value)
elif isinstance(current, (dict, list)):
    sys.stdout.write(json.dumps(current, ensure_ascii=False, separators=(",", ":"), sort_keys=True))
else:
    sys.stdout.write(str(current))
PY
}

traceary_build_failure_output() {
  python3 <<'PY'
import json
import os
import sys

raw = os.environ.get("TRACEARY_HOOK_INPUT", "")
if not raw.strip():
    raise SystemExit(0)

try:
    payload = json.loads(raw)
except json.JSONDecodeError:
    raise SystemExit(0)

result = {}
error = payload.get("error")
if error not in (None, ""):
    result["error"] = error
is_interrupt = payload.get("is_interrupt")
if is_interrupt is not None:
    result["is_interrupt"] = is_interrupt

if not result:
    raise SystemExit(0)

sys.stdout.write(json.dumps(result, ensure_ascii=False, separators=(",", ":"), sort_keys=True))
PY
}

traceary_normalize_git_remote() {
  local raw="${1:-}"

  python3 - "$raw" <<'PY'
import sys
from urllib.parse import urlparse

raw = sys.argv[1].strip()
if raw.endswith('.git'):
    raw = raw[:-4]
if not raw:
    raise SystemExit(0)

if raw.startswith('git@') and ':' in raw:
    host_and_path = raw[4:]
    host, path = host_and_path.split(':', 1)
    sys.stdout.write(host.lower().strip('/') + '/' + path.strip('/'))
    raise SystemExit(0)

parsed = urlparse(raw)
if parsed.hostname:
    sys.stdout.write(parsed.hostname.lower() + '/' + parsed.path.strip('/'))
    raise SystemExit(0)

sys.stdout.write(raw)
PY
}

traceary_resolve_repo() {
  local cwd="${1:-}"

  if [[ -n "${TRACEARY_REPO:-}" ]]; then
    printf '%s' "$TRACEARY_REPO"
    return 0
  fi
  if [[ -z "$cwd" ]]; then
    return 0
  fi
  if command -v git >/dev/null 2>&1; then
    local remote
    remote="$(git -C "$cwd" config --get remote.origin.url 2>/dev/null || true)"
    if [[ -n "$remote" ]]; then
      traceary_normalize_git_remote "$remote"
      return 0
    fi
  fi

  printf '%s' "$cwd"
}

traceary_resolve_bin() {
  if [[ -n "${TRACEARY_BIN:-}" ]]; then
    printf '%s' "$TRACEARY_BIN"
    return 0
  fi
  if command -v traceary >/dev/null 2>&1; then
    command -v traceary
    return 0
  fi

  return 1
}

traceary_session_state_path() {
  local client="$1"
  local state_dir="${TRACEARY_HOOK_STATE_DIR:-${HOME}/.config/traceary/hooks}"
  mkdir -p "$state_dir"
  printf '%s/%s-%s' "$state_dir" "$client" "${TRACEARY_HOOK_STATE_KEY:-${PPID:-$$}}"
}

traceary_write_state() {
  local client="$1"
  local session_id="$2"
  local state_file
  state_file="$(traceary_session_state_path "$client")"
  printf '%s' "$session_id" > "$state_file"
}

traceary_read_state() {
  local client="$1"
  local state_file
  state_file="$(traceary_session_state_path "$client")"
  if [[ ! -f "$state_file" ]]; then
    return 0
  fi

  cat "$state_file"
}

traceary_clear_state() {
  local client="$1"
  local state_file
  state_file="$(traceary_session_state_path "$client")"
  rm -f "$state_file"
}
