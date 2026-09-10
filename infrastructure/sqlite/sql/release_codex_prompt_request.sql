UPDATE consolidation_requests
   SET prompt_state = 'pending',
       prompt_claim_token = NULL,
       prompt_claimed_at = NULL,
       prompt_lease_expires_at = NULL
 WHERE prompt_request_id = ?
   AND prompt_claim_token = ?
   AND prompt_state = 'claimed';
