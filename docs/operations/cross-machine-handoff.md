# Cross-machine handoff

[日本語](./cross-machine-handoff.ja.md)

Part of #567 · closes #572.

Traceary is local-first and single-SQLite. `traceary bundle export` / `bundle import` are the portability primitives introduced in v0.9.0 so operators can carry their history between machines — laptop ↔ work desktop ↔ remote dev box — **without adding a hosted plane**.

## What the bundle contains

Current bundle (manifest_version = 2):

- `manifest.json` — store schema version, creation time, filters used, writer metadata, import defaults, and a per-table registry (`tables`) with `{table_name, file, row_count, checksum}` entries.
- `events.ndjson` — every event matching `--since` / `--until` / `--workspace`, ordered by `created_at` for deterministic output.
- `sessions.ndjson` — session boundary records: the sessions matching the export window/workspace filters plus any additional sessions referenced by the exported events, so imported events keep their owning session.
- `command_audits.ndjson` — shell command audit records, filtered to the exported events.
- `memories.ndjson` — durable memories with scope, validity window, supersession pointer, evidence refs, and artifact refs.
- `memory_edges.ndjson` — typed memory graph edges with `id`, `from_memory_id`, `to_memory_id`, `relation_type`, validity window, and `created_at`.
- `run_lineages.ndjson` — immutable namespaced run identities, optional parent/work correlation, and body-free packet/tool byte facts. Filtered exports include only usage-referenced runs and their complete ancestor closure; unfiltered exports also include standalone runs.
- `usage_observations.ndjson` — provider-neutral usage evidence with optional run attribution. Packet bodies, prompts, responses, tool names, arguments, and results are never included.

Traceary still imports v0.9.0 `manifest_version = 1` bundles that use `file_checksums`. v2 registers table files through `tables`; current writers always emit all seven table entries, including an empty `run_lineages` entry when no run facts exist. New readers accept older v2 bundles without that table; older readers reject the unknown table rather than silently discarding lineage.

## Encryption

Every bundle is encrypted with XChaCha20-Poly1305; the symmetric key is derived from your passphrase via Argon2id (OWASP defaults: 3 iterations, 64 MiB, 4 lanes). The archive envelope starts with the magic bytes `TRBUNDLE` so a mistakenly-renamed `.tar.gz` file is immediately identifiable.

Passphrases go through the **environment only** — never through a CLI flag. Shell history and audit logs never see the secret.

```sh
export TRACEARY_BUNDLE_PASSPHRASE='something-long-and-specific'
```

## Transport

Traceary does not move the bundle file. Pick whichever transport your machines already share:

| Transport | When it fits |
|---|---|
| AirDrop (macOS) | One-shot, same room, trustworthy fastest path |
| `scp` / `rsync` over SSH | dev box ↔ laptop, scriptable |
| Syncthing | continuous background sync (P2P, no hosted service) |
| iCloud Drive / Dropbox | Fine **because the bundle is already encrypted** |
| USB stick / external SSD | air-gapped |
| Email attachment | small bundles only; the encryption makes it safe in principle |

The bundle is encrypted AEAD content, not a readable `.tar.gz`, so any transport that preserves bytes is acceptable.

## Recommended flows

### Occasional (monthly)

```sh
# laptop
export TRACEARY_BUNDLE_PASSPHRASE='your-passphrase'
traceary bundle export --out ~/Desktop/traceary-$(date +%Y%m%d).tbun

# AirDrop → desktop

# desktop
export TRACEARY_BUNDLE_PASSPHRASE='your-passphrase'
traceary bundle import --in ~/Downloads/traceary-*.tbun
```

### Continuous (daily)

1. Stand up a Syncthing folder shared between your machines, e.g. `~/.traceary/bundles/`.
2. On the laptop, cron the export:
   ```cron
   0 19 * * * TRACEARY_BUNDLE_PASSPHRASE=... traceary bundle export --out ~/.traceary/bundles/$(date +%F).tbun --since $(date -v-1d +%Y-%m-%d)
   ```
3. On the other machine, a start-up hook (or cron) imports anything new:
   ```sh
   for bundle in ~/.traceary/bundles/*.tbun; do
     traceary bundle import --in "$bundle"
   done
   ```

`bundle import` defaults to `--on-conflict skip`: an event or memory already present in the destination store is skipped (counted under `events_skipped` / `memories_skipped`), so re-importing the same bundle any number of times is safe. Use `--on-conflict replace` to overwrite existing rows from the bundle, or `--on-conflict error` to fail on the first UNIQUE collision and roll back the import.

Imported memories use the candidate trust default: newly inserted rows are always written as `candidate`, even when the source machine had already accepted them. A memory fact can influence prompt context after acceptance, so importing from another machine keeps the existing memory inbox review step in the loop. Existing destination rows are untouched under the default `skip` policy; re-importing a bundle does not downgrade a memory you already reviewed and accepted locally.

The import command also accepts `--missing-parent {reject,skip,backfill}` to control how an imported session is handled when its parent session is absent in the destination store; the default is `reject`. Memory graph edges use `--orphan-edges {skip,reject}` instead. The default is `skip`: if either endpoint is absent after `memories.ndjson` has imported, Traceary skips the edge and emits a structured warning containing `table=memory_edges`, `edge_id`, both endpoint IDs, and endpoint existence booleans. `--orphan-edges=reject` aborts the import and rolls back the surrounding transaction.

## Manifest v2 table registry spec

`manifest_version = 2` uses `manifest.json.tables` as the authoritative registry. Each key is the table name and each value has:

```json
{
  "table_name": "memory_edges",
  "file": "memory_edges.ndjson",
  "row_count": 12,
  "checksum": "<sha256 of the exact NDJSON bytes>"
}
```

Import verifies every registered file checksum before opening the write transaction, rejects unregistered payload files, and applies supported tables in dependency order across the current seven-table portability surface:

1. `sessions.ndjson`
2. `run_lineages.ndjson`
3. `usage_observations.ndjson`
4. `events.ndjson`
5. `command_audits.ndjson`
6. `memories.ndjson`
7. `memory_edges.ndjson`

### Seven-table inclusion rules

| Table | Current writer | Import requirement |
|---|---:|---|
| `events` / `events.ndjson` | Included | Independent rows; idempotent by `events.id`. |
| `sessions` / `sessions.ndjson` | Included | Imported first so owning sessions exist before events; `--missing-parent` controls a session whose parent session is absent in the destination. |
| `run_lineages` / `run_lineages.ndjson` | Included | Imported ancestor-first. Every parent and every run referenced by usage must be present in the bundle itself. |
| `usage_observations` / `usage_observations.ndjson` | Included | Imported after run lineage. Run-scoped rows retain their body-free run identity; session snapshots cannot carry run attribution. |
| `command_audits` / `command_audits.ndjson` | Included | Filtered to the exported events; idempotent by `event_id`. |
| `memories` / `memories.ndjson` | Included | Imported before `memory_edges`; new rows enter `candidate` status unless already present. |
| `memory_edges` / `memory_edges.ndjson` | Included | Imported after memories; both endpoints must exist in the destination DB. Existing edge IDs are skipped under default `--on-conflict=skip`. |

### Conflict matrix

| Condition | Default | Strict option | Transaction outcome |
|---|---|---|---|
| Existing event ID | Skip and count `events_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Existing session ID | Skip and count `sessions_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Existing command-audit `event_id` | Skip and count `command_audits_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Existing memory ID | Skip and count `memories_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Existing memory edge ID | Skip and count `memory_edges_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Exact existing run lineage | Skip and count `run_lineages_skipped` | n/a | Idempotent under every conflict policy. |
| Conflicting/missing/cyclic run lineage or incomplete usage link | Reject | n/a | Always rolls back all tables; `skip`, `replace`, and missing-parent options cannot weaken lineage integrity. |
| Imported session parent missing | Reject the import (`--missing-parent=reject`) | `--missing-parent=skip` / `backfill` | Reject rolls back; `skip` drops the row, `backfill` reconstructs a placeholder parent. |
| Memory edge endpoint missing after memories import | Skip, count `memory_edges_skipped`, and log structured warning | `--orphan-edges=reject` | Strict mode rolls back all tables in the bundle import transaction. |
| Bundle schema newer than local store | Reject | n/a | No write transaction starts. |
| Manifest checksum / row-count mismatch | Reject | n/a | No write transaction starts. |

## Schema safety

The manifest records the exporter's `schema_migrations` max version. `bundle import` refuses to run if the bundle was created on a **newer** schema than the local store; upgrade Traceary first, then retry.

A bundle created on an **older** schema imports cleanly — the destination store only needs the union of the migrations that existed when each event was written, not the newer ones.

Migration `000028` is additive: v27 usage rows remain valid with unknown run attribution. Downgrading a store after new lineage writes is unsupported; restore a pre-migration backup before running an older binary.

## What bundle does NOT do

- **No real-time replication** (litestream-style block sync). Evaluate separately.
- **No public / shared bundles**. The encryption envelope is symmetric; all readers need the same passphrase.
- **No automatic Traceary transport**. Add only if a future version of the local-first posture ever accepts a hosted component — not planned.

## Follow-up (post-v0.9)

- Public-key mode (recipient pubkey instead of passphrase) for sending a bundle to a collaborator without sharing a passphrase.
