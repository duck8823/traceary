# Event response budget

[日本語](./response-budget.ja.md)

`list_events`, `search`, and `get_context` use a shared response budget.

- The default page is 20 events; a request may return at most 100.
- Bounded bodies default to 500 runes and may not exceed 16,384 runes.
- Encoded `body` and `body_blocks` fields together are capped at 64 KiB, including when `full_body=true` or `body_limit=0`.
- Responses include `coverage`, `partial`, `reasons`, and an opaque `continuation` when another page or a body-budget reduction remains.

A continuation is encrypted, authenticated, versioned, tied to one tool and normalized request shape, and cannot be combined with `offset`. All three tools preserve one resolved upper time bound and resume with the last `(created_at, event_id)` key, so concurrent newer writes and equal timestamps do not shift or duplicate later pages. Tokens are valid only for the MCP server process that issued them; clients must treat them as opaque and restart paging after a server restart.
