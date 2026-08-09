-- Resolve body_availability for already-selected event IDs.
-- Visible text, rune counts, and canonical-envelope classification are owned
-- by Go after loadEventPlaintext (event_bounded_datasource.go); this query
-- deliberately does not interpret body content (#1685 D6).
SELECT e.id, e.body_availability
  FROM events e
 WHERE e.id IN (SELECT CAST(value AS TEXT) FROM json_each(?));
