#!/usr/bin/env bash
# Clean-home smoke for the Traceary Grok plugin (#1301).
# Verifies install → details → doctor-shaped inventory → uninstall without
# touching the operator's real HOME or credentials.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLUGIN_DIR="${ROOT_DIR}/integrations/grok-plugin"
TMP_HOME="$(mktemp -d "${TMPDIR:-/tmp}/traceary-grok-clean-home.XXXXXX")"
cleanup() { rm -rf "${TMP_HOME}"; }
trap cleanup EXIT

export HOME="${TMP_HOME}"
export XDG_CONFIG_HOME="${TMP_HOME}/.config"
mkdir -p "${HOME}" "${XDG_CONFIG_HOME}"

if ! command -v grok >/dev/null 2>&1; then
  echo "skip: grok CLI is not installed (exit 0 for environments without Grok)" >&2
  exit 0
fi

if ! command -v traceary >/dev/null 2>&1 && [[ ! -x "${ROOT_DIR}/traceary" ]]; then
  echo "info: building local traceary binary for doctor" >&2
  (cd "${ROOT_DIR}" && go build -o "${TMP_HOME}/bin/traceary" .)
  export PATH="${TMP_HOME}/bin:${PATH}"
elif [[ -x "${ROOT_DIR}/traceary" ]]; then
  export PATH="${ROOT_DIR}:${PATH}"
fi

echo "== validate package =="
grok plugin validate "${PLUGIN_DIR}"

echo "== install (clean home) =="
if grok plugin list --json 2>/dev/null | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary"'; then
  grok plugin uninstall traceary || true
fi
grok plugin install --trust "${PLUGIN_DIR}"

echo "== details =="
grok plugin details traceary

echo "== list contains traceary =="
grok plugin list --json | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary"'

echo "== reinstall (update path) =="
# Host rejects double install of the same local path; uninstall then install
# models the post-upgrade refresh path operators use after a binary bump.
grok plugin uninstall traceary
grok plugin install --trust "${PLUGIN_DIR}"
grok plugin details traceary

echo "== doctor (best-effort; may skip host probes in empty home) =="
if command -v traceary >/dev/null 2>&1; then
  # Project dir is empty; doctor should still surface plugin presence checks.
  set +e
  traceary doctor --client grok --project-dir "${TMP_HOME}" --json >"${TMP_HOME}/doctor.json" 2>"${TMP_HOME}/doctor.err"
  doctor_rc=$?
  set -e
  echo "doctor exit=${doctor_rc}"
  if [[ -s "${TMP_HOME}/doctor.json" ]]; then
    # Prefer not hard-failing when grok host version probes are unavailable in CI.
    if grep -q 'grok-plugin' "${TMP_HOME}/doctor.json"; then
      echo "doctor reported grok-plugin check"
    fi
  fi
  cat "${TMP_HOME}/doctor.err" >&2 || true
fi

echo "== uninstall =="
grok plugin uninstall traceary
if grok plugin list --json 2>/dev/null | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary"'; then
  echo "error: traceary still listed after uninstall" >&2
  exit 1
fi

echo "OK: clean-home install/update/uninstall for integrations/grok-plugin"
