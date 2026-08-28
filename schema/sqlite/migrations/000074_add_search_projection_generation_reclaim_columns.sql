-- Terminal (failed / abandoned) generations keep their derived rows until a
-- later generation reaches cleanup (#2261). Record why and when a generation
-- became terminal on the lifecycle row itself -- search_projection_state only
-- holds the *current* generation's failure_class, so it is lost when the next
-- generation replaces it -- and record bounded reclaim progress so status and
-- doctor can prove the resident rows are gone.
--
-- Additive only: ALTER TABLE ADD COLUMN with constant defaults is a metadata
-- operation, and the backfill below touches one row per generation (12 on the
-- reference store), never an event. The lifecycle state CHECK is untouched:
-- "reclaimed" is evidence (reclaimed_at <> ''), not a state, so older binaries
-- keep working and simply ignore the columns.
ALTER TABLE search_projection_generation_lifecycle ADD COLUMN failure_class TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_generation_lifecycle ADD COLUMN terminal_at TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_generation_lifecycle ADD COLUMN reclaimed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE search_projection_generation_lifecycle ADD COLUMN reclaimed_rows INTEGER NOT NULL DEFAULT 0;
ALTER TABLE search_projection_generation_lifecycle ADD COLUMN reclaimed_logical_bytes INTEGER NOT NULL DEFAULT 0;

-- Best-effort backfill for the generation the singleton state row still describes.
UPDATE search_projection_generation_lifecycle
   SET failure_class = COALESCE((SELECT failure_class FROM search_projection_state WHERE singleton=1), ''),
       terminal_at   = COALESCE((SELECT updated_at    FROM search_projection_state WHERE singleton=1), '')
 WHERE state IN ('failed','abandoned')
   AND generation_id = COALESCE((SELECT generation_id FROM search_projection_state WHERE singleton=1), '');

-- Abandoned generations already carry their own timestamp.
UPDATE search_projection_generation_lifecycle
   SET terminal_at = abandoned_at
 WHERE state = 'abandoned' AND terminal_at = '' AND abandoned_at <> '';
