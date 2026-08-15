# Search-projection budget verdict fallback (#1835)

[日本語](./search-projection-budget-verdict.ja.md)

Scratch-sized measurement of the index-family budget verdict. Not the live ~36 GiB store.

## Decision

`-1` stays unknown. When the split walk is unavailable and the family **total** is available, publish a coarse `0`/`1` against that total. When the total is also unavailable, keep `-1` and require `physical_evidence.reason`.

No new flag. No new JSON field.

The 118 mid-rebuild samples were `-1` because the verdict was written only at cutover from the 3s split. `physical_bytes` was already complete on the same store.

## Tests

- `TestSearchProjectionStatusBudgetVerdictUsesFamilyTotalWhenPersistedUnknown`
- `TestSearchProjectionStatusUnknownBudgetVerdictNamesReason`
- `TestSearchProjectionCutoverEvidence_CompletionSurvivesUnmeasurableFamily` (1ns split still completes; after-bytes come from the total)
