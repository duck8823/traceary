WITH latest_lexical AS (
    SELECT created_at FROM events WHERE workspace = ? ORDER BY created_at DESC LIMIT 1
), latest_second AS (
    SELECT substr(created_at, 1, 19) AS second_prefix FROM latest_lexical
), same_second_ids AS (
    SELECT e.id FROM latest_second s CROSS JOIN events e
     WHERE e.workspace = ? AND e.created_at = s.second_prefix || 'Z'
    UNION ALL
    SELECT e.id FROM latest_second s CROSS JOIN events e
     WHERE e.workspace = ? AND e.created_at >= s.second_prefix || '.' AND e.created_at <= s.second_prefix || 'Z'
)
SELECT e.kind, e.created_at
  FROM same_second_ids candidate
 CROSS JOIN events e ON e.id = candidate.id
 ORDER BY ts_norm(e.created_at) DESC, e.id DESC
 LIMIT 1
