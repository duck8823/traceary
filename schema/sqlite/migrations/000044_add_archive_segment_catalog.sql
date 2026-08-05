-- The archive segment catalog is a lightweight registry of sealed immutable
-- segment files. A row is inserted only after the referenced file is durably
-- sealed and verified; rows carry no Hot authority and no workflow state.
-- Overlapping ranges are allowed because archive processing is at-least-once;
-- readers deduplicate overlapping Hot/segment records by stable event ID.
CREATE TABLE archive_segments (
    basename TEXT PRIMARY KEY CHECK (
        length(basename) = 82
        AND basename GLOB 'segment-v1-*.sqlite'
        AND instr(basename, '/') = 0
        AND instr(basename, char(92)) = 0
    ),
    store_id TEXT NOT NULL CHECK (length(store_id) > 0),
    format_version INTEGER NOT NULL CHECK (format_version > 0),
    start_sequence INTEGER NOT NULL CHECK (start_sequence > 0),
    end_sequence INTEGER NOT NULL CHECK (end_sequence >= start_sequence),
    unit_count INTEGER NOT NULL CHECK (unit_count = end_sequence - start_sequence + 1),
    audit_count INTEGER NOT NULL CHECK (audit_count >= 0 AND audit_count <= unit_count),
    min_created_at TEXT NOT NULL,
    max_created_at TEXT NOT NULL,
    time_complete INTEGER NOT NULL CHECK (time_complete IN (0, 1)),
    plain_value_count INTEGER NOT NULL CHECK (plain_value_count >= 0),
    zstd_value_count INTEGER NOT NULL CHECK (zstd_value_count >= 0),
    total_plain_bytes INTEGER NOT NULL CHECK (total_plain_bytes >= 0),
    total_stored_bytes INTEGER NOT NULL CHECK (total_stored_bytes >= 0),
    logical_digest TEXT NOT NULL CHECK (length(logical_digest) = 64),
    file_digest TEXT NOT NULL CHECK (length(file_digest) = 64),
    CHECK ((min_created_at = '') = (max_created_at = '')),
    CHECK (time_complete = 0 OR min_created_at <> '')
);

CREATE INDEX idx_archive_segments_range
    ON archive_segments(start_sequence, end_sequence);
