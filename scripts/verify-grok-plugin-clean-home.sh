#!/usr/bin/env bash
# Clean-home smoke for the Traceary Grok plugin (#1301).
# Verifies install → details → doctor-shaped inventory → uninstall without
# touching the operator's real HOME or credentials.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLUGIN_DIR="${ROOT_DIR}/integrations/grok-plugin"
PLUGIN_NAME="traceary-grok"
TMP_HOME="$(mktemp -d "${TMPDIR:-/tmp}/traceary-grok-clean-home.XXXXXX")"
cleanup() { rm -rf "${TMP_HOME}"; }
trap cleanup EXIT

if ! command -v grok >/dev/null 2>&1; then
  echo "skip: grok CLI is not installed (exit 0 for environments without Grok)" >&2
  exit 0
fi

echo "info: building this checkout's traceary binary for doctor" >&2
mkdir -p "${TMP_HOME}/bin"
(cd "${ROOT_DIR}" && go build -o "${TMP_HOME}/bin/traceary" .)

export HOME="${TMP_HOME}"
export XDG_CONFIG_HOME="${TMP_HOME}/.config"
mkdir -p "${HOME}" "${XDG_CONFIG_HOME}"
export PATH="${TMP_HOME}/bin:${PATH}"

echo "== validate package =="
grok plugin validate "${PLUGIN_DIR}"

echo "== install (clean home) =="
if grok plugin list --json 2>/dev/null | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary-grok"'; then
  grok plugin uninstall "${PLUGIN_NAME}" || true
fi
grok plugin install --trust "${ROOT_DIR}#integrations/grok-plugin"

echo "== details =="
grok plugin details "${PLUGIN_NAME}"

echo "== list contains traceary =="
grok plugin list --json | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary-grok"'

echo "== reinstall (update path) =="
# Host rejects double install of the same local path; uninstall then install
# models the post-upgrade refresh path operators use after a binary bump.
grok plugin uninstall "${PLUGIN_NAME}"
grok plugin install --trust "${ROOT_DIR}#integrations/grok-plugin"
grok plugin details "${PLUGIN_NAME}"

echo "== doctor native plugin checks =="
if command -v traceary >/dev/null 2>&1; then
  mkdir -p "${TMP_HOME}/project"
  traceary doctor --client grok --project-dir "${TMP_HOME}/project" --json --warnings-ok >"${TMP_HOME}/doctor.json"
  python3 - "${TMP_HOME}/doctor.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as source:
    report = json.load(source)

expected = {
    "grok-plugin": "pass",
    "grok-plugin-resolution": "pass",
    "grok-hooks": "pass",
    "grok-mcp": "pass",
    "grok-skills": "pass",
}
actual = {check.get("name"): check.get("status") for check in report.get("checks", [])}
missing = [name for name in expected if name not in actual]
wrong = {name: actual.get(name) for name, status in expected.items() if actual.get(name) != status}
if missing or wrong:
    raise SystemExit(
        "error: Grok native doctor checks did not converge: "
        f"missing={missing} wrong={wrong}"
    )
PY
fi

echo "== uninstall =="
grok plugin uninstall "${PLUGIN_NAME}"
if grok plugin list --json 2>/dev/null | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary-grok"'; then
  echo "error: ${PLUGIN_NAME} still listed after uninstall" >&2
  exit 1
fi

echo "OK: clean-home install/update/uninstall for integrations/grok-plugin"
