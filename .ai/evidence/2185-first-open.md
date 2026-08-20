# #2185 first-open search re-measure (v0.44.1)

Measured 2026-08-20 against the **live** operator store with Homebrew `traceary 0.44.1` (`c8edab93`). No repo-built binary was pointed at `~/.config/traceary/traceary.db`. An isolated 13 GiB copy was not made (`copy-unavailable`: 32 GiB free; brew-on-live is the operator path after #2186).

Store: `~/.config/traceary/traceary.db` 13 GiB, search-projection generation complete (v0.44.1).

| query | wall (real) | exit | notes |
|---|---|---|---|
| `search golangci --json --limit 5` first | **2.81 s** | 0 | hits returned (`events` length 5) |
| same query immediately after | **0.18 s** | 0 | warm |
| `search xyzzy-nomatch-2127 --json --limit 5` | **0.42 s** | 0 | empty, ≤2 s |
| `search compact --json --limit 5` | **1.88 s** | 0 | selective filterable, ≤2 s |

JSON object keys on the first success: `events`, `sessions` (unchanged vs v0.44.1).

## Verdict

The v0.44.0 live first-open of **83.14 s** does **not** reproduce on v0.44.1. That hit coincided with #2186 (`SQLITE_BUSY` on already-current schema under the 1 s hook timeout). After the operator-open retry, first filterable search is 2.81 s.

Remaining first-open (~2.6 s above the 0.18 s warm path) is cold process + 13 GiB page-cache fill, not the 4096-row candidate scan (`index_incomplete` did not fire; hits returned). No cache rewrite.

Warm no-match and selective stay ≤2 s. Unfilterable short queries remain out of scope.

This issue should close with this evidence. No code change.
