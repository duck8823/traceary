UPDATE consolidation_requests
   SET refine_outcome = ?,
       refine_reason = ?,
       refine_produced_by = ?,
       refined_at = ?,
       refinement_generation = ?,
       prompt_state = CASE WHEN ? = 'accepted' AND delivery = 'none' THEN 'fulfilled' ELSE prompt_state END,
       prompt_claim_token = CASE WHEN ? = 'accepted' AND delivery = 'none' THEN NULL ELSE prompt_claim_token END,
       prompt_claimed_at = CASE WHEN ? = 'accepted' AND delivery = 'none' THEN NULL ELSE prompt_claimed_at END,
       prompt_lease_expires_at = CASE WHEN ? = 'accepted' AND delivery = 'none' THEN NULL ELSE prompt_lease_expires_at END
 WHERE id = (
       SELECT id
         FROM consolidation_requests
        WHERE session_id = ?
          AND refine_outcome IS NULL
        ORDER BY id DESC
        LIMIT 1
 );
