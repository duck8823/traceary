# #1658 immutable segment Catalog summary

## Structure-Behavior Design Note

### Requirement summary

- Store a bounded immutable Catalog summary inside format-v1 segment SQLite; no sidecar is authoritative.
- Accept only caller-produced canonical metadata structures: HMAC-SHA256 exact tokens, versioned Bloom bytes, and keyed session aggregates. Do not store raw metadata plaintext.
- Bind the summary descriptor and normalized tables to the manifest, segment logical digest, and content-addressed identity.
- Verify summary schema, caps, normalized contents, digest, and identity without decoding history payloads.
- Preserve malformed legacy `created_at` raw bytes while preventing an incomplete time summary from excluding a segment.
- Summary generation, Hot Catalog publication, routing, and segment placement remain later-issue responsibilities.

### Conceptual model

| Concept | State | Invariant |
|---|---|---|
| Summary descriptor v1 | key ID, completeness, exact tokens, Bloom descriptors, session aggregates | deterministic canonical bytes contain no raw metadata fields |
| Normalized summary tables | immutable query-oriented copy inside the segment | reconstruction must byte-equal the canonical descriptor |
| Manifest summary facts | version/digest/key/completeness/row and byte counts | all facts are independently cap checked |
| Logical segment digest | domain-separated length frames for History Units and summary | concatenation ambiguity is impossible and summary changes segment identity |
| Time summary | complete or incomplete | incomplete time metadata can never return a negative candidate decision |

### Responsibilities and boundaries

`domain` owns fixed descriptors, deterministic encoding, validation, and conservative time-filter behavior. `infrastructure/sqlite` owns immutable tables, bounded construction, manifest binding, and metadata/full verification. The caller owns HMAC/Bloom generation and key policy. #1650 owns the Hot Catalog, #1651 publication, and #1652 routing.

### Behavior tests / TDD plan

Tests prove deterministic order, duplicate/invalid descriptor rejection, mixed valid/malformed legacy timestamps, conservative incomplete-time selection, sealed build/full verification, metadata verification without payload decode, singleton and fixed-schema enforcement, normalized-table tamper rejection, allocation-before-cap behavior, one-inode verification across pathname exchange, digest binding, and caps. Existing full verification remains the stronger payload-parity gate.

### Rollback and risk

The format is not activated, so rollback removes this additive implementation. The metadata verifier deliberately hashes the sealed file when an expected file digest is supplied, but never selects `history_units.payload`. HMAC keys are not stored; `filter_key_id` is only a non-secret identifier. False-negative properties of caller-generated Bloom contents cannot be proven at this storage boundary and must be tested by the generator/router issues.
