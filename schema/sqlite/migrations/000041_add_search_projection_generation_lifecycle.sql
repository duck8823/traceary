CREATE TABLE search_projection_generation_lifecycle(
 generation_id TEXT PRIMARY KEY,
 state TEXT NOT NULL CHECK(state IN('rebuilding','complete','failed','drifted','abandoned')),
 config_hash TEXT NOT NULL,
 source_revision INTEGER NOT NULL,
 high_water INTEGER NOT NULL,
 abandoned_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO search_projection_generation_lifecycle(generation_id,state,config_hash,source_revision,high_water)
SELECT generation_id,CASE state WHEN 'complete' THEN 'complete' WHEN 'failed' THEN 'failed' WHEN 'drifted' THEN 'drifted' ELSE 'rebuilding' END,config_hash,source_revision,high_water
FROM search_projection_state WHERE generation_id IS NOT NULL AND generation_id<>'';
