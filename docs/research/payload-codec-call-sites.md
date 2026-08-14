# Decision: remaining `payloadCodecIdentity` call sites (#1779)

[日本語](./payload-codec-call-sites.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1779

## Decision

| Site | Decision | Why |
|---|---|---|
| Bundle import (`bundle_datasource.go`) | Adopt `encodeCanonicalPayload` | Import writes the live store. The bundle format stays plaintext; the writer must not grow the store relative to a native hook write. |
| Archive restore (`store_archive.go`) | Adopt `encodeCanonicalPayload` | Archive export already decodes to plaintext. Restore is a write. Compatibility counters follow the codec actually stored. |
| Dedupe restore (`content_event_dedupe_datasource.go`) | Adopt `encodeCanonicalPayload` | The quarantine archive stores decoded plaintext. Restore used to re-encode as identity and inflate the store. Same writer rule as import. |
| Raw-body recovery (`raw_body_retention_datasource.go` restore) | Adopt `encodeCanonicalPayload` | Recovery bodies are plaintext from the ledger. Restore is a write. |
| Retention markers (raw-body apply + `discardEligibleEventBodies`) | Stay identity | Short sentinel. Apply/verify compare stored TEXT to `EventBodyUnavailableRetentionMarker`. Canonical encoding would no-op or break those checks. |

## Non-goals

- Changing bundle or archive formats.
- Querying the live store.
