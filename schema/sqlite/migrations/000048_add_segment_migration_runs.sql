-- Segment-specific durable construction/recovery journal.  It deliberately
-- carries no read-authority or Hot-eviction state.
ALTER TABLE archive_catalog_epochs ADD COLUMN transition_digest_version INTEGER NOT NULL DEFAULT 1 CHECK(transition_digest_version IN (1,2));

CREATE TABLE archive_segment_migration_runs (
    run_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK(revision>0),
    store_id TEXT NOT NULL CHECK(length(store_id)=32),
    reservation_id TEXT NOT NULL CHECK(length(reservation_id) BETWEEN 1 AND 255),
    plan_digest TEXT NOT NULL CHECK(length(plan_digest)=64),
    software_commit TEXT NOT NULL CHECK(length(software_commit) BETWEEN 1 AND 128),
    config_digest TEXT NOT NULL CHECK(length(config_digest)=64),
    envelope_page_rows INTEGER NOT NULL CHECK(envelope_page_rows>0),
    envelope_max_steps INTEGER NOT NULL CHECK(envelope_max_steps>0),
    envelope_wall_time_ns INTEGER NOT NULL CHECK(envelope_wall_time_ns>0),
    envelope_lock_time_ns INTEGER NOT NULL CHECK(envelope_lock_time_ns>0),
    envelope_max_plain_bytes INTEGER NOT NULL CHECK(envelope_max_plain_bytes>0),
    envelope_max_stored_bytes INTEGER NOT NULL CHECK(envelope_max_stored_bytes>0),
    envelope_max_value_plain_bytes INTEGER NOT NULL CHECK(envelope_max_value_plain_bytes>0),
    envelope_max_value_stored_bytes INTEGER NOT NULL CHECK(envelope_max_value_stored_bytes>0),
    envelope_max_file_bytes INTEGER NOT NULL CHECK(envelope_max_file_bytes>0),
    envelope_max_summary_bytes INTEGER NOT NULL CHECK(envelope_max_summary_bytes>0),
    envelope_max_wal_bytes INTEGER NOT NULL CHECK(envelope_max_wal_bytes>0),
    envelope_min_free_disk_bytes INTEGER NOT NULL CHECK(envelope_min_free_disk_bytes>0),
    envelope_max_summary_rows INTEGER NOT NULL CHECK(envelope_max_summary_rows>0),
    candidate_root TEXT NOT NULL,
    candidate_root_device TEXT NOT NULL,
    candidate_root_inode TEXT NOT NULL,
    archive_root TEXT NOT NULL,
    archive_root_device TEXT NOT NULL,
    archive_root_inode TEXT NOT NULL,
    compression_floor INTEGER NOT NULL CHECK(compression_floor>=0),
    start_sequence INTEGER NOT NULL CHECK(start_sequence>0),
    end_sequence INTEGER NOT NULL CHECK(end_sequence>=start_sequence),
    phase TEXT NOT NULL CHECK(phase IN ('planned','copying','candidate_built','install_intent','installed','seal_intent','sealed','verify_intent','verified_shadow','rollback_intent','rolled_back')),
    next_sequence INTEGER NOT NULL CHECK(next_sequence BETWEEN start_sequence AND end_sequence+1),
    copied_rows INTEGER NOT NULL CHECK(copied_rows=next_sequence-start_sequence),
    copied_plain_bytes INTEGER NOT NULL CHECK(copied_plain_bytes>=0),
    source_digest TEXT NOT NULL DEFAULT '',
    candidate_basename TEXT NOT NULL DEFAULT '',
    segment_id TEXT NOT NULL DEFAULT '',
    manifest_digest TEXT NOT NULL DEFAULT '',
    file_digest TEXT NOT NULL DEFAULT '',
    catalog_epoch INTEGER NOT NULL DEFAULT 0 CHECK(catalog_epoch>=0),
    recorded_at TEXT NOT NULL,
    PRIMARY KEY(run_id,revision)
);

CREATE TABLE archive_segment_migration_active (
    store_id TEXT PRIMARY KEY CHECK(length(store_id)=32),
    run_id TEXT NOT NULL UNIQUE,
    revision INTEGER NOT NULL CHECK(revision>0)
);

CREATE TABLE archive_segment_migration_pages (
    run_id TEXT NOT NULL,
    page_number INTEGER NOT NULL CHECK(page_number>=0),
    start_sequence INTEGER NOT NULL CHECK(start_sequence>0),
    end_sequence INTEGER NOT NULL CHECK(end_sequence>=start_sequence),
    rows_copied INTEGER NOT NULL CHECK(rows_copied=end_sequence-start_sequence+1),
    plain_bytes INTEGER NOT NULL CHECK(plain_bytes>0),
    page_digest TEXT NOT NULL CHECK(length(page_digest)=64),
    PRIMARY KEY(run_id,page_number),
    UNIQUE(run_id,start_sequence)
);

CREATE TABLE archive_segment_migration_install_intents (
    run_id TEXT PRIMARY KEY,
    candidate_basename TEXT NOT NULL CHECK(length(candidate_basename) BETWEEN 1 AND 255),
    final_basename TEXT NOT NULL CHECK(length(final_basename) BETWEEN 1 AND 255),
    file_digest TEXT NOT NULL CHECK(length(file_digest)=64),
    completed INTEGER NOT NULL DEFAULT 0 CHECK(completed IN (0,1)),
    created_at TEXT NOT NULL
);

CREATE TABLE archive_segment_migration_evidence (
    run_id TEXT PRIMARY KEY,
    schema_version TEXT NOT NULL CHECK(schema_version='traceary.segment-migration-evidence/v1'),
    evidence_digest TEXT NOT NULL CHECK(length(evidence_digest)=64),
    aggregate_json BLOB NOT NULL,
    recorded_at TEXT NOT NULL
);
CREATE TRIGGER archive_segment_migration_evidence_no_update BEFORE UPDATE ON archive_segment_migration_evidence BEGIN SELECT RAISE(ABORT,'segment migration evidence is immutable'); END;
CREATE TRIGGER archive_segment_migration_evidence_no_delete BEFORE DELETE ON archive_segment_migration_evidence BEGIN SELECT RAISE(ABORT,'segment migration evidence is immutable'); END;

-- A proof-specific adapter inserts an exact transaction-local authorization
-- before the Catalog transition and removes it before commit. Generic SQL that
-- merely mimics a state edge remains rejected by the trigger.
CREATE TABLE archive_catalog_transition_authorizations (
    epoch INTEGER NOT NULL,
    start_sequence INTEGER NOT NULL,
    end_sequence INTEGER NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    reservation_id TEXT NOT NULL,
    segment_id TEXT NOT NULL,
    proof_class TEXT NOT NULL CHECK(proof_class='segment_migration_v1'),
    PRIMARY KEY(epoch,start_sequence,end_sequence,from_state,to_state)
);

-- Owner preconditions are immutable CAS evidence. They are separate from the
-- v1 transition columns so their legacy resulting-owner shape remains stable.
CREATE TABLE archive_catalog_transition_owner_preconditions (
    epoch INTEGER NOT NULL REFERENCES archive_catalog_epochs(epoch),
    transition_index INTEGER NOT NULL,
    expected_reservation_id TEXT NOT NULL DEFAULT '',
    expected_segment_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(epoch,transition_index)
);

CREATE TRIGGER archive_segment_migration_runs_no_update BEFORE UPDATE ON archive_segment_migration_runs BEGIN SELECT RAISE(ABORT,'segment migration journal is append-only'); END;
CREATE TRIGGER archive_segment_migration_runs_no_delete BEFORE DELETE ON archive_segment_migration_runs BEGIN SELECT RAISE(ABORT,'segment migration journal is append-only'); END;
CREATE TRIGGER archive_segment_migration_pages_no_update BEFORE UPDATE ON archive_segment_migration_pages BEGIN SELECT RAISE(ABORT,'segment migration pages are immutable'); END;
CREATE TRIGGER archive_segment_migration_pages_no_delete BEFORE DELETE ON archive_segment_migration_pages BEGIN SELECT RAISE(ABORT,'segment migration pages are immutable'); END;
CREATE TRIGGER archive_catalog_transition_owner_preconditions_no_update BEFORE UPDATE ON archive_catalog_transition_owner_preconditions BEGIN SELECT RAISE(ABORT,'catalog transition owner preconditions are immutable'); END;
CREATE TRIGGER archive_catalog_transition_owner_preconditions_no_delete BEFORE DELETE ON archive_catalog_transition_owner_preconditions BEGIN SELECT RAISE(ABORT,'catalog transition owner preconditions are immutable'); END;

DROP TRIGGER archive_catalog_transitions_edge_guard;
CREATE TRIGGER archive_catalog_transitions_edge_guard
BEFORE INSERT ON archive_catalog_range_transitions
WHEN NOT (
    (NEW.from_state='hot' AND NEW.to_state='reserved' AND length(NEW.reservation_id)>0 AND NEW.segment_id='') OR
    (NEW.from_state='reserved' AND NEW.to_state='hot' AND length(NEW.reservation_id)>0 AND NEW.segment_id='') OR
    ((NEW.from_state='reserved' AND NEW.to_state='sealed' AND length(NEW.reservation_id)>0 AND length(NEW.segment_id)>0) OR
     (NEW.from_state='sealed' AND NEW.to_state='verified_shadow' AND NEW.reservation_id='' AND length(NEW.segment_id)>0) OR
     (NEW.from_state IN ('sealed','verified_shadow') AND NEW.to_state='reserved' AND length(NEW.reservation_id)>0 AND NEW.segment_id=''))
    AND EXISTS (SELECT 1 FROM archive_catalog_transition_authorizations a WHERE a.epoch=NEW.epoch AND a.start_sequence=NEW.start_sequence AND a.end_sequence=NEW.end_sequence AND a.from_state=NEW.from_state AND a.to_state=NEW.to_state AND a.reservation_id=NEW.reservation_id AND a.segment_id=NEW.segment_id AND a.proof_class='segment_migration_v1')
)
BEGIN SELECT RAISE(ABORT,'catalog transition requires a proof-specific port'); END;

-- The frozen source remains rollback material until #1652 owns authority.
DROP TRIGGER archive_reserved_events_no_update;
DROP TRIGGER archive_reserved_events_no_delete;
DROP TRIGGER archive_reserved_sequences_no_update;
DROP TRIGGER archive_reserved_sequences_no_delete;
DROP TRIGGER archive_reserved_audits_no_insert;
DROP TRIGGER archive_reserved_audits_no_update;
DROP TRIGGER archive_reserved_audits_no_delete;
CREATE TRIGGER archive_reserved_events_no_update BEFORE UPDATE ON events WHEN EXISTS (SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r ON s.sequence BETWEEN r.start_sequence AND r.end_sequence WHERE s.event_id=OLD.id AND r.placement_state IN ('reserved','sealed','verified_shadow')) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_events_no_delete BEFORE DELETE ON events WHEN EXISTS (SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r ON s.sequence BETWEEN r.start_sequence AND r.end_sequence WHERE s.event_id=OLD.id AND r.placement_state IN ('reserved','sealed','verified_shadow')) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_sequences_no_update BEFORE UPDATE ON archive_event_sequences WHEN EXISTS (SELECT 1 FROM archive_catalog_current_ranges r WHERE OLD.sequence BETWEEN r.start_sequence AND r.end_sequence AND r.placement_state IN ('reserved','sealed','verified_shadow')) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_sequences_no_delete BEFORE DELETE ON archive_event_sequences WHEN EXISTS (SELECT 1 FROM archive_catalog_current_ranges r WHERE OLD.sequence BETWEEN r.start_sequence AND r.end_sequence AND r.placement_state IN ('reserved','sealed','verified_shadow')) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_audits_no_insert BEFORE INSERT ON command_audits WHEN EXISTS (SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r ON s.sequence BETWEEN r.start_sequence AND r.end_sequence WHERE s.event_id=NEW.event_id AND r.placement_state IN ('reserved','sealed','verified_shadow')) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_audits_no_update BEFORE UPDATE ON command_audits WHEN EXISTS (SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r ON s.sequence BETWEEN r.start_sequence AND r.end_sequence WHERE s.event_id IN (OLD.event_id,NEW.event_id) AND r.placement_state IN ('reserved','sealed','verified_shadow')) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_audits_no_delete BEFORE DELETE ON command_audits WHEN EXISTS (SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r ON s.sequence BETWEEN r.start_sequence AND r.end_sequence WHERE s.event_id=OLD.event_id AND r.placement_state IN ('reserved','sealed','verified_shadow')) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
