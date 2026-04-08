#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_PATH="${1:-${ROOT_DIR}/dist/traceary.tar.gz}"
SOURCE_DIR="${ROOT_DIR}/integrations/gemini-extension"

mkdir -p "$(dirname "${OUTPUT_PATH}")"
rm -f "${OUTPUT_PATH}"

tar -C "${SOURCE_DIR}" -czf "${OUTPUT_PATH}" .
echo "packaged Gemini extension at ${OUTPUT_PATH}"
