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
- `usage_observations.ndjson` — provider-neutral usage evidence with optional run attribution. Packet bodies, prompts, responses, tool names, arguments, and results are never included.

Traceary still imports v0.9.0 `manifest_version = 1` bundles that use `file_checksums`. v2 registers table files through `tables`; current writers emit five table entries (`sessions`, `usage_observations`, `events`, `command_audits`, `memories`). Omitting a table is a subtractive change: v2 readers skip manifest-absent tables and reject unknown present ones, so no bundle version bump is required. `run_lineages` and `memory_edges` are retired entries — legacy v2 bundles carrying them are checksum-verified and skipped when empty / atomically refused when non-empty on import (the refuse prints the row count and the 0.48.2 retrieval procedure; nothing is imported). New readers accept v2 bundles without those tables.

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

The import command also accepts `--missing-parent {reject,skip,backfill}` to control how an imported session is handled when its parent session is absent in the destination store; the default is `reject`. `--orphan-edges` is gone with the memory graph (#2327).

## Manifest v2 table registry spec

`manifest_version = 2` uses `manifest.json.tables` as the authoritative registry. Each key is the table name and each value has:

```json
{
  "table_name": "memories",
  "file": "memories.ndjson",
  "row_count": 12,
  "checksum": "<sha256 of the exact NDJSON bytes>"
}
```

Import verifies every registered file checksum before opening the write transaction, rejects unregistered payload files, and applies supported tables in dependency order across the current five-table portability surface:

1. `sessions.ndjson`
2. `usage_observations.ndjson`
3. `events.ndjson`
4. `command_audits.ndjson`
5. `memories.ndjson`

### Five-table inclusion rules

| Table | Current writer | Import requirement |
|---|---:|---|
| `events` / `events.ndjson` | Included | Independent rows; idempotent by `events.id`. |
| `sessions` / `sessions.ndjson` | Included | Imported first so owning sessions exist before events; `--missing-parent` controls a session whose parent session is absent in the destination. |
| `usage_observations` / `usage_observations.ndjson` | Included | Run-scoped rows retain their body-free run identity; session snapshots cannot carry run attribution. Legacy `run_lineages` is not required. |
| `command_audits` / `command_audits.ndjson` | Included | Filtered to the exported events; idempotent by `event_id`. |
| `memories` / `memories.ndjson` | Included | New rows enter `candidate` status unless already present. |

### Conflict matrix

| Condition | Default | Strict option | Transaction outcome |
|---|---|---|---|
| Existing event ID | Skip and count `events_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Existing session ID | Skip and count `sessions_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Existing command-audit `event_id` | Skip and count `command_audits_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Existing memory ID | Skip and count `memories_skipped` | `--on-conflict=error` | Strict mode rolls back. |
| Legacy empty `run_lineages` or `memory_edges` entry | Skip after checksum/count validation | n/a | Rest of the bundle imports. |
| Legacy non-empty `run_lineages` or `memory_edges` entry | Reject | n/a | Atomic refuse before the write transaction; retrieve facts with the 0.48.2 binary. |
| Imported session parent missing | Reject the import (`--missing-parent=reject`) | `--missing-parent=skip` / `backfill` | Reject rolls back; `skip` drops the row, `backfill` reconstructs a placeholder parent. |
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
