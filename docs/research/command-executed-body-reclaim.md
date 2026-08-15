# Decision: reclaim duplicated command_executed bodies during compact (#1853)

[日本語](./command-executed-body-reclaim.ja.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1853

## Decision

`traceary store compact` clears historical `events.body` on `command_executed`
rows that already have a `command_audits` row. It does this on the work copy,
before encoding remaining bodies and `VACUUM INTO`. No new command. Implicit
store open never does this.

`store payload-backfill` and `store dedupe` are gone (#1872). Folding the
reclaim into compact is the remaining operator surface for "rewrite historical
payload representation, then return pages to the filesystem."

## Guards that must survive

- `EXISTS (SELECT 1 FROM command_audits a WHERE a.event_id = events.id)`.
  `traceary log --kind command_executed` writes a body with no audit row;
  that body is the only copy and must stay.
- Empty identity metadata: `encodePayload(nil, identity)` so
  `body_sha256` is the empty-string digest and later integrity checks match
  every other empty write.

## Why not a live cursor

#1853 asked for `payload_backfill_runs`-style resume because a 2.24 GiB live
`UPDATE` would restart from zero if killed. Compact already copies the file,
filters the work copy, journals phases, and keeps the source inode as
rollback. Interruption does not lose the original. Physical bytes drop only
after the same `VACUUM INTO` that compact already runs.

## Measured release

The JSON reports `released_command_body_rows` and
`released_command_body_bytes`. Bytes are
`SUM(length(CAST(body AS BLOB)))` of the selected source rows — stored size,
not `body_plaintext_bytes` (that inflation is #1744).

## VerifyPair

Emptying an available `command_executed` body is a permitted candidate rewrite
when the source row had an audit and a non-empty body, and the candidate body
decodes to empty. Other available-body rewrites still fail.

## Procedure

1. `traceary store backup create …`
2. Fold sessions you want to reclaim (compact still refuses a store whose
   only discardable work is unrefined transcripts).
3. `traceary store compact`
4. If search is drifted after the swap:
   `traceary store search-projection start` then `resume --until-complete`
5. File size is `bytes_after`. Delete the rollback inode when you accept it.

Compact drops the search family on the work copy, so body updates there do
not invalidate a live projection. The operator rebuilds after the swap,
same as the retired payload-backfill procedure.
