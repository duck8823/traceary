-- Explicit list_events compatibility fallback. The caller supplies only IDs
-- that the bounded projection classified as canonical and response-untruncated,
-- then revalidates the decoded envelope before attaching body_blocks.
-- Body bytes are loaded via loadEventPlaintext, not this projection (#1685 D6).
SELECT e.id
  FROM events e
 WHERE e.body_availability = 'available'
   AND e.id IN (SELECT CAST(value AS TEXT) FROM json_each(?));
