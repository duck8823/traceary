UPDATE events
   SET body = ?,
       body_codec = ?,
       body_format_version = ?,
       body_plaintext_bytes = ?,
       body_encoded_bytes = ?,
       body_sha256 = ?,
       body_availability = 'unavailable_retention',
       body_pruned_at = ?,
       body_pruned_plan_id = NULL
 WHERE id IN (
-- discardable-event-bodies
)
