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
  if [[ "${TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE:-0}" != "1" ]]; then
    echo 'skip: gemini runtime smoke test requires an authenticated Gemini CLI and may open a browser; set TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 to opt in'
    return 0
  fi

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
  echo 'ok: gemini runtime smoke test passed'
}

run_codex() {
  # The repo-tooling verifier owns the v0.25.0 assertion that the entire
  # `integration` subtree is gone (checkCodexRemovedCommands). The shell
  # side adds a quick CLI-level guard so re-registration surfaces here too.
  (cd "${ROOT_DIR}" && go run ./cmd/repo-tooling integrations verify)
  local integration_output
  if integration_output="$(TRACEARY_LANG=en go run . integration codex install 2>&1)"; then
    echo "error: 'go run . integration codex install' unexpectedly succeeded after v0.25.0 removal" >&2
    echo "${integration_output}" >&2
    return 1
  fi
  # Expect Cobra unknown-command style failure (no migration stubs remain).
  if ! printf '%s' "${integration_output}" | grep -Eqi 'unknown|invalid|不明|未知'; then
    echo "error: integration subtree removal must fail as unknown command: ${integration_output}" >&2
    return 1
  fi
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

run_grok() {
  command -v grok >/dev/null 2>&1 || {
    echo "skip: grok not found" >&2
    return 0
  }

  local tmp_home
  tmp_home="$(mktemp -d)"
  HOME="${tmp_home}" grok plugin validate "${ROOT_DIR}/integrations/grok-plugin"
  HOME="${tmp_home}" grok plugin install --trust "${ROOT_DIR}/integrations/grok-plugin"
  local list_output details_output inspect_output tmp_cwd
  list_output="$(HOME="${tmp_home}" grok plugin list --json)"
  details_output="$(HOME="${tmp_home}" grok plugin details traceary)"
  tmp_cwd="$(mktemp -d)"
  inspect_output="$(HOME="${tmp_home}" grok --cwd "${tmp_cwd}" inspect --json)"
  [[ "${list_output}" == *'"name":"traceary"'* || "${list_output}" == *'"name": "traceary"'* ]]
  [[ "${details_output}" == *traceary* ]]
  [[ "${details_output}" == *traceary-session-history* ]]
  [[ "${inspect_output}" == *'"plugin_name": "traceary"'* ]]
  [[ "${inspect_output}" == *'"mcpServers": 1'* ]]
  HOME="${tmp_home}" grok plugin uninstall traceary
  rm -rf "${tmp_home}" "${tmp_cwd}"
  echo 'ok: grok plugin validation, install, inventory, and uninstall passed'
}

case "${TARGET}" in
  all)
    run_claude
    run_codex
    run_gemini
    run_grok
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
  grok)
    run_grok
    ;;
  *)
    echo "usage: $0 [all|claude|codex|gemini|grok]" >&2
    exit 64
    ;;
esac
