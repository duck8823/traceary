-- List memory edges matching the given filters, ordered by validity
-- then creation descending. NULL valid_to is treated as +∞ for the
-- as-of comparison. See #573.
SELECT id, from_memory_id, to_memory_id, relation_type, valid_from, valid_to, created_at
  FROM memory_edges
 WHERE (? = '' OR from_memory_id = ? OR to_memory_id = ?)
   AND (? = '' OR relation_type = ?)
   AND (? = '' OR valid_from <= ?)
   AND (? = '' OR valid_to IS NULL OR valid_to > ?)
 ORDER BY valid_from DESC, created_at DESC
 LIMIT CASE WHEN ? = 0 THEN -1 ELSE ? END
