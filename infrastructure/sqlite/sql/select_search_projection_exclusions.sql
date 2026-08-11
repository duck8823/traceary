SELECT source_sequence,event_id,class,measured_bytes,byte_limit
FROM search_projection_exclusions
WHERE generation_id=?
ORDER BY source_sequence
LIMIT ?
