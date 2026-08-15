SELECT
    COALESCE(s.generation_id, ''),
    s.state,
    CASE WHEN s.state = 'rebuilding' AND i.state = 'rebuilding' THEN 'inventory' ELSE s.phase END,
    s.checkpoint,
    s.config_hash,
    s.capacity_semantics_version,
    COALESCE(s.failure_class, ''),
    COALESCE(s.cutover_index_family, ''),
    s.cutover_family_bytes_before,
    s.cutover_family_bytes_after,
    COALESCE(s.cutover_before_evidence_status, ''),
    COALESCE(s.cutover_before_evidence_reason, ''),
    COALESCE(s.cutover_after_evidence_status, ''),
    COALESCE(s.cutover_after_evidence_reason, '')
FROM search_projection_state AS s
JOIN search_projection_inventory_state AS i ON i.singleton = s.singleton
WHERE s.singleton = 1
