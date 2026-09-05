UPDATE events
   SET body = ?,
       body_availability = 'unavailable_retention',
       body_pruned_at = ?,
       body_pruned_plan_id = NULL
 WHERE id IN (
-- discardable-event-bodies
)
