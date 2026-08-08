-- Re-recording the same (session_id, to_event_id) is a no-op.
INSERT OR IGNORE INTO session_orphan_ranges (
    session_id,
    from_event_id,
    to_event_id,
    observed_at
) VALUES (?, ?, ?, ?)
