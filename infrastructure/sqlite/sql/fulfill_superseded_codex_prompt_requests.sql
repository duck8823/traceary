UPDATE consolidation_requests
   SET prompt_state = 'fulfilled',
       prompt_claim_token = NULL,
       prompt_claimed_at = NULL,
       prompt_lease_expires_at = NULL
 WHERE session_id = ?
   AND client = 'codex'
   AND delivery = 'none'
   AND refine_outcome IS NULL
   AND id < (
       SELECT id
         FROM consolidation_requests
        WHERE session_id = ?
        ORDER BY id DESC
        LIMIT 1
   );
