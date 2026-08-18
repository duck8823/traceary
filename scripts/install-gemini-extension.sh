#!/bin/bash

set -euo pipefail

usage() {
    cat >&2 <<'EOF'
usage: scripts/install-gemini-extension.sh [--ref <git-ref>]

Installs the Traceary Gemini extension from a clean clone, then installs
project hooks. Existing installs are uninstalled first: `gemini extensions
update` prompts interactively on local installs and is not used.

--ref defaults to v$(traceary -v version), so a tagged CLI is not silently
paired with main-branch hooks. Override with --ref or TRACEARY_GEMINI_REF.
EOF
}

REF="${TRACEARY_GEMINI_REF:-}"
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
    echo "❌ traceary is not installed. Please install it first (e.g., brew install traceary or go install github.com/duck8823/traceary@latest)"
    exit 1
fi

if ! command -v gemini &> /dev/null; then
    echo "❌ gemini is not installed. Please install it first (e.g., npm install -g @google/gemini-cli)"
    exit 1
fi

if [[ -z "$REF" ]]; then
    cli_version="$(traceary -v | awk '{print $2}')"
    if [[ -z "$cli_version" ]]; then
        echo "error: could not parse version from \`traceary -v\`" >&2
        exit 1
    fi
    REF="v${cli_version#v}"
fi

TMP_DIR=$(mktemp -d)
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

echo "Cloning Traceary ${REF} for extension assets..."
git clone --depth 1 --branch "$REF" https://github.com/duck8823/traceary.git "$TMP_DIR"

# Local installs hang on `gemini extensions update`. Uninstall-then-install is
# the non-interactive refresh. --consent skips the security prompt.
echo "Refreshing Gemini extension (uninstall existing, then install --consent)..."
gemini extensions uninstall traceary >/dev/null 2>&1 || true
gemini extensions install --consent "$TMP_DIR/integrations/gemini-extension"

echo "Configuring Traceary hooks for Gemini CLI in current project..."
traceary hooks install --client gemini --project-dir .

echo "✅ Traceary Gemini extension installed and configured successfully!"
echo "Pinned to ${REF}. Run 'traceary doctor --client gemini' to verify."
