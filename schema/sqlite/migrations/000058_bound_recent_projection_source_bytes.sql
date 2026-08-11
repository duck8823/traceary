-- Persist the exact source-text bytes resident in the recent tier. Any
-- in-flight generation predates this invariant and is derived, so abandon it
-- rather than guessing a backfill total.
ALTER TABLE search_projection_state ADD COLUMN recent_source_bytes INTEGER NOT NULL DEFAULT 0;

UPDATE search_projection_generation_lifecycle
SET state='abandoned', abandoned_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1)
  AND state='rebuilding';

UPDATE literal_search_projection_state
SET state='stale', updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE singleton=1
  AND generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1);

UPDATE search_projection_state
SET generation_id=NULL, active_generation_id=NULL, config_hash='', source_revision=0,
    high_water=0, checkpoint=0, phase='source', cleanup_scope='old',
    failure_class='', state='idle', recent_source_bytes=0,
    updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE singleton=1 AND state='rebuilding';
