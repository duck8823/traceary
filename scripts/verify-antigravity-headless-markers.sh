#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/traceary-antigravity-headless.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

if ! command -v agy >/dev/null 2>&1; then
  echo "antigravity headless marker probe: agy is not installed" >&2
  exit 2
fi

candidate=${TRACEARY_DOGFOOD_BINARY:-"$work_dir/traceary-candidate"}
if [ -z "${TRACEARY_DOGFOOD_BINARY:-}" ]; then
  (
    cd "$repo_root"
    go build -o "$candidate" .
  )
fi

# The packaged hooks invoke `traceary` through PATH. Put the candidate first so
# the live host exercises this checkout while every persisted marker stays in
# an isolated disposable database.
mkdir -p "$work_dir/bin" "$work_dir/state"
ln -s "$candidate" "$work_dir/bin/traceary"
PATH="$work_dir/bin:$PATH"
export PATH
export TRACEARY_DB_PATH="$work_dir/traceary.db"
export TRACEARY_HOOK_STATE_DIR="$work_dir/state"
export TRACEARY_WORKSPACE="github.com/dogfood/antigravity-headless-markers"

marker=TRACEARY_ANTIGRAVITY_HEADLESS_OK
prompt="Reply with exactly ${marker}. Do not call tools."
stdout_path="$work_dir/agy.stdout"
stderr_path="$work_dir/agy.stderr"

# Safety invariants: plan mode, terminal sandbox, and no permission bypass.
# agy can exit 0 with empty stdout when a hook is auto-denied (no TTY to
# prompt). Diagnose permission wording on stderr regardless of exit status.
agy_exit=0
if ! agy --print --mode plan --sandbox --print-timeout 120s "$prompt" >"$stdout_path" 2>"$stderr_path"; then
  agy_exit=$?
fi
if grep -qi "permission" "$stderr_path"; then
  echo "antigravity headless marker probe: scoped hook permission is absent or shadowed" >&2
  exit 1
fi
if [ "$agy_exit" -ne 0 ]; then
  echo "antigravity headless marker probe: agy exited before the marker response" >&2
  exit 1
fi
if ! grep -Fxq "$marker" "$stdout_path"; then
  echo "antigravity headless marker probe: expected public response marker was not returned" >&2
  exit 1
fi

events_path="$work_dir/events.json"
"$candidate" list \
  --db-path "$TRACEARY_DB_PATH" \
  --agent antigravity \
  --limit 100 \
  --fields id,kind,session,source_hook \
  --json >"$events_path"

# Read and print metadata only. Prompt/response bodies and the host transcript
# never enter the probe result.
python3 - "$events_path" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    events = json.load(stream)

kinds = [event.get("kind") for event in events]
source_hooks = [event.get("source_hook") for event in events]
result = {
    "agy_exit": 0,
    "sandbox": True,
    "permission_bypass": False,
    "response_marker": True,
    "session_start": "session_started" in kinds,
    "prompt": "prompt" in kinds,
    "final_turn": "transcript" in kinds,
    "stop_boundary": "stop_transcript" in source_hooks,
    # Antigravity Stop is a turn boundary, not a true session-end event.
    "session_end": "session_ended" in kinds,
}

required = ("session_start", "prompt", "final_turn", "stop_boundary")
missing = [key for key in required if not result[key]]
if missing:
    raise SystemExit("missing body-free marker(s): " + ", ".join(missing))

print(json.dumps(result, indent=2, sort_keys=True))
PY
