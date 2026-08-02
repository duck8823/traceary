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

-- Keep the normalized lifecycle row in the same canonical-mutation
-- transaction as the legacy singleton state. Recreate migration-38 triggers
-- so upgraded databases cannot expose a complete lifecycle for drifted data.
DROP TRIGGER search_projection_complete_event_update;
CREATE TRIGGER search_projection_complete_event_update AFTER UPDATE OF body,body_availability,session_id,kind,created_at ON events
WHEN (SELECT state FROM search_projection_state WHERE singleton=1)='complete'
BEGIN
 UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE singleton=1;
 UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1) AND state='complete';
END;
DROP TRIGGER search_projection_complete_event_delete;
CREATE TRIGGER search_projection_complete_event_delete BEFORE DELETE ON events
WHEN (SELECT state FROM search_projection_state WHERE singleton=1)='complete'
BEGIN
 UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE singleton=1;
 UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1) AND state='complete';
END;
DROP TRIGGER search_projection_complete_audit_update;
CREATE TRIGGER search_projection_complete_audit_update AFTER UPDATE ON command_audits
WHEN (SELECT state FROM search_projection_state WHERE singleton=1)='complete'
BEGIN
 UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE singleton=1;
 UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1) AND state='complete';
END;
DROP TRIGGER search_projection_complete_audit_insert;
CREATE TRIGGER search_projection_complete_audit_insert AFTER INSERT ON command_audits
WHEN (SELECT state FROM search_projection_state WHERE singleton=1)='complete'
 AND EXISTS(SELECT 1 FROM search_projection_source_sequence q,search_projection_state s WHERE q.event_id=new.event_id AND q.sequence<=s.high_water)
BEGIN
 UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE singleton=1;
 UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1) AND state='complete';
END;
DROP TRIGGER search_projection_complete_audit_delete;
CREATE TRIGGER search_projection_complete_audit_delete AFTER DELETE ON command_audits
WHEN (SELECT state FROM search_projection_state WHERE singleton=1)='complete'
BEGIN
 UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE singleton=1;
 UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1) AND state='complete';
END;
