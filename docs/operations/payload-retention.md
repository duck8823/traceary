# Payload retention and capacity contract

[日本語](./payload-retention.ja.md)

Status: the public archive-package and retention-plan surface was removed in
v0.49.0 (#2326). This page keeps the storage facts that still apply.

Offline migration 82 decodes remaining encoded event and command-audit
payloads, then drops codec metadata. Live writers store bodies as written
plaintext. There is no compact encode step and no flag to re-enable
compression. A 0.48 store still upgrades through the migration-only decoder on
the prepared-upgrade candidate.

Offline migration 83 drops body-availability / raw-body-retention objects.
Bodies stay as written; there is no live discard path.

Offline migration 84 drops empty `archive_segments`. A non-empty table refuses
the upgrade and names the 0.48.2 restore/export procedure. This binary cannot
read archive packages. Pin Traceary 0.48.2 to retrieve them. `bundle` and
`store backup` remain the portable and safety-copy paths.

`store compact --retention-plan` / `--retention-apply` / `--archive*` are
unknown flags. Historical v0.31 plan JSON, golden vectors, and
`schema/retention-plan.schema.json` are no longer a live contract.

Allocated bytes remain an estimate of storage occupied by a payload, not a
promise that the filesystem will immediately reclaim them. Physical file size
falls only after `store compact` (VACUUM INTO / candidate rewrite) or an
explicit VACUUM. DROP moves pages to the freelist.
