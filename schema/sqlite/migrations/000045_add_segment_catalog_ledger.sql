-- The Segment Catalog ledger is installed without reading canonical history.
-- Authority is carried only by immutable epochs and transitions; current
-- ranges are a derived cache that may be rebuilt from the ledger.
CREATE TABLE archive_catalog_head (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    current_epoch INTEGER NOT NULL CHECK (current_epoch >= 0),
    ledger_digest TEXT NOT NULL CHECK (length(ledger_digest) = 64),
    current_ranges_digest TEXT NOT NULL CHECK (length(current_ranges_digest) = 64)
);

INSERT INTO archive_catalog_head(singleton, current_epoch, ledger_digest, current_ranges_digest)
VALUES (1, 0,
        '0000000000000000000000000000000000000000000000000000000000000000',
        '0000000000000000000000000000000000000000000000000000000000000000');

CREATE TABLE archive_catalog_epochs (
    epoch INTEGER PRIMARY KEY CHECK (epoch > 0),
    parent_epoch INTEGER NOT NULL CHECK (parent_epoch >= 0 AND parent_epoch < epoch),
    transition_digest TEXT NOT NULL CHECK (length(transition_digest) = 64),
    evidence_digest TEXT NOT NULL CHECK (length(evidence_digest) = 64),
    parent_ledger_digest TEXT NOT NULL CHECK (length(parent_ledger_digest) = 64),
    source_high_water INTEGER NOT NULL CHECK (source_high_water > 0),
    boundary_count INTEGER NOT NULL CHECK (boundary_count >= 1),
    boundary_digest TEXT NOT NULL CHECK (length(boundary_digest) = 64),
    ledger_digest TEXT NOT NULL UNIQUE CHECK (length(ledger_digest) = 64),
    committed_at TEXT NOT NULL
);

CREATE TABLE archive_catalog_segment_bindings (
    segment_id TEXT PRIMARY KEY CHECK (length(segment_id) BETWEEN 1 AND 255),
    store_id TEXT NOT NULL CHECK (length(store_id) = 32),
    start_sequence INTEGER NOT NULL CHECK (start_sequence > 0),
    end_sequence INTEGER NOT NULL CHECK (end_sequence >= start_sequence),
    format_version INTEGER NOT NULL CHECK (format_version > 0),
    manifest_version INTEGER NOT NULL CHECK (manifest_version > 0),
    summary_version INTEGER NOT NULL CHECK (summary_version > 0),
    logical_digest TEXT NOT NULL CHECK (length(logical_digest) = 64),
    file_digest TEXT NOT NULL CHECK (length(file_digest) = 64),
    manifest_digest TEXT NOT NULL CHECK (length(manifest_digest) = 64),
    summary_digest TEXT NOT NULL CHECK (length(summary_digest) = 64),
    relative_basename TEXT NOT NULL CHECK (
        length(relative_basename) BETWEEN 1 AND 255
        AND instr(relative_basename, '/') = 0
        AND instr(relative_basename, char(92)) = 0
        AND relative_basename NOT IN ('.', '..')
    ),
    storage_class TEXT NOT NULL CHECK (storage_class IN ('sealed_sqlite_zstd_v1')),
    bound_epoch INTEGER NOT NULL REFERENCES archive_catalog_epochs(epoch),
    UNIQUE(store_id, start_sequence, end_sequence, logical_digest)
);

CREATE TABLE archive_catalog_range_transitions (
    epoch INTEGER NOT NULL REFERENCES archive_catalog_epochs(epoch),
    transition_index INTEGER NOT NULL CHECK (transition_index >= 0),
    start_sequence INTEGER NOT NULL CHECK (start_sequence > 0),
    end_sequence INTEGER NOT NULL CHECK (end_sequence >= start_sequence),
    from_state TEXT NOT NULL CHECK (from_state IN
        ('hot', 'reserved', 'sealed', 'verified_shadow', 'segment_authoritative', 'evicting', 'cold')),
    to_state TEXT NOT NULL CHECK (to_state IN
        ('hot', 'reserved', 'sealed', 'verified_shadow', 'segment_authoritative', 'evicting', 'cold')),
    reservation_id TEXT NOT NULL DEFAULT '',
    segment_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(epoch, transition_index),
    CHECK (from_state <> to_state),
    CHECK ((to_state = 'reserved' OR from_state = 'reserved') = (length(reservation_id) > 0))
);

CREATE INDEX idx_archive_catalog_transitions_epoch_range
    ON archive_catalog_range_transitions(epoch, start_sequence, end_sequence);
CREATE INDEX idx_archive_catalog_transitions_range_epoch
    ON archive_catalog_range_transitions(start_sequence, end_sequence, epoch DESC);
CREATE INDEX idx_archive_catalog_transitions_latest_covering
    ON archive_catalog_range_transitions(epoch DESC, start_sequence, end_sequence);

-- Boundary facts make an old epoch reconstructable by one indexed latest
-- transition lookup per elementary range, without replaying every epoch.
CREATE TABLE archive_catalog_boundaries (
    sequence INTEGER PRIMARY KEY CHECK (sequence > 0),
    first_epoch INTEGER NOT NULL CHECK (first_epoch >= 0)
);
INSERT INTO archive_catalog_boundaries(sequence, first_epoch) VALUES(1, 0);

CREATE TABLE archive_catalog_reservation_deltas (
    epoch INTEGER NOT NULL REFERENCES archive_catalog_epochs(epoch),
    reservation_id TEXT NOT NULL CHECK (length(reservation_id) BETWEEN 1 AND 255),
    delta TEXT NOT NULL CHECK (delta IN ('reserve', 'release')),
    start_sequence INTEGER NOT NULL CHECK (start_sequence > 0),
    end_sequence INTEGER NOT NULL CHECK (end_sequence >= start_sequence),
    PRIMARY KEY(epoch, reservation_id)
);

CREATE INDEX idx_archive_catalog_reservation_history
    ON archive_catalog_reservation_deltas(reservation_id, epoch);
CREATE UNIQUE INDEX idx_archive_catalog_one_reserve_per_id
    ON archive_catalog_reservation_deltas(reservation_id) WHERE delta='reserve';
CREATE UNIQUE INDEX idx_archive_catalog_one_release_per_id
    ON archive_catalog_reservation_deltas(reservation_id) WHERE delta='release';

CREATE TABLE archive_catalog_current_ranges (
    start_sequence INTEGER PRIMARY KEY CHECK (start_sequence > 0),
    end_sequence INTEGER NOT NULL CHECK (end_sequence >= start_sequence),
    placement_state TEXT NOT NULL CHECK (placement_state IN
        ('hot', 'reserved', 'sealed', 'verified_shadow', 'segment_authoritative', 'evicting', 'cold')),
    reservation_id TEXT NOT NULL DEFAULT '',
    segment_id TEXT NOT NULL DEFAULT '',
    source_epoch INTEGER NOT NULL CHECK (source_epoch >= 0),
    UNIQUE(end_sequence)
);

CREATE INDEX idx_archive_catalog_current_end
    ON archive_catalog_current_ranges(end_sequence, start_sequence);

CREATE TRIGGER archive_catalog_epochs_no_update
BEFORE UPDATE ON archive_catalog_epochs BEGIN SELECT RAISE(ABORT, 'catalog epochs are append-only'); END;
CREATE TRIGGER archive_catalog_epochs_no_delete
BEFORE DELETE ON archive_catalog_epochs BEGIN SELECT RAISE(ABORT, 'catalog epochs are append-only'); END;
CREATE TRIGGER archive_catalog_transitions_no_update
BEFORE UPDATE ON archive_catalog_range_transitions BEGIN SELECT RAISE(ABORT, 'catalog transitions are append-only'); END;
CREATE TRIGGER archive_catalog_transitions_no_delete
BEFORE DELETE ON archive_catalog_range_transitions BEGIN SELECT RAISE(ABORT, 'catalog transitions are append-only'); END;
CREATE TRIGGER archive_catalog_reservations_no_update
BEFORE UPDATE ON archive_catalog_reservation_deltas BEGIN SELECT RAISE(ABORT, 'catalog reservations are append-only'); END;
CREATE TRIGGER archive_catalog_reservations_no_delete
BEFORE DELETE ON archive_catalog_reservation_deltas BEGIN SELECT RAISE(ABORT, 'catalog reservations are append-only'); END;
CREATE TRIGGER archive_catalog_bindings_no_update
BEFORE UPDATE ON archive_catalog_segment_bindings BEGIN SELECT RAISE(ABORT, 'catalog segment bindings are immutable'); END;
CREATE TRIGGER archive_catalog_bindings_no_delete
BEFORE DELETE ON archive_catalog_segment_bindings BEGIN SELECT RAISE(ABORT, 'catalog segment bindings are immutable'); END;
CREATE TRIGGER archive_catalog_boundaries_no_update
BEFORE UPDATE ON archive_catalog_boundaries BEGIN SELECT RAISE(ABORT, 'catalog boundaries are append-only'); END;
CREATE TRIGGER archive_catalog_boundaries_no_delete
BEFORE DELETE ON archive_catalog_boundaries BEGIN SELECT RAISE(ABORT, 'catalog boundaries are append-only'); END;

CREATE TRIGGER archive_catalog_transitions_edge_guard
BEFORE INSERT ON archive_catalog_range_transitions
WHEN NOT (
    (NEW.from_state='hot' AND NEW.to_state='reserved' AND length(NEW.reservation_id)>0 AND NEW.segment_id='')
    OR
    (NEW.from_state='reserved' AND NEW.to_state='hot' AND length(NEW.reservation_id)>0 AND NEW.segment_id='')
)
BEGIN SELECT RAISE(ABORT, 'catalog transition requires a proof-specific port'); END;
