#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/traceary-antigravity-stop.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

binary=${TRACEARY_DOGFOOD_BINARY:-"$work_dir/traceary"}
if [ -z "${TRACEARY_DOGFOOD_BINARY:-}" ]; then
  (
    cd "$repo_root"
    go build -o "$binary" .
  )
fi

db_path="$work_dir/traceary.db"
state_dir="$work_dir/state"
transcript_path="$work_dir/transcript.jsonl"
export TRACEARY_HOOK_STATE_DIR="$state_dir"
export TRACEARY_WORKSPACE="github.com/dogfood/antigravity-stop"

fire_stop() {
  payload=$(printf '{"conversationId":"antigravity-stop-rollout","workspacePaths":["/dogfood/antigravity-stop"],"transcriptPath":"%s","terminationReason":"completed"}' "$transcript_path")
  output=$(printf '%s' "$payload" | "$binary" hook antigravity stop --db-path "$db_path")
  if [ "$output" != '{"decision":""}' ]; then
    printf 'unexpected Stop output: %s\n' "$output" >&2
    exit 1
  fi
}

# 101 distinct stable turns provide 202 accepted body-event attempts. Replaying
# the last turn adds two exact redeliveries, producing 2/204 = 0.9804%.
turn=0
while [ "$turn" -lt 101 ]; do
  prompt_step=$((turn * 2))
  response_step=$((prompt_step + 1))
  {
    printf '{"step_index":%d,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"dogfood prompt %d"}\n' "$prompt_step" "$turn"
    printf '{"step_index":%d,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","content":"dogfood response %d"}\n' "$response_step" "$turn"
  } >"$transcript_path"
  fire_stop
  turn=$((turn + 1))
done
fire_stop

before_report=$(python3 - "$db_path" <<'PY'
import json
import sqlite3
import sys

with sqlite3.connect(sys.argv[1]) as db:
    result = {
        "events": db.execute("SELECT COUNT(*) FROM events").fetchone()[0],
        "prompts": db.execute("SELECT COUNT(*) FROM events WHERE kind = 'prompt'").fetchone()[0],
        "transcripts": db.execute("SELECT COUNT(*) FROM events WHERE kind = 'transcript'").fetchone()[0],
        "session_ended": db.execute("SELECT COUNT(*) FROM events WHERE kind = 'session_ended'").fetchone()[0],
    }
print(json.dumps(result, sort_keys=True))
PY
)

report_path="$work_dir/report.json"
"$binary" report workspace-identity --db-path "$db_path" --json >"$report_path"

python3 - "$db_path" "$report_path" "$before_report" <<'PY'
import json
import math
import sqlite3
import sys

db_path, report_path, before_json = sys.argv[1:]
before = json.loads(before_json)
with open(report_path, encoding="utf-8") as stream:
    report = json.load(stream)

with sqlite3.connect(db_path) as db:
    after = {
        "events": db.execute("SELECT COUNT(*) FROM events").fetchone()[0],
        "prompts": db.execute("SELECT COUNT(*) FROM events WHERE kind = 'prompt'").fetchone()[0],
        "transcripts": db.execute("SELECT COUNT(*) FROM events WHERE kind = 'transcript'").fetchone()[0],
        "session_ended": db.execute("SELECT COUNT(*) FROM events WHERE kind = 'session_ended'").fetchone()[0],
    }

exact = report["exact_delivery"]
heuristic = report["heuristic_candidates"]
expected_rate = 2 / 204
checks = [
    (before == after, f"report mutated event counts: {before} -> {after}"),
    (after == {"events": 202, "prompts": 101, "transcripts": 101, "session_ended": 0}, f"unexpected event counts: {after}"),
    (exact["attempt_count"] == 204, f"attempt_count={exact['attempt_count']}"),
    (exact["exact_redelivery_count"] == 2, f"exact_redelivery_count={exact['exact_redelivery_count']}"),
    (math.isclose(exact["exact_redelivery_rate"], expected_rate), f"exact_redelivery_rate={exact['exact_redelivery_rate']}"),
    (exact["sample_available"] is True, "sample_available is false"),
    (exact["target_met"] is True, "target_met is false"),
    (heuristic["candidate_count"] == 0, f"heuristic candidate_count={heuristic['candidate_count']}"),
]
failures = [message for passed, message in checks if not passed]
if failures:
    raise SystemExit("; ".join(failures))

print(json.dumps({
    "event_counts": after,
    "runtime_attempts": exact["attempt_count"],
    "accepted_attempts": exact["attempt_count"] - exact["exact_redelivery_count"],
    "exact_redeliveries": exact["exact_redelivery_count"],
    "exact_redelivery_rate": exact["exact_redelivery_rate"],
    "target_rate": exact["target_rate"],
    "target_met": exact["target_met"],
    "heuristic_candidates": heuristic["candidate_count"],
    "report_preserved_events": before == after,
}, indent=2, sort_keys=True))
PY
