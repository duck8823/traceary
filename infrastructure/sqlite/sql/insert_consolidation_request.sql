INSERT OR IGNORE INTO consolidation_requests (
    session_id,
    client,
    requested_at,
    at_event_id,
    signal,
    pressure_value,
    threshold_value,
    re_request,
    delivery
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
