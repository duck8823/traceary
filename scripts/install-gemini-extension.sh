#!/bin/bash

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/install-gemini-extension.sh [--ref <git-ref>]

Installs the Traceary Gemini extension, then installs project hooks.
Existing installs are copied aside first. Gemini CLI cannot overlay an
already-installed name, so the script then uninstalls and installs. A
failed or timed-out gemini CLI call restores the previous directory.

`gemini extensions update` prompts interactively on local installs and
is not used.

--ref defaults to v$(traceary -v version), so a tagged CLI is not silently
paired with main-branch hooks. Override with --ref or TRACEARY_GEMINI_REF.

TRACEARY_GEMINI_TIMEOUT (seconds, default 60) bounds each gemini CLI call.
If --ref matches this checkout's VERSION, the script installs from
integrations/gemini-extension instead of cloning (avoids the temp-clone hang).
EOF
}

REF="${TRACEARY_GEMINI_REF:-}"
TIMEOUT="${TRACEARY_GEMINI_TIMEOUT:-60}"
while [[ $# -gt 0 ]]; do
    case "$1" in
        --ref)
            if [[ $# -lt 2 || -z "${2:-}" ]]; then
                echo "error: --ref requires a git ref" >&2
                exit 64
            fi
            REF="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            usage
            exit 64
            ;;
    esac
done

if ! command -v traceary &> /dev/null; then
    echo "error: traceary is not installed. Please install it first (e.g., brew install traceary or go install github.com/duck8823/traceary@latest)" >&2
    exit 1
fi

if ! command -v gemini &> /dev/null; then
    echo "error: gemini is not installed. Please install it first (e.g., npm install -g @google/gemini-cli)" >&2
    exit 1
fi

if ! command -v python3 &> /dev/null; then
    echo "error: python3 is required to bound gemini CLI invocations" >&2
    exit 1
fi

if [[ -z "$REF" ]]; then
    cli_version="$(traceary -v | awk '{print $2}')"
    if [[ -z "$cli_version" ]]; then
        echo "error: could not parse version from \`traceary -v\`" >&2
        exit 64
    fi
    REF="v${cli_version#v}"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
EXT_HOME="${GEMINI_EXTENSION_HOME:-${HOME}/.gemini/extensions}"
EXT_DIR="${EXT_HOME}/traceary"

TMP_DIR="$(mktemp -d)"
BACKUP_DIR=""
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

run_gemini() {
    python3 - "$TIMEOUT" "$@" <<'PY'
import subprocess
import sys

timeout = int(sys.argv[1])
cmd = sys.argv[2:]
try:
    completed = subprocess.run(cmd, timeout=timeout)
except subprocess.TimeoutExpired:
    print(
        f"error: gemini timed out after {timeout}s: {' '.join(cmd)}",
        file=sys.stderr,
    )
    raise SystemExit(124)
raise SystemExit(completed.returncode)
PY
}

restore_previous() {
    if [[ -z "$BACKUP_DIR" || ! -d "$BACKUP_DIR" ]]; then
        return 0
    fi
    echo "Restoring previous Gemini extension from backup..."
    mkdir -p "$EXT_HOME"
    rm -rf "$EXT_DIR"
    cp -a "$BACKUP_DIR" "$EXT_DIR"
}

checkout_version=""
if [[ -f "${REPO_ROOT}/VERSION" ]]; then
    checkout_version="$(tr -d '[:space:]' < "${REPO_ROOT}/VERSION")"
fi
src=""
if [[ -n "$checkout_version" && "v${checkout_version#v}" == "$REF" && -d "${REPO_ROOT}/integrations/gemini-extension" ]]; then
    echo "Using checkout integrations/gemini-extension for ${REF} (matches VERSION)."
    src="${REPO_ROOT}/integrations/gemini-extension"
else
    echo "Cloning Traceary ${REF} for extension assets..."
    git clone --depth 1 --branch "$REF" https://github.com/duck8823/traceary.git "$TMP_DIR/src"
    src="${TMP_DIR}/src/integrations/gemini-extension"
fi

if [[ -d "$EXT_DIR" ]]; then
    BACKUP_DIR="${TMP_DIR}/previous-traceary"
    cp -a "$EXT_DIR" "$BACKUP_DIR"
    echo "Copied previous Gemini extension aside (gemini cannot overlay an installed name)..."
    set +e
    run_gemini gemini extensions uninstall traceary
    uninstall_rc=$?
    set -e
    if [[ "$uninstall_rc" -eq 124 ]]; then
        restore_previous
        echo "error: gemini extensions uninstall timed out; previous extension restored. Recovery: gemini extensions install --consent ${REPO_ROOT}/integrations/gemini-extension" >&2
        exit 124
    fi
fi

echo "Installing Gemini extension from ${src}..."
set +e
run_gemini gemini extensions install --consent "$src"
install_rc=$?
set -e
if [[ "$install_rc" -eq 124 ]]; then
    restore_previous
    echo "error: gemini extensions install timed out; previous extension restored if one existed. Recovery: gemini extensions install --consent ${REPO_ROOT}/integrations/gemini-extension" >&2
    exit 124
fi
if [[ "$install_rc" -ne 0 ]]; then
    restore_previous
    echo "error: gemini extensions install failed; previous extension restored if one existed. Recovery: gemini extensions install --consent ${REPO_ROOT}/integrations/gemini-extension" >&2
    exit 1
fi

echo "Configuring Traceary hooks for Gemini CLI in current project..."
traceary hooks install --client gemini --project-dir .

echo "Traceary Gemini extension installed and configured successfully."
echo "Pinned to ${REF}. Run 'traceary doctor --client gemini' to verify."
echo "If a later install hangs, recover with:"
echo "  gemini extensions install --consent ${REPO_ROOT}/integrations/gemini-extension"
