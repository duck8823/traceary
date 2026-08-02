-- Databases that already applied the original migration 38 have a complete,
-- trigger-maintained source sequence. The compatibility default avoids an
-- unbounded discovery scan on those stores. Modified migration 38 creates the
-- same table first with requires_inventory=1 for fresh upgrades.
CREATE TABLE IF NOT EXISTS search_projection_inventory_compat(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 requires_inventory INTEGER NOT NULL CHECK(requires_inventory IN(0,1))
);
INSERT OR IGNORE INTO search_projection_inventory_compat VALUES(1,0);
-- A newly-created empty store has no historical gap; subsequent inserts are
-- maintained by the migration-38 trigger. The LIMIT keeps this constant-time.
UPDATE search_projection_inventory_compat SET requires_inventory=0
WHERE singleton=1 AND NOT EXISTS(SELECT 1 FROM events LIMIT 1);

CREATE TABLE search_projection_inventory_state(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 generation_id TEXT NOT NULL DEFAULT '',
 cursor TEXT NOT NULL DEFAULT '',
 cursor_started INTEGER NOT NULL DEFAULT 0 CHECK(cursor_started IN(0,1)),
 state TEXT NOT NULL DEFAULT 'idle' CHECK(state IN('idle','rebuilding','complete','drifted'))
);
INSERT INTO search_projection_inventory_state(singleton) VALUES(1);
