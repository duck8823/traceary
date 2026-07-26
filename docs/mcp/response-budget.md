# Event response budget

[日本語](./response-budget.ja.md)

`list_events`, `search`, and `get_context` use a shared response budget.

- The default page is 20 events; a request may return at most 100.
- Bounded bodies default to 500 runes and may not exceed 16,384 runes.
- Encoded `body` and `body_blocks` fields together are capped at 64 KiB, including when `full_body=true` or `body_limit=0`.
- Responses include `coverage`, `partial`, and `reasons`. An opaque `continuation` is included when another event remains after page-size or aggregate-body reduction.

A continuation is encrypted, authenticated, versioned, tied to one tool and normalized request shape, and cannot be combined with `offset`. All three tools preserve one resolved upper time bound and resume with the last `(created_at, event_id)` key, so concurrent newer writes and equal timestamps do not shift or duplicate later pages. Tokens are valid only for the MCP server process that issued them; clients must treat them as opaque and restart paging after a server restart.

## Design and behavioral invariants

| Concept | Owner | Invariant |
|---|---|---|
| Response budget | application value objects | The item, per-body, and aggregate limits are validated before a query runs. |
| Candidate page | SQLite query adapter | Membership is ordered by `(created_at_norm, id)` and an anchored page uses a composite keyset seek. |
| Bounded hydration | bounded read use case and SQLite adapter | The exact metadata candidates selected for the page are hydrated; filters are not re-run between membership and hydration. |
| Continuation | MCP adapter | A token is decoded once, authenticated, and bound to the tool, normalized request, snapshot upper bound, and last emitted event. |
| Body-budgeted prefix | MCP adapter | Only a contiguous prefix is returned. If the next event cannot retain a body, it is not emitted and the continuation remains anchored to the last emitted event. |

Observable behavior is protected by tests for equal-timestamp pagination, concurrent inserts, anchored query plans, bounded candidate reuse, continuation restart errors, aggregate-budget recovery on the following page, truncation including the ellipsis, and the published MCP tool schema fixture.
