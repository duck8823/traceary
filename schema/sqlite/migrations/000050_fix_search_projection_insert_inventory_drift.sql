-- Inserts during historical inventory must not bump source_revision.
-- The events insert trigger already registers the new identity into
-- search_projection_source_sequence unconditionally, so inventory has
-- nothing further to do for that row and the generation is not invalidated
-- by live writers (hooks) that open the store and append an event.
-- Membership (sequence <= high_water) remains the only insert-time drift
-- signal. Update/delete triggers keep requires_inventory=1: they can change
-- rows the inventory walk has not yet reached, with no membership info
-- available while inventory is still required.

DROP TRIGGER search_projection_events_insert;
CREATE TRIGGER search_projection_events_insert AFTER INSERT ON events BEGIN INSERT OR IGNORE INTO search_projection_source_sequence(event_id) VALUES(new.id); UPDATE search_projection_source_revision SET revision=revision+1 WHERE EXISTS(SELECT 1 FROM search_projection_state s WHERE s.state='rebuilding' AND EXISTS(SELECT 1 FROM search_projection_source_sequence q WHERE q.event_id=new.id AND q.sequence<=s.high_water)); END;

DROP TRIGGER search_projection_audits_insert;
CREATE TRIGGER search_projection_audits_insert AFTER INSERT ON command_audits WHEN EXISTS(SELECT 1 FROM search_projection_state s WHERE s.state='rebuilding' AND EXISTS(SELECT 1 FROM search_projection_source_sequence q WHERE q.event_id=new.event_id AND q.sequence<=s.high_water)) BEGIN UPDATE search_projection_source_revision SET revision=revision+1; END;
