-- Reconciled summary metadata is a disposable, payload-free cache. The
-- append-only Catalog ledger and immutable segment bindings remain authority.
CREATE TABLE archive_catalog_summary_segments (
    segment_id TEXT PRIMARY KEY REFERENCES archive_catalog_segment_bindings(segment_id),
    bound_epoch INTEGER NOT NULL CHECK(bound_epoch > 0),
    summary_version INTEGER NOT NULL CHECK(summary_version > 0),
    filter_key_id TEXT NOT NULL CHECK(length(filter_key_id) BETWEEN 1 AND 255),
    time_summary_complete INTEGER NOT NULL CHECK(time_summary_complete IN (0,1)),
    min_created_at TEXT NOT NULL,
    max_created_at TEXT NOT NULL,
    unit_count INTEGER NOT NULL CHECK(unit_count > 0),
    audit_count INTEGER NOT NULL CHECK(audit_count >= 0),
    plain_value_count INTEGER NOT NULL CHECK(plain_value_count >= 0),
    zstd_value_count INTEGER NOT NULL CHECK(zstd_value_count >= 0),
    total_plain_bytes INTEGER NOT NULL CHECK(total_plain_bytes >= 0),
    total_stored_bytes INTEGER NOT NULL CHECK(total_stored_bytes >= 0),
    summary_row_count INTEGER NOT NULL CHECK(summary_row_count >= 0),
    summary_byte_count INTEGER NOT NULL CHECK(summary_byte_count > 0),
    summary_digest TEXT NOT NULL CHECK(length(summary_digest)=64),
    reconciled_at TEXT NOT NULL
);

CREATE TABLE archive_catalog_summary_exact (
    segment_id TEXT NOT NULL REFERENCES archive_catalog_summary_segments(segment_id) ON DELETE CASCADE,
    kind INTEGER NOT NULL CHECK(kind BETWEEN 1 AND 5),
    token BLOB NOT NULL CHECK(length(token)=32),
    PRIMARY KEY(segment_id,kind,token)
);
CREATE TABLE archive_catalog_summary_blooms (
    segment_id TEXT NOT NULL REFERENCES archive_catalog_summary_segments(segment_id) ON DELETE CASCADE,
    kind INTEGER NOT NULL CHECK(kind BETWEEN 1 AND 5),
    bit_count INTEGER NOT NULL CHECK(bit_count > 0 AND bit_count <= 8388608),
    hash_count INTEGER NOT NULL CHECK(hash_count BETWEEN 1 AND 16),
    bits BLOB NOT NULL CHECK(length(bits)*8=bit_count),
    PRIMARY KEY(segment_id,kind)
);
CREATE TABLE archive_catalog_summary_sessions (
    segment_id TEXT NOT NULL REFERENCES archive_catalog_summary_segments(segment_id) ON DELETE CASCADE,
    session_token BLOB NOT NULL CHECK(length(session_token)=32),
    unit_count INTEGER NOT NULL CHECK(unit_count > 0),
    audit_count INTEGER NOT NULL CHECK(audit_count >= 0),
    PRIMARY KEY(segment_id,session_token)
);

CREATE INDEX idx_archive_catalog_summary_epoch ON archive_catalog_summary_segments(bound_epoch,segment_id);

-- Both cursors are internal and durable. Public evidence reports counts only.
CREATE TABLE archive_catalog_summary_rebuild_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    expected_epoch INTEGER NOT NULL CHECK(expected_epoch>=0),
    expected_ledger_digest TEXT NOT NULL CHECK(length(expected_ledger_digest)=64),
    audit_epoch INTEGER NOT NULL CHECK(audit_epoch>=0),
    audit_ledger_digest TEXT NOT NULL CHECK(length(audit_ledger_digest)=64),
    cache_bound_epoch INTEGER NOT NULL CHECK(cache_bound_epoch>=0),
    cache_segment_id TEXT NOT NULL,
    audit_complete INTEGER NOT NULL CHECK(audit_complete IN (0,1)),
    cache_complete INTEGER NOT NULL CHECK(cache_complete IN (0,1))
);
INSERT INTO archive_catalog_summary_rebuild_state VALUES(
    1,0,
    '0000000000000000000000000000000000000000000000000000000000000000',
    0,
    '0000000000000000000000000000000000000000000000000000000000000000',
    0,'',0,0
);

CREATE TABLE archive_catalog_summary_diagnostics_state (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    expected_epoch INTEGER NOT NULL CHECK(expected_epoch>=0),
    expected_ledger_digest TEXT NOT NULL CHECK(length(expected_ledger_digest)=64),
    audit_epoch INTEGER NOT NULL CHECK(audit_epoch>=0),
    audit_ledger_digest TEXT NOT NULL CHECK(length(audit_ledger_digest)=64),
    cache_bound_epoch INTEGER NOT NULL CHECK(cache_bound_epoch>=0),
    cache_segment_id TEXT NOT NULL,
    audit_complete INTEGER NOT NULL CHECK(audit_complete IN (0,1)),
    cache_complete INTEGER NOT NULL CHECK(cache_complete IN (0,1)),
    cycle_complete INTEGER NOT NULL CHECK(cycle_complete IN (0,1))
);
INSERT INTO archive_catalog_summary_diagnostics_state VALUES(
    1,0,
    '0000000000000000000000000000000000000000000000000000000000000000',
    0,
    '0000000000000000000000000000000000000000000000000000000000000000',
    0,'',0,0,0
);
