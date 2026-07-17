# Archive-before-GC design note and manifest contract

[日本語](./archive-before-gc.ja.md)

Part of epic #1309 · closes design slice #1370 · parent cut #1360.

This document is the **Structure-Behavior Design Note** and **versioned archive manifest contract** for hot/cold store retention. Slice 2 (#1371) implements the manual operator path; slice 3 (#1372) reuses the same application core for opt-in automatic scheduling. No destructive GC enablement ships in the design PR itself.

## Requirement summary

| Item | Detail |
|---|---|
| **Purpose** | Let operators shrink multi-GB live SQLite stores without silently losing cold history: export eligible rows into a versioned, integrity-checked (and optionally encrypted) archive, **verify**, then delete only the exact archived identities from the live DB. |
| **Status quo** | `traceary store gc` hard-deletes aged rows (events/sessions/memories/edges) under `--keep-days`. There is full-file backup (`store backup`) and portable encrypted bundle (`store bundle`), but neither is a stream archive of GC-eligible cold rows with verify-before-delete. |
| **Expected behavior** | Dry-run-first archive plan → stream export → manifest + digest verification (+ decrypt round-trip when sealed) → delete exact IDs → optional VACUUM. Restore re-imports archived identities idempotently. Failures leave the live DB unchanged. |
| **Non-goals (this design)** | Compressed VFS / ZIPVFS; network offload; changing default retention to auto-archive; rewriting full-file backup; auto-accept of memories. |

### Release posture (v0.28.0)

| Slice | Issue | Posture |
|---|---|---|
| 1 Design + manifest contract | #1370 | **Release-blocking** (this doc) |
| 2 Manual archive + restore | #1371 | **Release-blocking** |
| 3 Opt-in automatic scheduler + doctor | #1372 | **In scope for v0.28.0** (user: no scope-out); still **opt-in / fail-closed**, never default-on |
| 4 Dogfood + decision log | #1373 | **Release-blocking** evidence |

Serialize GC rewrites with memory-decay GC work (#1368/#1369): archive ownership of the delete-after-verify path is #1371; decay ownership of candidate → expired transitions remains #1368.

## Conceptual model

| Concept | State | Behavior | Constraint / invariant |
|---|---|---|---|
| **Live store** | hot SQLite under normal path | Accepts hooks/CLI/MCP writes | Never partially deleted mid-archive; all deletes after verified archive only |
| **Archive plan** | computed, not yet written | Lists exact primary keys per table in stable order | Same plan on dry-run and apply for same cutoff/target/clock |
| **Archive package** | file at caller path | Contains envelope + payload + manifest | Magic + version; payload digest matches manifest; row IDs exact |
| **Archive manifest** | JSON v1 inside package | Records schema version, cutoff, table counts, per-row digests, package digest | Immutable after seal; verification reads this first |
| **Cold record set** | rows matching GC eligibility for selected target(s) | Eligible for export then delete | Accepted memories never auto-included unless operator target explicitly includes them under GC rules |
| **Restore run** | import of archive package | Insert-or-skip by primary key | Never overwrite differing live rows silently; report conflicts |
| **Retention mode** | config: `disabled` (default) / `archive_then_gc` | Automatic path only when opt-in | Passphrase via env **name** only; secrets never persisted |

## Responsibility assignment

| Responsibility | Owner | Reason to change | Not owner |
|---|---|---|---|
| Eligibility rules (what is cold) | Align with existing GC targets + keep-days; domain/types for target enum | Retention policy change | Presentation |
| Stream export / import of row sets | `application/usecase` archive use case | Export format / batching | CLI flag parsing |
| Manifest build + verify | application codec + pure verifier | Manifest schema version | SQLite SQL strings alone |
| Envelope seal/open (optional encryption) | Reuse bundle crypto primitives (Argon2id + XChaCha20-Poly1305) | Crypto parameters | Doctor message text |
| Exact-ID delete after verify | `StoreManagement` / SQLite datasource | SQL identity lists | CLI |
| Opportunistic auto worker + lease | infrastructure + hook/doctor piggyback (#1372) | Scheduling policy | Manual CLI path |
| Operator surface | `presentation/cli` (`store archive …` / `store gc --archive` — final names in #1371) | UX | Domain invariants |

## Boundaries / interfaces

| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| `ArchivePlan(ctx, cutoff, target) → Plan` | CLI, auto worker | SQL selection | Invalid target → user error; empty plan → success no-op |
| `ExportArchive(ctx, plan, path, opts) → Manifest` | CLI, auto worker | Streaming write, temp file rename | IO / disk full → live DB unchanged; no partial package at final path |
| `VerifyArchive(ctx, path, opts) → Report` | CLI, before delete, restore | Decrypt + re-hash | Fail closed; never delete on verify fail |
| `DeleteArchived(ctx, manifest) → Counts` | CLI apply path only | Transactional multi-table delete by ID | Missing ID is warn+continue or hard fail (choose hard fail in v1 for safety) |
| `RestoreArchive(ctx, path, opts) → Counts` | CLI | Idempotent insert | Conflict if live row differs → abort batch or skip with report (v1: skip+report, exit non-zero if any conflict) |
| Config `retention` section (#1372) | doctor, worker | File + env | Missing passphrase env when sealed mode → WARN, no delete |

### Proposed CLI shape (normative for #1371; bikeshed only with design amendment)

```text
traceary store archive create --output PATH [--target all|events|…] [--keep-days N] [--dry-run] [--passphrase-env NAME]
traceary store archive verify --input PATH [--passphrase-env NAME]
traceary store archive restore --input PATH [--dry-run] [--passphrase-env NAME]
# Optional sugar later:
traceary store gc --archive PATH …   # archive-then-gc one shot; still verify-before-delete
```

Automatic mode (#1372) calls the same use case methods; it does not reimplement SQL.

## Archive package format (v1)

Inspired by `store bundle` but specialized for cold-row GC:

1. **Optional outer envelope** (same family as bundle): magic `TRCARYAR` (8 bytes) + version byte `1` + salt(16) + nonce(24) + ciphertext, when passphrase is provided. Without passphrase: magic + version + raw payload (still integrity-checked via manifest digests).
2. **Inner payload**: gzip or zstd compressed tar (choose **zstd** if stdlib/module already available; otherwise gzip for zero new deps — decision recorded in #1371 PR with benchmark numbers).
3. **Tar members** (stable names):
   - `manifest.json` — schema below
   - `tables/<table>.ndjson` — one JSON object per line, full row projection needed for restore
   - `tables/<table>.sha256` — hex digest of the exact ndjson bytes

### `manifest.json` schema (v1)

Machine-readable JSON Schema: [`archive-manifest.v1.schema.json`](./archive-manifest.v1.schema.json).

Logical fields:

```json
{
  "schema_version": 1,
  "format": "traceary.store.archive",
  "created_at": "2026-07-17T12:00:00.000000000Z",
  "tool_version": "0.28.0",
  "source_db_fingerprint": {
    "path": "~/.config/traceary/traceary.db",
    "page_count": 12345,
    "schema_user_version": 42
  },
  "plan": {
    "target": "all",
    "keep_days": 90,
    "cutoff": "2026-04-18T00:00:00.000000000Z",
    "dry_run": false
  },
  "tables": [
    {
      "name": "events",
      "primary_key": ["id"],
      "row_count": 1000,
      "ndjson_sha256": "…",
      "row_ids_sha256": "…",
      "compressed_bytes": 123456,
      "uncompressed_bytes": 987654
    }
  ],
  "totals": {
    "rows": 1000,
    "compressed_bytes": 123456,
    "uncompressed_bytes": 987654
  },
  "payload_sha256": "…",
  "encryption": {
    "mode": "none|xchacha20poly1305-argon2id",
    "kdf": "argon2id",
    "kdf_params": { "time": 3, "memory_kib": 65536, "threads": 4 }
  }
}
```

**Identity rules**

- Export order: table order fixed (`events`, `command_audits`, `sessions`, `memories`, `memory_edges`, then child ref tables as needed). Within a table, primary key ascending.
- Delete order: reverse FK-safe order (children before parents) using the **same ID sets** listed in the archive (never re-query with a new cutoff after export).
- `row_ids_sha256` is the SHA-256 of `join("\n", sorted_ids)+"\n"` so verify can prove the ID set without re-reading full bodies when only identity checks are needed.

## Verification checklist (must pass before delete)

1. File exists; magic + version parse.
2. If sealed: decrypt succeeds with configured passphrase env (wrong secret → fail, no delete).
3. `manifest.json` validates against schema v1.
4. Each `tables/<name>.ndjson` digest matches manifest.
5. Each line parses; primary keys unique; `row_count` matches.
6. `row_ids_sha256` matches reconstructed ID list.
7. Optional deep check: re-hash full payload / sample N random rows against live DB (same content) when still present.
8. Only then open a write transaction that deletes **exactly** those IDs for each table.

Any failure → abort; live DB unchanged; leave archive file as-is for inspection.

## Crash windows and recovery

| Window | Risk | Mitigation |
|---|---|---|
| Mid-export write | Partial file | Write to `PATH.partial` + fsync + atomic rename |
| Export done, verify not run | Orphan archive | Safe; operator re-runs verify |
| Verify OK, delete mid-transaction | Partial delete | Single SQLite transaction for all deletes; rollback on error |
| Delete committed, VACUUM fails | Space not reclaimed | Report VACUUM error; data already consistent |
| Auto worker concurrent | Double archive / race | Per-DB exclusive lease file + interval marker (#1372) |
| Restore into divergent live row | Silent clobber | v1 skip+report conflict; never overwrite different content |

## Per-record compression benchmark plan

Bulk zstd on a whole DB is **not** sufficient. #1371 must record:

| Case | Method | Metrics |
|---|---|---|
| Synthetic 10k short events | ndjson+zstd vs gzip vs none | compressed size, export seconds, restore seconds |
| Synthetic 1k large command audits (~100 KiB bodies) | same | ratio vs full-file `store backup` size for same rows |
| Live dogfood multi-GB store sample (operator machine, #1373) | archive create dry-run + apply on copy | pre/post live size, `store-size` doctor, read latency of `sessions --snapshot` before/after |
| Single-row overhead | 1-row archive package | floor size (manifest+envelope) |

Acceptance for benchmarks in #1371 PR body (not a hard CI gate): document numbers so #1372/operators can judge cold storage cost.

## Behavior tests (slice 2+)

| # | Behavior | Given | When | Then | Level |
|---|---|---|---|---|---|
| 1 | Dry-run plan | Aged events exist | `archive create --dry-run` | Prints counts; no file; DB unchanged | CLI + usecase |
| 2 | Export identities | Known IDs | create | Manifest row_ids match query | usecase |
| 3 | Verify-before-delete | Valid archive | apply path | Delete only after verify OK | usecase |
| 4 | Corrupt body | Bit-flip ndjson | verify / apply | Fail; zero deletes | usecase |
| 5 | Wrong passphrase | Sealed archive | verify | Fail closed | usecase |
| 6 | Idempotent restore | Empty target DB | restore twice | Second run skips all; exit 0 | usecase |
| 7 | Conflict restore | Live row differs | restore | Skip conflict; non-zero exit | usecase |
| 8 | Empty plan | Nothing eligible | create | No-op success | CLI |
| 9 | Auto disabled default | Fresh config | worker tick | No archive, no delete | #1372 |
| 10 | Lease conflict | Lease held | second worker | Skip; doctor shows reason | #1372 |

## TDD plan (for #1371)

| Behavior | Red | Green | Refactor |
|---|---|---|---|
| Plan counts | Test plan builder against fixture DB | Implement SQL selectors reusing GC eligibility | Share eligibility helpers with `CollectGarbage` carefully (don't couple delete SQL) |
| Manifest digests | Test golden small fixture | Write ndjson + sha helpers | Extract pure `archivecodec` package if files grow |
| Verify fail closed | Mutate bytes in test | Verifier checks | — |
| Delete exact IDs | Archive then delete | ID-list DELETE in tx | — |
| Restore skip | Double restore | Insert-or-skip | — |

## Config sketch (#1372 only; defaults fail-closed)

```json
{
  "retention": {
    "mode": "disabled",
    "archive_then_gc": {
      "interval": "168h",
      "keep_days": 90,
      "target": "all",
      "output_dir": "~/.config/traceary/archives",
      "passphrase_env": "TRACEARY_ARCHIVE_PASSPHRASE"
    }
  }
}
```

- `mode` default `disabled`.
- Never store passphrase material in config or SQLite.
- Doctor surfaces last success/failure, next eligibility, counts/bytes; never secrets.

## Risks / rollback

| Risk | Mitigation |
|---|---|
| Procedural mega-usecase | Separate plan / export / verify / delete / restore; pure codec tests |
| Premature shared framework with bundle | Share **crypto helpers only** first; diverge formats freely |
| GC dual-writer with decay (#1368) | Decay changes candidate→expired only; archive deletes aged terminal rows; document order: decay then archive-gc in ops notes |
| Operator deletes archive after GC | Document: archive is the recovery path; full-file backup remains the disaster path |
| Auto mode surprise deletes | Default disabled; dry-run markers; doctor WARN before first auto run |

**Rollback:** feature flags are unnecessary if auto is opt-in. Manual commands can ship disabled behind docs until dogfood (#1373). Tag can ship commands; destructive default remains today's `store gc` without archive unless operator passes archive flags.

## Implementation PR split

| PR | Issue | Deliverable |
|---|---|---|
| Design | #1370 | This doc + schema fixture |
| Manual path | #1371 | Commands + usecase + tests + benchmarks section in PR |
| Auto path | #1372 | Config + worker + doctor + tests |
| Dogfood | #1373 | Evidence log + any small ops doc fixes (no scope-out) |

## Linkage

- Epic: #1309
- Storage overview: [README.md](./README.md)
- Full-file backup (orthogonal): [../backup/README.md](../backup/README.md)
- Bundle portability (crypto precedent): application `bundle_codec.go`
