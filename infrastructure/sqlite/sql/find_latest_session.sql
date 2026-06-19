WITH candidate_sessions AS (
     SELECT started.id,
            started.kind,
            started.client,
            started.agent,
            started.session_id,
            started.workspace,
            started.body,
            started.source_hook,
            started.created_at,
            (
              SELECT boundary.created_at
                FROM events boundary
               WHERE boundary.session_id = started.session_id
                 AND boundary.client = started.client
                 AND boundary.agent = started.agent
                 AND boundary.workspace = started.workspace
                 AND boundary.kind IN (?, ?)
               ORDER BY ts_norm(boundary.created_at) DESC, boundary.id DESC
               LIMIT 1
            ) AS latest_boundary_created_at,
            (
              SELECT boundary.id
                FROM events boundary
               WHERE boundary.session_id = started.session_id
                 AND boundary.client = started.client
                 AND boundary.agent = started.agent
                 AND boundary.workspace = started.workspace
                 AND boundary.kind IN (?, ?)
               ORDER BY ts_norm(boundary.created_at) DESC, boundary.id DESC
               LIMIT 1
            ) AS latest_boundary_id
       FROM events started
      WHERE started.kind = ?
        AND (? = '' OR started.client = ?)
        AND (? = '' OR started.agent = ?)
        AND (? = '' OR started.workspace = ?)
        AND NOT EXISTS (
             SELECT 1
               FROM events newer_started
              WHERE newer_started.kind = ?
                AND newer_started.session_id = started.session_id
                AND newer_started.client = started.client
                AND newer_started.agent = started.agent
                AND newer_started.workspace = started.workspace
                AND (
                     ts_norm(newer_started.created_at) > ts_norm(started.created_at) OR
                     (ts_norm(newer_started.created_at) = ts_norm(started.created_at) AND newer_started.id > started.id)
                )
        )
        AND (
             ? = 0 OR (
                 -- A session is active unless it is terminally ended by EITHER
                 -- signal, matching the CLI snapshot's row-based rule so MCP
                 -- session_status(action="active") and `sessions --snapshot`
                 -- agree on inclusion.
                 --
                 -- (1) Event signal: a session_ended event with no later event.
                 -- Late events (prompts, audits) after the end marker keep the
                 -- session active (ended_with_late_events).
                 NOT EXISTS (
                     SELECT 1
                       FROM events ended
                      WHERE ended.kind = ?
                        AND ended.session_id = started.session_id
                        AND ended.client = started.client
                        AND ended.agent = started.agent
                        AND ended.workspace = started.workspace
                        AND (
                             ts_norm(ended.created_at) > ts_norm(started.created_at) OR
                             (ts_norm(ended.created_at) = ts_norm(started.created_at) AND ended.id > started.id)
                        )
                        -- The "later event" check is session_id-only so it
                        -- matches list_sessions.sql's late-event rule exactly.
                        -- A same-session event from a different agent/workspace
                        -- after the end marker must keep the session active on
                        -- both surfaces (CLI snapshot and MCP active).
                        AND NOT EXISTS (
                             SELECT 1
                               FROM events later_ev
                              WHERE later_ev.session_id = started.session_id
                                AND (
                                     ts_norm(later_ev.created_at) > ts_norm(ended.created_at) OR
                                     (ts_norm(later_ev.created_at) = ts_norm(ended.created_at) AND later_ev.id > ended.id)
                                )
                        )
                 )
                 -- (2) Row signal: sessions.ended_at set with no later event.
                 -- Covers `session gc`, which writes ended_at directly without a
                 -- session_ended event; the CLI snapshot already excludes such a
                 -- session, so the active query must too.
                 AND NOT EXISTS (
                     SELECT 1
                       FROM sessions ended_row
                      WHERE ended_row.session_id = started.session_id
                        AND ended_row.ended_at IS NOT NULL
                        AND NOT EXISTS (
                             SELECT 1
                               FROM events row_later
                              WHERE row_later.session_id = started.session_id
                                AND ts_norm(row_later.created_at) > ts_norm(ended_row.ended_at)
                        )
                 )
             )
        )
)
SELECT id,
       kind,
       client,
       agent,
       session_id,
       workspace,
       body,
       source_hook,
       created_at
  FROM candidate_sessions
 ORDER BY ts_norm(CASE WHEN ? THEN created_at ELSE latest_boundary_created_at END) DESC,
          CASE WHEN ? THEN id ELSE latest_boundary_id END DESC
 LIMIT 1
