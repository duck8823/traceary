WITH window_events AS (
    SELECT kind, session_id, source_hook
      FROM events
     WHERE agent = 'codex'
       AND workspace = ?
       AND ts_norm(created_at) >= ts_norm(?)
       AND ts_norm(created_at) < ts_norm(?)
),
stop_counts AS (
    SELECT session_id, COUNT(*) AS stop_count
      FROM window_events
     WHERE kind = 'transcript'
       AND source_hook = 'stop'
     GROUP BY session_id
),
usage_counts AS (
    SELECT observation.session_id,
           COUNT(*) AS usage_count,
           SUM(CASE WHEN observation.total_state = 'known' THEN 1 ELSE 0 END) AS known_count,
           SUM(CASE WHEN observation.total_state = 'unavailable' THEN 1 ELSE 0 END) AS unavailable_count
      FROM usage_observations AS observation
      JOIN stop_counts AS stopped
        ON stopped.session_id = observation.session_id
     WHERE observation.host = 'codex'
       AND observation.status = 'finalized'
       AND observation.source_name IN ('rollout_jsonl', 'stop_hook')
       AND (
            observation.source_name = 'stop_hook'
            OR (
                ts_norm(observation.observed_at) >= ts_norm(?)
                AND ts_norm(observation.observed_at) < ts_norm(?)
            )
       )
     GROUP BY observation.session_id
),
stop_usage AS (
    SELECT stopped.stop_count,
           COALESCE(usage.usage_count, 0) AS usage_count,
           COALESCE(usage.known_count, 0) AS known_count,
           COALESCE(usage.unavailable_count, 0) AS unavailable_count
      FROM stop_counts AS stopped
      LEFT JOIN usage_counts AS usage
        ON usage.session_id = stopped.session_id
)
SELECT
    (SELECT COUNT(*) FROM window_events) AS stored_events,
    EXISTS(SELECT 1 FROM window_events WHERE kind = 'session_started') AS session_start_observed,
    EXISTS(SELECT 1 FROM window_events WHERE kind = 'prompt') AS prompt_observed,
    EXISTS(SELECT 1 FROM window_events WHERE kind = 'command_executed') AS tool_observed,
    EXISTS(SELECT 1 FROM window_events WHERE kind = 'compact_summary') AS compact_observed,
    COALESCE((SELECT SUM(stop_count) FROM stop_usage), 0) AS stop_count,
    COALESCE((SELECT SUM(MIN(stop_count, usage_count)) FROM stop_usage), 0) AS covered_stop_count,
    COALESCE((SELECT SUM(usage_count) FROM stop_usage), 0) AS usage_count,
    COALESCE((SELECT SUM(known_count) FROM stop_usage), 0) AS known_count,
    COALESCE((SELECT SUM(unavailable_count) FROM stop_usage), 0) AS unavailable_count;
