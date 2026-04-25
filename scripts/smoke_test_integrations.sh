#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TARGET="${1:-all}"

run_claude() {
  command -v claude >/dev/null 2>&1 || {
    echo "skip: claude not found" >&2
    return 0
  }

  local tmp_home
  tmp_home="$(mktemp -d)"
  HOME="${tmp_home}" claude plugins validate "${ROOT_DIR}/.claude-plugin/marketplace.json"
  HOME="${tmp_home}" claude plugins validate "${ROOT_DIR}/integrations/claude-plugin"
  rm -rf "${tmp_home}"
  echo 'ok: claude package validation passed'
  echo 'manual runtime probe: in a clean Claude Code profile, run /plugin marketplace add duck8823/traceary, then /plugin install traceary, then traceary doctor --client claude --json'
}

run_gemini() {
  command -v gemini >/dev/null 2>&1 || {
    echo "skip: gemini not found" >&2
    return 0
  }

  local tmp_home
  tmp_home="$(mktemp -d)"
  mkdir -p "${tmp_home}/.gemini"
  # Gemini CLI 0.38 expects projects.json to carry a top-level "projects"
  # map. Plain `{}` makes ProjectRegistry.getShortId read
  # `undefined[<path>]` during cleanupCheckpoints, which prints "Early
  # cleanup failed" / "Tool output cleanup failed" warnings per
  # invocation (#536). Writing the correct empty-but-keyed shape keeps
  # cleanup silent without weakening smoke coverage.
  printf '{"projects":{}}\n' > "${tmp_home}/.gemini/projects.json"
  HOME="${tmp_home}" gemini extensions validate "${ROOT_DIR}/integrations/gemini-extension"
  printf 'y\n' | HOME="${tmp_home}" gemini extensions link "${ROOT_DIR}/integrations/gemini-extension"
  local list_output
  list_output="$(HOME="${tmp_home}" gemini extensions list || true)"
  [[ "${list_output}" == *traceary* ]]
  HOME="${tmp_home}" gemini extensions uninstall traceary >/dev/null 2>&1 || true
  rm -rf "${tmp_home}"
  echo 'ok: gemini smoke test passed'
}

run_codex() {
  python3 "${ROOT_DIR}/scripts/verify_integrations.py"
  local tmp_root tmp_codex_home tmp_marketplace_root
  tmp_root="$(mktemp -d)"
  tmp_codex_home="${tmp_root}/codex-home"
  tmp_marketplace_root="${tmp_root}/agents/plugins"
  go run . integration codex install \
    --repo-root "${ROOT_DIR}" \
    --codex-home "${tmp_codex_home}" \
    --marketplace-root "${tmp_marketplace_root}" \
    --traceary-bin /tmp/traceary
  test -f "${tmp_marketplace_root}/plugins/traceary/.codex-plugin/plugin.json"
  test -f "${tmp_marketplace_root}/marketplace.json"
  test -f "${tmp_codex_home}/plugins/cache/local-traceary-plugins/traceary/local/.codex-plugin/plugin.json"
  test -f "${tmp_codex_home}/hooks.json"
  grep -q 'UserPromptSubmit' "${tmp_codex_home}/hooks.json"
  grep -q "'hook' 'prompt' 'codex'" "${tmp_codex_home}/hooks.json"
  grep -q 'codex_hooks = true' "${tmp_codex_home}/config.toml"
  grep -q 'traceary@local-traceary-plugins' "${tmp_codex_home}/config.toml"
  go run . integration codex uninstall \
    --codex-home "${tmp_codex_home}" \
    --marketplace-root "${tmp_marketplace_root}"
  rm -rf "${tmp_root}"
  if [[ "${TRACEARY_ENABLE_CODEX_RUNTIME_SMOKE:-0}" != "1" ]]; then
    echo 'ok: codex smoke test passed (set TRACEARY_ENABLE_CODEX_RUNTIME_SMOKE=1 for an authenticated runtime probe)'
    return 0
  fi

  command -v codex >/dev/null 2>&1 || {
    echo "skip: codex not found for runtime probe" >&2
    return 0
  }

  codex exec -C "${ROOT_DIR}" -a never -s workspace-write 'In one sentence, name the Traceary slash command or skill exposed by the current repository plugin bundle.'
  echo 'ok: codex runtime probe completed'
}

case "${TARGET}" in
  all)
    run_claude
    run_codex
    run_gemini
    ;;
  claude)
    run_claude
    ;;
  codex)
    run_codex
    ;;
  gemini)
    run_gemini
    ;;
  *)
    echo "usage: $0 [all|claude|codex|gemini]" >&2
    exit 64
    ;;
esac
