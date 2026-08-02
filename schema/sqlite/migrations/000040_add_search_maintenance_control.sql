-- Additive control ledger only. Destructive legacy projection work is owned
-- by the explicit, resumable maintenance command, never migration or init.
CREATE TABLE search_maintenance_control(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 authority TEXT NOT NULL CHECK(authority IN('legacy','tiered')),
 phase TEXT NOT NULL CHECK(phase IN('active','retiring','retired','restoring')),
 progress INTEGER NOT NULL DEFAULT 0 CHECK(progress>=0),
	 transition_revision INTEGER NOT NULL DEFAULT 0,
 evidence_binding TEXT NOT NULL DEFAULT '',
	 target_adopted INTEGER NOT NULL DEFAULT 0 CHECK(target_adopted IN(0,1)),
 logical_bytes_before INTEGER NOT NULL DEFAULT 0,
 logical_bytes_after INTEGER NOT NULL DEFAULT 0,
 physical_bytes_before INTEGER NOT NULL DEFAULT 0,
 physical_bytes_after INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL,
 CHECK((authority='legacy' AND phase='active') OR (authority='tiered' AND phase IN('retiring','retired','restoring')))
);
INSERT INTO search_maintenance_control(singleton,authority,phase,updated_at)
VALUES(1,'legacy','active',strftime('%Y-%m-%dT%H:%M:%fZ','now'));
