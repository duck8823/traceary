SELECT
    ref_kind,
    ref_value
FROM memory_artifact_refs
WHERE memory_id = ?
ORDER BY ordinal ASC;
