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

traceary_run_hook() {
  local preserve_stdout="${1:-0}"
  shift || true

  traceary_read_hook_input

  local traceary_cmd
  traceary_cmd="$(traceary_resolve_bin 2>/dev/null || true)"
  if [[ -z "$traceary_cmd" ]]; then
    return 0
  fi

  local command=("$traceary_cmd" "$@")
  if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
    command+=(--db-path "$TRACEARY_DB_PATH")
  fi

  if [[ "$preserve_stdout" == "1" ]]; then
    printf '%s' "${TRACEARY_HOOK_INPUT:-}" | "${command[@]}" || return 0
    return 0
  fi

  if [[ -n "${TRACEARY_HOOK_DEBUG:-}" ]]; then
    printf '%s' "${TRACEARY_HOOK_INPUT:-}" | "${command[@]}" >/dev/null || return 0
    return 0
  fi

  printf '%s' "${TRACEARY_HOOK_INPUT:-}" | "${command[@]}" >/dev/null 2>&1 || return 0
}
