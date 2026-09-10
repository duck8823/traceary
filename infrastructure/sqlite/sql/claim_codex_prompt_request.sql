UPDATE consolidation_requests
   SET prompt_state = 'claimed',
       prompt_claim_token = ?,
       prompt_claimed_at = ?,
       prompt_lease_expires_at = ?
 WHERE id = (
       SELECT id
         FROM consolidation_requests
        WHERE session_id = ?
          AND client = 'codex'
          AND delivery = 'none'
          AND refine_outcome IS NULL
          AND (prompt_state = 'pending' OR (prompt_state = 'claimed' AND prompt_lease_expires_at <= ?))
        ORDER BY id DESC
        LIMIT 1
 )
 RETURNING session_id, client, requested_at, at_event_id, signal, pressure_value, threshold_value, re_request, delivery, prompt_request_id;
