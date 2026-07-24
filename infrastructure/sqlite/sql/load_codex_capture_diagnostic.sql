WITH window_events AS (
    SELECT id, kind, session_id, source_hook, created_at
      FROM events
     WHERE agent = 'codex'
       AND workspace = ?
       AND ts_norm(created_at) >= ts_norm(?)
       AND ts_norm(created_at) < ts_norm(?)
),
window_sessions AS (
    SELECT DISTINCT session_id
      FROM window_events
     WHERE session_id <> ''
),
window_stops AS (
    SELECT id, session_id, created_at
      FROM window_events
     WHERE kind = 'transcript'
       AND source_hook = 'stop'
),
stop_counts AS (
    SELECT session_id, COUNT(*) AS stop_count
      FROM window_stops
     GROUP BY session_id
),
prompt_turns AS (
    SELECT id,
           session_id,
           created_at,
           LEAD(created_at) OVER prompt_order AS next_created_at,
           LEAD(id) OVER prompt_order AS next_id
      FROM window_events
     WHERE kind = 'prompt'
       AND source_hook = 'user_prompt_submit'
    WINDOW prompt_order AS (
        PARTITION BY session_id
        ORDER BY ts_norm(created_at), id
    )
),
final_turn_gaps AS (
    SELECT COUNT(*) AS prompt_count,
           SUM(CASE WHEN EXISTS (
               SELECT 1
                 FROM window_stops AS stopped
                WHERE stopped.session_id = prompted.session_id
                  AND (
                      ts_norm(stopped.created_at) > ts_norm(prompted.created_at)
                      OR (
                          ts_norm(stopped.created_at) = ts_norm(prompted.created_at)
                          AND stopped.id > prompted.id
                      )
                  )
                  AND (
                      prompted.next_created_at IS NULL
                      OR ts_norm(stopped.created_at) < ts_norm(prompted.next_created_at)
                      OR (
                          ts_norm(stopped.created_at) = ts_norm(prompted.next_created_at)
                          AND stopped.id < prompted.next_id
                      )
                  )
           ) THEN 0 ELSE 1 END) AS uncovered_count
      FROM prompt_turns AS prompted
),
stop_usage_counts AS (
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
      LEFT JOIN stop_usage_counts AS usage
        ON usage.session_id = stopped.session_id
),
headless_usage AS (
    SELECT COUNT(*) AS usage_count,
           SUM(CASE WHEN observation.total_state = 'known' THEN 1 ELSE 0 END) AS known_count,
           SUM(CASE WHEN observation.total_state = 'unavailable' THEN 1 ELSE 0 END) AS unavailable_count
      FROM usage_observations AS observation
      JOIN window_sessions AS session
        ON session.session_id = observation.session_id
     WHERE observation.host = 'codex'
       AND observation.status = 'finalized'
       AND observation.source_name = 'headless_stream'
       AND ts_norm(observation.observed_at) >= ts_norm(?)
       AND ts_norm(observation.observed_at) < ts_norm(?)
)
SELECT
    (SELECT COUNT(*) FROM window_events) AS stored_events,
    EXISTS(SELECT 1 FROM window_events WHERE kind = 'session_started') AS session_start_observed,
    EXISTS(SELECT 1 FROM prompt_turns) AS prompt_observed,
    COALESCE((SELECT prompt_count FROM final_turn_gaps), 0) AS prompt_count,
    EXISTS(SELECT 1 FROM window_events WHERE kind = 'command_executed') AS tool_observed,
    EXISTS(SELECT 1 FROM window_events WHERE kind = 'compact_summary') AS compact_observed,
    COALESCE((SELECT SUM(stop_count) FROM stop_usage), 0) AS stop_count,
    COALESCE((SELECT SUM(MIN(stop_count, usage_count)) FROM stop_usage), 0) AS covered_stop_count,
    COALESCE((SELECT uncovered_count FROM final_turn_gaps), 0) AS uncovered_final_turn_count,
    COALESCE((SELECT SUM(usage_count) FROM stop_usage), 0) + COALESCE((SELECT usage_count FROM headless_usage), 0) AS usage_count,
    COALESCE((SELECT usage_count FROM headless_usage), 0) AS headless_usage_count,
    COALESCE((SELECT SUM(known_count) FROM stop_usage), 0) + COALESCE((SELECT known_count FROM headless_usage), 0) AS known_count,
    COALESCE((SELECT SUM(unavailable_count) FROM stop_usage), 0) + COALESCE((SELECT unavailable_count FROM headless_usage), 0) AS unavailable_count;
