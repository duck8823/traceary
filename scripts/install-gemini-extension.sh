#!/bin/bash

set -euo pipefail

# Check for Traceary CLI
if ! command -v traceary &> /dev/null; then
    echo "❌ traceary is not installed. Please install it first (e.g., brew install traceary or go install github.com/duck8823/traceary@latest)"
    exit 1
fi

# Check for Gemini CLI
if ! command -v gemini &> /dev/null; then
    echo "❌ gemini is not installed. Please install it first (e.g., npm install -g @google/gemini-cli)"
    exit 1
fi

# Install extension from a clean clone to avoid path issues
TMP_DIR=$(mktemp -d)
echo "Cloning latest Traceary for extension assets..."
git clone --depth 1 https://github.com/duck8823/traceary.git "$TMP_DIR"
echo "Installing Gemini extension..."
gemini extensions install "$TMP_DIR/integrations/gemini-extension"
rm -rf "$TMP_DIR"

# Install hooks to .gemini/settings.json in current project
echo "Configuring Traceary hooks for Gemini CLI in current project..."
traceary hooks install --client gemini --project-dir .

echo "✅ Traceary Gemini extension installed and configured successfully!"
echo "Run 'traceary doctor --client gemini' to verify."
