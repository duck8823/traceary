#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLUGIN_DIR="${ROOT_DIR}/integrations/grok-plugin"
PLUGIN_NAME="traceary-grok"
PLUGIN_SOURCE="${ROOT_DIR}#integrations/grok-plugin"
MIGRATE_LOCAL_REPO_IDENTITY=false

usage() {
  cat >&2 <<'EOF'
usage: scripts/install-grok-plugin.sh [--migrate-local-repo-identity]

Installs the canonical traceary-grok package from this checkout, then installs
user-level Grok command hooks (~/.grok/hooks/traceary.json) via
`traceary hooks install --client grok --global`. Grok Build 1.0.5 executes
those user-level command files; the plugin listing is not dispatch.

Without the migration flag, the installer never removes a package named
traceary. If Grok previously installed this checkout without its subdirectory
selector, it is reported as a local-repository identity conflict instead.
Review the bounded migration and rerun with --migrate-local-repo-identity to
remove only that exact checkout-local conflict before installing traceary-grok.

TRACEARY_BIN overrides the traceary CLI used for the user-level hook install.
The installer never writes cmux-session.json.
EOF
}

case "${1:-}" in
  "") ;;
  --migrate-local-repo-identity) MIGRATE_LOCAL_REPO_IDENTITY=true ;;
  -h|--help) usage; exit 0 ;;
  *) usage; exit 64 ;;
esac

command -v grok >/dev/null 2>&1 || {
  echo 'error: grok CLI is not installed' >&2
  exit 69
}

# Grok 1.0.5 executes ~/.grok/hooks/*.json command files. The native plugin
# is listing-only on that host, so capture requires this user-level file.
install_grok_user_command_hooks() {
  local traceary_bin="${TRACEARY_BIN:-}"
  if [[ -z "${traceary_bin}" ]]; then
    if command -v traceary >/dev/null 2>&1; then
      traceary_bin="$(command -v traceary)"
    else
      echo 'note: traceary CLI is not on PATH; after installing the CLI run: traceary hooks install --client grok --global' >&2
      return 0
    fi
  fi
  local user_hooks="${HOME}/.grok/hooks/traceary.json"
  if [[ -f "${user_hooks}" ]]; then
    "${traceary_bin}" hooks install --client grok --global --upgrade
  else
    "${traceary_bin}" hooks install --client grok --global
  fi
  echo 'installed user-level Grok command hooks at ~/.grok/hooks/traceary.json (Grok 1.0.5 executes this route; plugin listing is not dispatch)'
}

grok plugin validate "${PLUGIN_DIR}"
plugin_list="$(grok plugin list --json)"

# A source ending at integrations/grok-plugin makes Grok clone the repository
# root without the required #subdir selector. Grok then exposes the repository
# identity (usually traceary), rather than this package's traceary-grok name.
# Inspect only inventory metadata; never read installed plugin files or payloads.
if ! local_repo_conflict="$(PLUGIN_LIST="${plugin_list}" python3 - "${PLUGIN_DIR}" <<'PY'
import json
import os
import pathlib
import sys

plugin_dir = pathlib.Path(sys.argv[1]).resolve()
try:
    plugins = json.loads(os.environ["PLUGIN_LIST"])
except json.JSONDecodeError:
    raise SystemExit(1)

for plugin in plugins:
    source = plugin.get("source")
    name = plugin.get("name")
    if not isinstance(source, str) or not isinstance(name, str):
        continue
    try:
        same_source = pathlib.Path(source).resolve() == plugin_dir
    except OSError:
        same_source = False
    if same_source and name != "traceary-grok":
        print(name)
        break
PY
)"; then
  echo 'error: Grok plugin inventory was not valid JSON' >&2
  exit 65
fi

if [[ -n "${local_repo_conflict}" ]]; then
  if [[ "${MIGRATE_LOCAL_REPO_IDENTITY}" != true ]]; then
    cat >&2 <<'EOF'
error: this checkout is installed through Grok's local-repository identity, not as traceary-grok.
No package was removed. Review the installed plugin inventory, then rerun:
  scripts/install-grok-plugin.sh --migrate-local-repo-identity
The migration removes only the conflicting identity whose source is this checkout's
integrations/grok-plugin directory. It never removes another legacy traceary package.
EOF
    exit 78
  fi
  echo 'info: removing the explicitly approved local-repository identity for this checkout' >&2
  grok plugin uninstall --confirm "${local_repo_conflict}"
fi

if printf '%s' "${plugin_list}" | grep -q '"name"[[:space:]]*:[[:space:]]*"traceary-grok"'; then
  grok plugin uninstall "${PLUGIN_NAME}"
fi
grok plugin install --trust "${PLUGIN_SOURCE}"
grok plugin details "${PLUGIN_NAME}"
echo 'installed traceary-grok: 7 native hook boundaries and 4 skills'
echo 'note: the installer intentionally does not uninstall a legacy plugin named traceary because it may belong to another host'
install_grok_user_command_hooks
