# Cross-machine handoff

[日本語](./cross-machine-handoff.ja.md)

Part of #567 · closes #572.

Traceary is local-first and single-SQLite. `traceary bundle export` / `bundle import` are the portability primitives introduced in v0.9.0 so operators can carry their history between machines — laptop ↔ work desktop ↔ remote dev box — **without adding a hosted plane**.

## What the bundle contains

v0.9.0 bundle (manifest_version = 1):

- `manifest.json` — schema version, creation time, filters used, per-file SHA-256 checksums.
- `events.ndjson` — every event matching `--since` / `--until` / `--workspace`, ordered by `created_at` for deterministic output.

**Not yet in v0.9.0**: sessions, command audits, durable memories, graph edges. Tracked as a follow-up (see bottom of this doc). Events are the primary portable history, so the MVP ships with events only while the rest of the data model stabilises.

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

`bundle import` is idempotent — an event already present in the destination store is skipped (counted under `events_skipped`), so re-importing the same bundle any number of times is safe.

## Schema safety

The manifest records the exporter's `schema_migrations` max version. `bundle import` refuses to run if the bundle was created on a **newer** schema than the local store; upgrade Traceary first, then retry.

A bundle created on an **older** schema imports cleanly — the destination store only needs the union of the migrations that existed when each event was written, not the newer ones.

## What bundle does NOT do

- **No real-time replication** (litestream-style block sync). Evaluate separately.
- **No public / shared bundles**. The encryption envelope is symmetric; all readers need the same passphrase.
- **No automatic Traceary transport**. Add only if a future version of the local-first posture ever accepts a hosted component — not planned.

## Follow-up (post-v0.9)

- Extend `bundle export` / `bundle import` to sessions, command audits, durable memories (as `candidate` by default), and graph edges. Tracked as a new follow-up issue when this doc ships.
- Public-key mode (recipient pubkey instead of passphrase) for sending a bundle to a collaborator without sharing a passphrase.
