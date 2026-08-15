-- Search-projection invalidators must watch every events column the decoder
-- reads. A codec-metadata-only UPDATE used to leave fingerprints describing
-- a superseded decode (silent false negative). Constant-time DROP/CREATE only.
-- events.id is immutable rather than watched: changing it would orphan
-- search_projection_source_sequence.event_id without a joinable events row.

DROP TRIGGER search_projection_complete_event_update;
CREATE TRIGGER search_projection_complete_event_update AFTER UPDATE OF body,body_availability,session_id,kind,created_at,body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256 ON events
WHEN (SELECT state FROM search_projection_state WHERE singleton=1)='complete'
BEGIN
 UPDATE search_projection_state SET state='drifted',phase='cleanup',cleanup_scope='all',active_generation_id=NULL WHERE singleton=1;
 UPDATE search_projection_generation_lifecycle SET state='drifted' WHERE generation_id=(SELECT generation_id FROM search_projection_state WHERE singleton=1) AND state='complete';
END;

DROP TRIGGER search_projection_events_update;
CREATE TRIGGER search_projection_events_update AFTER UPDATE OF body,body_availability,session_id,kind,created_at,body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256 ON events WHEN EXISTS(SELECT 1 FROM search_projection_state s WHERE s.state='rebuilding' AND (EXISTS(SELECT 1 FROM search_projection_inventory_compat c WHERE c.requires_inventory=1) OR EXISTS(SELECT 1 FROM search_projection_source_sequence q WHERE q.event_id=new.id AND q.sequence<=s.high_water))) BEGIN UPDATE search_projection_source_revision SET revision=revision+1; END;

DROP TRIGGER literal_search_event_update;
CREATE TRIGGER literal_search_event_update AFTER UPDATE OF workspace,client,agent,session_id,kind,created_at,body,body_availability,body_codec,body_format_version,body_plaintext_bytes,body_encoded_bytes,body_sha256 ON events BEGIN
 UPDATE literal_search_projection_state SET query_revision=query_revision+1,state='stale',updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE singleton=1;
END;

CREATE TRIGGER events_id_immutable BEFORE UPDATE OF id ON events
BEGIN
 SELECT RAISE(ABORT, 'events.id is immutable');
END;
