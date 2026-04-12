SELECT
    ref_kind,
    ref_value
FROM memory_evidence_refs
WHERE memory_id = ?
ORDER BY ordinal ASC;
