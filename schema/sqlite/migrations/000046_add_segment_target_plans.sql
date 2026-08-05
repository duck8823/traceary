-- Durable target plans bind a reservation to its exact source snapshot,
-- policy, ordered canonical input, and any measured shortening proof.
CREATE TABLE archive_segment_target_plans (
    reservation_id TEXT PRIMARY KEY CHECK(length(reservation_id) BETWEEN 1 AND 255),
    bound_epoch INTEGER NOT NULL UNIQUE REFERENCES archive_catalog_epochs(epoch),
    store_id TEXT NOT NULL CHECK(length(store_id)=32),
    catalog_parent_epoch INTEGER NOT NULL CHECK(catalog_parent_epoch>=0),
    catalog_parent_ledger_digest TEXT NOT NULL CHECK(length(catalog_parent_ledger_digest)=64),
    catalog_parent_ranges_digest TEXT NOT NULL CHECK(length(catalog_parent_ranges_digest)=64),
    captured_high_water INTEGER NOT NULL CHECK(captured_high_water>0),
    captured_at TEXT NOT NULL,
    hot_horizon_ns INTEGER NOT NULL CHECK(hot_horizon_ns>0),
    max_rows INTEGER NOT NULL CHECK(max_rows>0),
    max_canonical_plain_bytes INTEGER NOT NULL CHECK(max_canonical_plain_bytes>0),
    max_decoded_bytes INTEGER NOT NULL CHECK(max_decoded_bytes>0),
    max_stored_upper_bytes INTEGER NOT NULL CHECK(max_stored_upper_bytes>0),
    max_file_bytes INTEGER NOT NULL CHECK(max_file_bytes>0),
    stored_bound_version INTEGER NOT NULL CHECK(stored_bound_version>0),
    start_sequence INTEGER NOT NULL CHECK(start_sequence>0),
    end_sequence INTEGER NOT NULL CHECK(end_sequence>=start_sequence),
    selected_rows INTEGER NOT NULL CHECK(selected_rows=end_sequence-start_sequence+1),
    canonical_plain_bytes INTEGER NOT NULL CHECK(canonical_plain_bytes>0),
    decoded_bytes INTEGER NOT NULL CHECK(decoded_bytes>=0),
    stored_upper_bytes INTEGER NOT NULL CHECK(stored_upper_bytes>0),
    source_digest TEXT NOT NULL CHECK(length(source_digest)=64),
    plan_digest TEXT NOT NULL UNIQUE CHECK(length(plan_digest)=64),
    reservation_evidence_digest TEXT NOT NULL CHECK(length(reservation_evidence_digest)=64),
    retry_previous_reservation_id TEXT NOT NULL DEFAULT '',
    retry_failure_class TEXT NOT NULL DEFAULT '' CHECK(retry_failure_class IN ('','stored_cap','file_cap')),
    retry_measured_bytes INTEGER NOT NULL DEFAULT 0 CHECK(retry_measured_bytes>=0),
    retry_failed_cap_bytes INTEGER NOT NULL DEFAULT 0 CHECK(retry_failed_cap_bytes>=0),
    retry_evidence_digest TEXT NOT NULL DEFAULT '',
    CHECK ((length(retry_previous_reservation_id)>0) = (length(retry_failure_class)>0)),
    CHECK ((length(retry_previous_reservation_id)>0) = (retry_measured_bytes>0)),
    CHECK ((length(retry_previous_reservation_id)>0) = (retry_failed_cap_bytes>0)),
    CHECK ((length(retry_previous_reservation_id)>0) = (length(retry_evidence_digest)=64))
);

CREATE TABLE archive_segment_target_plan_units (
    reservation_id TEXT NOT NULL REFERENCES archive_segment_target_plans(reservation_id),
    sequence INTEGER NOT NULL CHECK(sequence>0),
    canonical_bytes INTEGER NOT NULL CHECK(canonical_bytes>0),
    canonical_digest TEXT NOT NULL CHECK(length(canonical_digest)=64),
    PRIMARY KEY(reservation_id,sequence)
);

CREATE TRIGGER archive_segment_target_plans_no_update
BEFORE UPDATE ON archive_segment_target_plans BEGIN SELECT RAISE(ABORT,'segment target plans are immutable'); END;
CREATE TRIGGER archive_segment_target_plans_no_delete
BEFORE DELETE ON archive_segment_target_plans BEGIN SELECT RAISE(ABORT,'segment target plans are immutable'); END;
CREATE TRIGGER archive_segment_target_plan_units_no_update
BEFORE UPDATE ON archive_segment_target_plan_units BEGIN SELECT RAISE(ABORT,'segment target plan units are immutable'); END;
CREATE TRIGGER archive_segment_target_plan_units_no_delete
BEFORE DELETE ON archive_segment_target_plan_units BEGIN SELECT RAISE(ABORT,'segment target plan units are immutable'); END;

-- A reserved range is a durable source freeze. Late audit creation is a source
-- mutation just like event/audit update or delete.
CREATE TRIGGER archive_reserved_events_no_update
BEFORE UPDATE ON events WHEN EXISTS (
    SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r
      ON s.sequence BETWEEN r.start_sequence AND r.end_sequence
     WHERE s.event_id=OLD.id AND r.placement_state='reserved'
) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_events_no_delete
BEFORE DELETE ON events WHEN EXISTS (
    SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r
      ON s.sequence BETWEEN r.start_sequence AND r.end_sequence
     WHERE s.event_id=OLD.id AND r.placement_state='reserved'
) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_sequences_no_update
BEFORE UPDATE ON archive_event_sequences WHEN EXISTS (
    SELECT 1 FROM archive_catalog_current_ranges r
     WHERE OLD.sequence BETWEEN r.start_sequence AND r.end_sequence
       AND r.placement_state='reserved'
) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_sequences_no_delete
BEFORE DELETE ON archive_event_sequences WHEN EXISTS (
    SELECT 1 FROM archive_catalog_current_ranges r
     WHERE OLD.sequence BETWEEN r.start_sequence AND r.end_sequence
       AND r.placement_state='reserved'
) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_audits_no_insert
BEFORE INSERT ON command_audits WHEN EXISTS (
    SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r
      ON s.sequence BETWEEN r.start_sequence AND r.end_sequence
     WHERE s.event_id=NEW.event_id AND r.placement_state='reserved'
) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_audits_no_update
BEFORE UPDATE ON command_audits WHEN EXISTS (
    SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r
      ON s.sequence BETWEEN r.start_sequence AND r.end_sequence
     WHERE s.event_id IN (OLD.event_id,NEW.event_id) AND r.placement_state='reserved'
) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
CREATE TRIGGER archive_reserved_audits_no_delete
BEFORE DELETE ON command_audits WHEN EXISTS (
    SELECT 1 FROM archive_event_sequences s JOIN archive_catalog_current_ranges r
      ON s.sequence BETWEEN r.start_sequence AND r.end_sequence
     WHERE s.event_id=OLD.event_id AND r.placement_state='reserved'
) BEGIN SELECT RAISE(ABORT,'reserved archive history is immutable'); END;
