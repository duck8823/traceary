-- command_executed payloads were persisted twice: once as events.body
-- (command + INPUT + OUTPUT) and once as command_audits columns. Measured
-- live-store ratio is 1.00 — pure duplication. command_audits is the retained
-- execution record; search indexes those columns independently of body.
--
-- This rewrite visits every command_executed row and clears the envelope body
-- (and identity codec metadata when present). Classified data_dependent_offline
-- because cost scales with historical audit volume, like 000035/000045.
--
-- Empty identity SHA-256 is the digest of the empty string so later payload
-- integrity checks remain consistent with encodePayload(..., identity).
UPDATE events
   SET body = '',
       body_codec = CASE WHEN body_codec IS NOT NULL THEN 'identity' ELSE body_codec END,
       body_format_version = CASE WHEN body_format_version IS NOT NULL THEN 1 ELSE body_format_version END,
       body_plaintext_bytes = CASE WHEN body_plaintext_bytes IS NOT NULL THEN 0 ELSE body_plaintext_bytes END,
       body_encoded_bytes = CASE WHEN body_encoded_bytes IS NOT NULL THEN 0 ELSE body_encoded_bytes END,
       body_sha256 = CASE
           WHEN body_sha256 IS NOT NULL
           THEN 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
           ELSE body_sha256
       END
 WHERE kind = 'command_executed'
   AND (
        length(CAST(body AS BLOB)) > 0
     OR COALESCE(body_plaintext_bytes, 0) > 0
     OR COALESCE(body_encoded_bytes, 0) > 0
   );
