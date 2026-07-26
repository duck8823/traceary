# Event response budget

[日本語](./response-budget.ja.md)

`list_events`, `search`, and `get_context` use a shared response budget.

- The default page is 20 events; a request may return at most 100.
- Bounded bodies default to 500 runes and may not exceed 16,384 runes.
- Encoded `body` and `body_blocks` fields together are capped at 64 KiB, including when `full_body=true` or `body_limit=0`.
- Responses include `coverage`, `partial`, `reasons`, and an opaque `continuation` when another page or a body-budget reduction remains.

A continuation is versioned, tied to one tool and request shape, and cannot be combined with `offset`. List and search continuations preserve the resolved upper time bound so an omitted `to` does not drift while paging. Clients must treat the token as opaque.
