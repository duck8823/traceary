-- Hydrate only the visible-text prefix for already-selected event IDs. For a
-- canonical envelope SQLite still materializes the complete joined visible
-- text before computing substr/length, but neither that text nor the complete
-- stored body is a result column.
WITH envelope_classification AS (
    SELECT
        e.id,
        e.body,
        e.body_availability,
        CASE
            WHEN e.body_availability = 'available'
                 AND json_valid(e.body)
                 AND json_type(e.body, '$') = 'object'
                 AND json_type(e.body, '$.blocks') = 'array'
                 AND NOT EXISTS (
                     SELECT 1
                       FROM json_each(json_extract(e.body, '$.blocks'))
                      WHERE typeof(json_extract(value, '$.type')) != 'text'
                         OR typeof(json_extract(value, '$.text')) != 'text'
                 )
            THEN 1
            ELSE 0
        END AS canonical_envelope
    FROM events e
    WHERE e.id IN (SELECT CAST(value AS TEXT) FROM json_each(?))
),
classified_events AS (
    SELECT
        classified.id,
        classified.body_availability,
        classified.canonical_envelope,
        CASE
            -- Body availability and canonical classification share the same
            -- owner above. Any current or future unavailable state stays
            -- body-free instead of falling through to the stored marker/body.
            WHEN classified.body_availability <> 'available' THEN ''
            WHEN classified.canonical_envelope = 1
            THEN COALESCE(
                (
                    -- Ordering inside the aggregate is the contract; do not
                    -- depend on subquery presentation order.
                    SELECT group_concat(
                               json_extract(value, '$.text'),
                               char(10) || char(10)
                               ORDER BY CAST(key AS INTEGER)
                           )
                      FROM json_each(json_extract(classified.body, '$.blocks'))
                     WHERE json_extract(value, '$.type') = 'text'
                       -- Match strings.TrimSpace: Unicode White_Space code
                       -- points are ignored only when deciding whether a text
                       -- block is empty. Nonblank block text is preserved
                       -- byte-for-byte.
                       AND length(trim(
                           json_extract(value, '$.text'),
                           char(9) || char(10) || char(11) || char(12) ||
                           char(13) || char(32) || char(133) || char(160) ||
                           char(5760) || char(8192) || char(8193) ||
                           char(8194) || char(8195) || char(8196) ||
                           char(8197) || char(8198) || char(8199) ||
                           char(8200) || char(8201) || char(8202) ||
                           char(8232) || char(8233) || char(8239) ||
                           char(8287) || char(12288)
                       )) > 0
                ),
                ''
            )
            ELSE classified.body
        END AS visible_body
    FROM envelope_classification classified
)
SELECT
    id,
    substr(visible_body, 1, ?),
    length(visible_body),
    body_availability,
    canonical_envelope
FROM classified_events;
