# Archive and backup capacity retention

[English] | [日本語](file-capacity-retention.ja.md)

Traceary v0.31 adds a hidden, explicit plan/apply workflow for bounding local archive and SQLite-backup roots by age, count, and allocated bytes. It is not automatic and does not compact the live database.

## Safety contract

- `plan` is read-only. It writes only the explicitly requested plan output.
- Archive and backup roots are evaluated independently.
- A non-empty class needs a verified current-store recovery floor. The newest matching recovery point is protected.
- An unreadable entry, symlink, hard link, device boundary, invalid reserved manifest, or unknown allocation needed by a byte ceiling makes that class `indeterminate`; it has no apply batches.
- Corrupt, unverified, orphaned, and pinned files remain visible and count toward known pressure, but never become deletion candidates.
- Every candidate is bound to root device/inode, file device/inode/link count, size, mtime, SHA-256, verification evidence, all reasons, and one ordered batch.
- `apply` requires the exact plan ID, rechecks the complete remaining inventory and recovery floor, and expires plans after one hour by default.
- Deletion uses an exclusive root lock, durable catalog/journal/ledger files, and an atomic same-directory no-replace rename to a reserved tombstone. The moved tombstone is verified before unlink; a raced replacement is restored when the original name is still free and is never unlinked. A retry resumes only a recognized state and never overwrites a tombstone.

New SQLite backups also receive a digest-bound reserved retention manifest. A legacy backup without this manifest is reported as `backup_manifest_missing` and does not authorize deletion. Existing store archives are verified through their internal manifest and table digests; encrypted archives are report-only until an explicit passphrase-aware verifier is added.

## Plan

The commands are hidden while v0.31 dogfooding is in progress.

```sh
traceary store retention files plan \
  --db-path ~/.config/traceary/traceary.db \
  --archive-root ~/.config/traceary/archives \
  --archive-max-age 2160h \
  --archive-max-count 12 \
  --archive-max-allocated-bytes 1073741824 \
  --backup-root ~/.config/traceary/backups \
  --backup-max-age 720h \
  --backup-max-count 8 \
  --backup-max-allocated-bytes 2147483648 \
  --output /tmp/traceary-file-retention-plan.json
```

Review `canonical_payload.classes[].inventory`, `floor`, `status`, `ceilings`, `candidates`, and `batches`. `display` is non-authoritative. Only classes with `status: satisfied` may be applied.

## Apply and retry

```sh
PLAN_ID=$(jq -r .plan_id /tmp/traceary-file-retention-plan.json)
traceary store retention files apply \
  --plan /tmp/traceary-file-retention-plan.json \
  --confirm-plan-id "$PLAN_ID"
```

If the process is interrupted, keep the plan and run the same command again after the one-minute fenced lease expires. Do not rename or edit `.traceary-retention-*` state files. A successful retry converges each candidate to `committed`, one matching ledger row, and catalog state `deleted`.

## Recovery verification

Before enabling a real cleanup policy, copy the retained floor to a disposable store and verify:

```sh
sqlite3 copied-backup.db 'PRAGMA integrity_check;'
traceary store archive verify --input retained.trcaryar
```

Then restore into a disposable Traceary database and run representative metadata and full-body reads. Capacity cleanup and SQLite compaction remain separate operations.
