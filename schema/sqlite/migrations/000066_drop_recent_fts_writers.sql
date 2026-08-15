-- Stop writing the unread recent full-text tier (#1842).
--
-- The virtual table can be multi-GiB (`search_projection_recent_fts_data`
-- was 9.0 GB on the #1620 store). Do not DROP it here: startup applies
-- every pending migration, and a multi-GiB DROP would block every open
-- (same rule as 052). Reclaim is `store compact` on the work copy.
--
-- Source-side: nothing new enters the index. Existing documents stay.
DROP TRIGGER IF EXISTS search_projection_recent_ai;
DROP TRIGGER IF EXISTS search_projection_recent_ad;
