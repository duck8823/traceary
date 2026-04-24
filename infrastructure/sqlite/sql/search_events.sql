-- Transcript / prompt bodies from v0.8.1+ are JSON envelopes that
-- separate thinking blocks from user-visible text blocks (see #662).
-- Matching raw body would let a "needle" inside a thinking block
-- surface as a search hit that then renders empty via ExtractPlainBody
-- (confusing UX and effectively leaking internal reasoning into the
-- search surface). Strip thinking before matching: when body parses as
-- a canonical envelope, match only the joined text-block content;
-- otherwise keep the raw body (legacy plain text, non-envelope JSON).
SELECT DISTINCT e.id, e.kind, e.client, e.agent, e.session_id, e.workspace, e.body, e.source_hook, e.created_at
  FROM events e
  LEFT JOIN command_audits a ON a.event_id = e.id
 WHERE (? = '' OR
        (CASE WHEN json_valid(e.body) AND json_type(e.body, '$.blocks') = 'array'
              THEN COALESCE(
                     (SELECT group_concat(json_extract(value, '$.text'), X'0A0A')
                        FROM json_each(json_extract(e.body, '$.blocks'))
                       WHERE json_extract(value, '$.type') = 'text'),
                     '')
              ELSE e.body
         END) LIKE ? ESCAPE '\' OR
        COALESCE(a.command_text, '') LIKE ? ESCAPE '\' OR
        COALESCE(a.input_text, '') LIKE ? ESCAPE '\' OR
        COALESCE(a.output_text, '') LIKE ? ESCAPE '\')
   AND (? = '' OR e.workspace = ?)
   AND (? = '' OR e.session_id = ?)
   AND (? = '' OR e.client = ?)
   AND (? = '' OR e.agent = ?)
   AND (? = '' OR e.kind = ?)
   AND (? = '' OR e.created_at >= ?)
   AND (? = '' OR e.created_at < ?)
   AND (? = 0 OR (a.exit_code IS NOT NULL AND a.exit_code != 0))
 ORDER BY e.created_at DESC, e.id DESC
 LIMIT ? OFFSET ?
