package cli

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"
)

const hooksScriptsDirEnvKey = "TRACEARY_HOOK_SCRIPTS_DIR"

type hookScriptAsset struct {
	name    string
	content string
}

var hookScriptAssets = []hookScriptAsset{
	{
		name: "common.sh",
		content: `#!/bin/bash

set -euo pipefail

traceary_read_hook_input() {
  if [[ -n "${TRACEARY_HOOK_INPUT:-}" ]]; then
    export TRACEARY_HOOK_INPUT
    return 0
  fi

  TRACEARY_HOOK_INPUT="$(cat 2>/dev/null || true)"
  export TRACEARY_HOOK_INPUT
}

traceary_helper_command() {
  local helper_name="${1:-}"
  shift || true

  local traceary_cmd="${TRACEARY_CMD:-}"
  if [[ -z "$traceary_cmd" ]]; then
    traceary_cmd="$(traceary_resolve_bin 2>/dev/null || true)"
  fi
  if [[ -z "$traceary_cmd" ]]; then
    return 1
  fi

  TRACEARY_HOOK_INPUT="${TRACEARY_HOOK_INPUT:-}" "$traceary_cmd" hooks helper "$helper_name" "$@" 2>/dev/null
}

traceary_json_get() {
  local path="${1:-}"
  local default_value="${2:-}"

  traceary_helper_command json-get "$path" "$default_value" || printf '%s' "$default_value"
}

traceary_build_failure_output() {
  traceary_helper_command build-failure-output || true
}

traceary_normalize_git_remote() {
  local raw="${1:-}"

  traceary_helper_command normalize-git-remote "$raw" || true
}

traceary_resolve_repo() {
  local cwd="${1:-}"

  if [[ -n "${TRACEARY_REPO:-}" ]]; then
    printf '%s' "$TRACEARY_REPO"
    return 0
  fi
  if [[ -z "$cwd" ]]; then
    return 0
  fi
  if command -v git >/dev/null 2>&1; then
    local remote
    remote="$(git -C "$cwd" config --get remote.origin.url 2>/dev/null || true)"
    if [[ -n "$remote" ]]; then
      traceary_normalize_git_remote "$remote"
      return 0
    fi

    local repo_root
    repo_root="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null || true)"
    if [[ -n "$repo_root" ]]; then
      printf '%s' "$repo_root"
      return 0
    fi
  fi

  printf '%s' "$cwd"
}

traceary_resolve_bin() {
  if [[ -n "${TRACEARY_BIN:-}" ]]; then
    printf '%s' "$TRACEARY_BIN"
    return 0
  fi
  if command -v traceary >/dev/null 2>&1; then
    command -v traceary
    return 0
  fi

  return 1
}

traceary_session_state_path() {
  local client="$1"
  local state_dir="${TRACEARY_HOOK_STATE_DIR:-${HOME}/.config/traceary/hooks}"
  mkdir -p "$state_dir"
  printf '%s/%s-%s' "$state_dir" "$client" "${TRACEARY_HOOK_STATE_KEY:-${PPID:-$$}}"
}

traceary_sanitize_state_key() {
  local value="${1:-}"

  printf '%s' "$value" | tr -cs 'A-Za-z0-9._-' '_'
}

traceary_session_end_marker_path() {
  local client="$1"
  local session_id="$2"
  local state_dir="${TRACEARY_HOOK_STATE_DIR:-${HOME}/.config/traceary/hooks}"
  local sanitized_session_id
  sanitized_session_id="$(traceary_sanitize_state_key "$session_id")"
  mkdir -p "$state_dir/ended"
  printf '%s/ended/%s-%s' "$state_dir" "$client" "$sanitized_session_id"
}

traceary_write_state() {
  local client="$1"
  local session_id="$2"
  local state_file
  state_file="$(traceary_session_state_path "$client")"
  printf '%s' "$session_id" > "$state_file"
}

traceary_read_state() {
  local client="$1"
  local state_file
  state_file="$(traceary_session_state_path "$client")"
  if [[ ! -f "$state_file" ]]; then
    return 0
  fi

  cat "$state_file"
}

traceary_clear_state() {
  local client="$1"
  local state_file
  state_file="$(traceary_session_state_path "$client")"
  rm -f "$state_file"
}

traceary_mark_session_ended() {
  local client="$1"
  local session_id="$2"
  local marker_file
  marker_file="$(traceary_session_end_marker_path "$client" "$session_id")"
  : > "$marker_file"
}

traceary_session_end_already_recorded() {
  local client="$1"
  local session_id="$2"
  local marker_file
  marker_file="$(traceary_session_end_marker_path "$client" "$session_id")"
  [[ -f "$marker_file" ]]
}

traceary_clear_session_end_marker() {
  local client="$1"
  local session_id="$2"
  local marker_file
  marker_file="$(traceary_session_end_marker_path "$client" "$session_id")"
  rm -f "$marker_file"
}
`,
	},
	{
		name: "traceary-session.sh",
		content: `#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

CLIENT="${1:-}"
ACTION="${2:-}"

if [[ -z "$CLIENT" || -z "$ACTION" ]]; then
  echo "usage: traceary-session.sh <client> <start|end|stop>" >&2
  exit 64
fi

traceary_read_hook_input

TRACEARY_CMD="$(traceary_resolve_bin 2>/dev/null || true)"
if [[ -z "$TRACEARY_CMD" ]]; then
  exit 0
fi

HOOK_CWD="$(traceary_json_get 'cwd')"
SESSION_ID="$(traceary_json_get 'session_id')"
REPO_VALUE="$(traceary_resolve_repo "$HOOK_CWD")"

COMMON_ARGS=(session)
if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
  COMMON_ARGS+=(--db-path "$TRACEARY_DB_PATH")
fi
COMMON_ARGS+=("${ACTION/start/start}")

case "$ACTION" in
  start)
    COMMAND=("$TRACEARY_CMD" session start --client hook --agent "$CLIENT")
    if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
      COMMAND+=(--db-path "$TRACEARY_DB_PATH")
    fi
    if [[ -n "$REPO_VALUE" ]]; then
      COMMAND+=(--repo "$REPO_VALUE")
    fi
    if [[ -n "$SESSION_ID" ]]; then
      COMMAND+=(--session-id "$SESSION_ID")
    fi

    ACTUAL_SESSION_ID="$("${COMMAND[@]}" 2>/dev/null | tail -n 1 | tr -d '\r' || true)"
    if [[ -n "$ACTUAL_SESSION_ID" ]]; then
      traceary_write_state "$CLIENT" "$ACTUAL_SESSION_ID"
      traceary_clear_session_end_marker "$CLIENT" "$ACTUAL_SESSION_ID"
    fi
    ;;
  end|stop)
    if [[ -z "$SESSION_ID" ]]; then
      SESSION_ID="$(traceary_read_state "$CLIENT")"
    fi
    if [[ -z "$SESSION_ID" ]]; then
      exit 0
    fi

    if traceary_session_end_already_recorded "$CLIENT" "$SESSION_ID"; then
      traceary_clear_state "$CLIENT"
      exit 0
    fi

    COMMAND=("$TRACEARY_CMD" session end --client hook --agent "$CLIENT" --session-id "$SESSION_ID")
    if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
      COMMAND+=(--db-path "$TRACEARY_DB_PATH")
    fi
    if [[ -n "$REPO_VALUE" ]]; then
      COMMAND+=(--repo "$REPO_VALUE")
    fi
    "${COMMAND[@]}" >/dev/null 2>&1 || exit 0
    traceary_clear_state "$CLIENT"
    traceary_mark_session_ended "$CLIENT" "$SESSION_ID"
    ;;
  *)
    echo "unsupported action: $ACTION" >&2
    exit 64
    ;;
esac

exit 0
`,
	},
	{
		name: "traceary-audit.sh",
		content: `#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
source "$SCRIPT_DIR/common.sh"

CLIENT="${1:-}"

if [[ -z "$CLIENT" ]]; then
  echo "usage: traceary-audit.sh <client>" >&2
  exit 64
fi

traceary_read_hook_input

TRACEARY_CMD="$(traceary_resolve_bin 2>/dev/null || true)"
if [[ -z "$TRACEARY_CMD" ]]; then
  exit 0
fi

COMMAND_VALUE="$(traceary_json_get 'tool_input.command')"
if [[ -z "$COMMAND_VALUE" ]]; then
  exit 0
fi

SESSION_ID="$(traceary_json_get 'session_id')"
if [[ -z "$SESSION_ID" ]]; then
  SESSION_ID="$(traceary_read_state "$CLIENT")"
fi
if [[ -z "$SESSION_ID" ]]; then
  exit 0
fi

HOOK_CWD="$(traceary_json_get 'cwd')"
REPO_VALUE="$(traceary_resolve_repo "$HOOK_CWD")"
AUDIT_INPUT="$(traceary_json_get 'tool_input' '{}')"
AUDIT_OUTPUT="$(traceary_json_get 'tool_response')"
if [[ -z "$AUDIT_OUTPUT" ]]; then
  AUDIT_OUTPUT="$(traceary_build_failure_output || true)"
fi

COMMAND=("$TRACEARY_CMD" audit "$COMMAND_VALUE" "$AUDIT_INPUT" "$AUDIT_OUTPUT" --client hook --agent "$CLIENT" --session-id "$SESSION_ID")
if [[ -n "${TRACEARY_DB_PATH:-}" ]]; then
  COMMAND+=(--db-path "$TRACEARY_DB_PATH")
fi
if [[ -n "$REPO_VALUE" ]]; then
  COMMAND+=(--repo "$REPO_VALUE")
fi

"${COMMAND[@]}" >/dev/null 2>&1 || exit 0

exit 0
`,
	},
}

func ensureHookScriptsInstalled() (string, error) {
	scriptsDir, err := resolveHooksScriptsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to create hook scripts directory", "hook script ディレクトリの作成に失敗しました"), err)
	}

	for _, asset := range hookScriptAssets {
		outputPath := filepath.Join(scriptsDir, asset.name)
		currentContent, err := os.ReadFile(outputPath)
		if err == nil && string(currentContent) == asset.content {
			if chmodErr := os.Chmod(outputPath, 0o755); chmodErr != nil {
				return "", xerrors.Errorf("%s: %w", Localize("failed to chmod hook script", "hook script の chmod に失敗しました"), chmodErr)
			}
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return "", xerrors.Errorf("%s: %w", Localize("failed to inspect installed hook script", "既存 hook script の確認に失敗しました"), err)
		}
		if err := os.WriteFile(outputPath, []byte(asset.content), 0o755); err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to write hook script", "hook script の書き出しに失敗しました"), err)
		}
	}

	return scriptsDir, nil
}

func resolveHooksScriptsDir() (string, error) {
	if envValue := strings.TrimSpace(os.Getenv(hooksScriptsDirEnvKey)); envValue != "" {
		resolvedPath, err := filepath.Abs(envValue)
		if err != nil {
			return "", xerrors.Errorf("%s: %w", Localize("failed to resolve absolute hook scripts path", "hook scripts path の絶対パス化に失敗しました"), err)
		}

		return resolvedPath, nil
	}

	homeDir, err := userHomeDirFunc()
	if err != nil {
		return "", xerrors.Errorf("%s: %w", Localize("failed to get user home directory", "ユーザーホームディレクトリの取得に失敗しました"), err)
	}

	return filepath.Join(homeDir, ".config", "traceary", "hook-scripts"), nil
}
