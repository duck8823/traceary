SELECT o.session_id,
       o.from_event_id,
       o.to_event_id,
       o.observed_at,
       (SELECT e.created_at
          FROM events AS e
         WHERE e.session_id = o.session_id
           AND NULLIF(e.created_at, '') IS NOT NULL
           AND (
                 o.from_event_id = ''
                 OR e.created_at_norm > (
                      SELECT e2.created_at_norm
                        FROM events AS e2
                       WHERE e2.id = o.from_event_id
                         AND e2.session_id = o.session_id
                    )
               )
         ORDER BY e.created_at_norm ASC, e.id ASC
         LIMIT 1) AS earliest_event_time
  FROM session_orphan_ranges AS o
 ORDER BY session_id ASC, to_event_id ASC
