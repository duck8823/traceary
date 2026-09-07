#!/usr/bin/env bash
# Install the packaged git hooks into the current checkout (#2341).
# Existing hooks are kept with a .traceary-bak suffix. Re-running is safe.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOK_SRC="${ROOT_DIR}/scripts/githooks"
HOOK_DIR="$(git -C "${ROOT_DIR}" rev-parse --git-dir)/hooks"

installed=0
for src in "${HOOK_SRC}"/*; do
  name="$(basename "${src}")"
  dest="${HOOK_DIR}/${name}"
  if [ -e "${dest}" ] && ! [ -L "${dest}" ]; then
    mv "${dest}" "${dest}.traceary-bak"
    echo "backup: ${dest} -> ${dest}.traceary-bak" >&2
  fi
  ln -sfn "${src}" "${dest}"
  echo "installed: ${name}" >&2
  installed=$((installed + 1))
done
echo "ok: ${installed} hook(s) installed (bypass per commit with TRACEARY_SKIP_HOOKS=1)"
