package sqlite

const epochZeroHookUsageObservedAt = "1970-01-01T00:00:00.000000000Z"

const epochZeroHookUsageSourceFilter = `
    source_name IN ('stop_hook', 'session_end_hook', 'after_agent_hook', 'headless_stream')`

const epochZeroHookUsageStampSQL = `COALESCE(
               (SELECT event.created_at
                  FROM events AS event
                 WHERE event.session_id = observation.session_id
                   AND event.kind IN ('session_ended', 'session_started')
                 ORDER BY ts_norm(event.created_at) DESC, event.id DESC
                 LIMIT 1),
               (SELECT event.created_at
                  FROM events AS event
                 WHERE event.session_id = observation.session_id
                 ORDER BY ts_norm(event.created_at) DESC, event.id DESC
                 LIMIT 1),
               (SELECT session.ended_at
                  FROM sessions AS session
                 WHERE session.session_id = observation.session_id
                   AND session.ended_at IS NOT NULL
                   AND length(trim(session.ended_at)) > 0),
               (SELECT session.started_at
                  FROM sessions AS session
                 WHERE session.session_id = observation.session_id)
           )`

const epochZeroHookUsageRepairableSQL = `
    ts_valid(trim(` + epochZeroHookUsageStampSQL + `)) = 1
    AND ts_norm(trim(` + epochZeroHookUsageStampSQL + `)) >= '1970-01-01T00:00:01.000000000Z'`
