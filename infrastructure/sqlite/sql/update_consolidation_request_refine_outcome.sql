UPDATE consolidation_requests
   SET refine_outcome = ?,
       refine_reason = ?,
       refine_produced_by = ?,
       refined_at = ?,
       refinement_generation = ?
 WHERE id = (
    SELECT id
      FROM consolidation_requests
     WHERE session_id = ?
       AND refine_outcome IS NULL
     ORDER BY id DESC
     LIMIT 1
 );
