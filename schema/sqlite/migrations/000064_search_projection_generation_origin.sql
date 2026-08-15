-- Record whether the live search-projection generation was started by
-- automatic catch-up or by an explicit operator start (#1861).
-- Existing rows whose ConfigHash is not the v0.36/v0.37 default were a
-- deliberate non-default budget and stay operator-owned. Matching or empty
-- hashes stay automatic so a later default change can replace them.
-- Do not bump capacity_semantics_version here: that would also replace
-- operator-owned --index-family-bytes generations (#1752).

ALTER TABLE search_projection_state ADD COLUMN origin TEXT NOT NULL DEFAULT 'automatic';

UPDATE search_projection_state
   SET origin='operator'
 WHERE config_hash <> ''
   AND config_hash <> 'v4:8388608:8388608:8388608:2592000000000000:1535115264';
