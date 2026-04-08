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

traceary_helper_command() {
  local helper_name="${1:-}"
  shift || true

  local traceary_cmd="${TRACEARY_CMD:-}"
  if [[ -z "$traceary_cmd" ]]; then
    traceary_cmd="$(traceary_resolve_bin 2>/dev/null || true)"
  fi
  if [[ -z "$traceary_cmd" ]]; then
    return 1
  fi

  TRACEARY_HOOK_INPUT="${TRACEARY_HOOK_INPUT:-}" "$traceary_cmd" hooks helper "$helper_name" "$@" 2>/dev/null
}

traceary_json_get() {
  local path="${1:-}"
  local default_value="${2:-}"

  traceary_helper_command json-get "$path" "$default_value" || printf '%s' "$default_value"
}

traceary_build_failure_output() {
  traceary_helper_command build-failure-output || true
}

traceary_normalize_git_remote() {
  local raw="${1:-}"

  traceary_helper_command normalize-git-remote "$raw" || true
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
