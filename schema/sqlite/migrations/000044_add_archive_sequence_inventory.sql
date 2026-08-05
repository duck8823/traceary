-- Archive sequence state is independent from the retired search projection.
-- This migration is intentionally data-independent: it installs singleton
-- state and triggers, but never scans or backfills historical events.
CREATE TABLE archive_store_lineage (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    store_id TEXT NOT NULL UNIQUE CHECK (length(store_id) = 32),
    filter_key BLOB NOT NULL CHECK (typeof(filter_key) = 'blob' AND length(filter_key) = 32)
);

INSERT INTO archive_store_lineage(singleton, store_id, filter_key)
VALUES (1, lower(hex(randomblob(16))), randomblob(32));

CREATE TABLE archive_sequence_allocator (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    next_sequence INTEGER NOT NULL CHECK (next_sequence > 0)
);

INSERT INTO archive_sequence_allocator(singleton, next_sequence) VALUES (1, 1);

CREATE TABLE archive_event_sequences (
    event_id TEXT PRIMARY KEY,
    sequence INTEGER NOT NULL UNIQUE CHECK (sequence > 0)
);

CREATE INDEX idx_archive_event_sequences_sequence_event
    ON archive_event_sequences(sequence, event_id);

CREATE TABLE archive_sequence_inventory_state (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    generation_id TEXT NOT NULL DEFAULT '',
    config_hash TEXT NOT NULL DEFAULT '',
    phase TEXT NOT NULL DEFAULT 'idle'
        CHECK (phase IN ('idle', 'inventory', 'verifying', 'complete', 'failed')),
    event_cursor TEXT NOT NULL DEFAULT '',
    event_cursor_started INTEGER NOT NULL DEFAULT 0 CHECK (event_cursor_started IN (0, 1)),
    verify_cursor INTEGER NOT NULL DEFAULT 0 CHECK (verify_cursor >= 0),
    high_water INTEGER NOT NULL DEFAULT 0 CHECK (high_water >= 0),
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    failure_class TEXT NOT NULL DEFAULT ''
);

INSERT INTO archive_sequence_inventory_state(singleton) VALUES (1);

CREATE TABLE archive_sequence_activation (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    active_generation_id TEXT NOT NULL DEFAULT '',
    verified_high_water INTEGER NOT NULL DEFAULT 0 CHECK (verified_high_water >= 0)
);

INSERT INTO archive_sequence_activation(singleton) VALUES (1);

CREATE TRIGGER archive_events_assign_sequence
AFTER INSERT ON events
BEGIN
    SELECT CASE
        WHEN NOT EXISTS (SELECT 1 FROM archive_event_sequences WHERE event_id = NEW.id)
         AND (SELECT next_sequence FROM archive_sequence_allocator WHERE singleton = 1) >= 9223372036854775807
        THEN RAISE(ABORT, 'archive sequence exhausted')
    END;
    UPDATE archive_sequence_allocator
       SET next_sequence = next_sequence + 1
     WHERE singleton = 1
       AND NOT EXISTS (SELECT 1 FROM archive_event_sequences WHERE event_id = NEW.id);
    INSERT INTO archive_event_sequences(event_id, sequence)
    VALUES (NEW.id, (SELECT next_sequence - 1 FROM archive_sequence_allocator WHERE singleton = 1))
    ON CONFLICT(event_id) DO NOTHING;
END;
