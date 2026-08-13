---
name: traceary-session-history
description: Use when the user asks about prior Traceary sessions, event history, command audits, or what happened earlier in the workspace. Trigger on phrases like "Traceary", "session history", "audit trail", "recent events", or "what happened earlier".
version: 2.1.0
---

# Traceary session history

Use the **Traceary CLI** as the default read path when the user asks about local agent history. Prefer shell invocations of `traceary` over direct SQLite inspection.

## Staged retrieval

Read history in stages. Start with the narrowest useful metadata query, inspect
only the candidates it identifies, and fetch full detail only after selecting a
single event. The same rule applies to prior sessions, event history, command
audits, and "what happened earlier" questions.

### 1. Discovery — metadata only

Start with the current workspace whenever it is known, then run a small
workspace-scoped `traceary list` with a small limit (normally `5`). Add a time
range or `--session-id` when the question provides one.

**Always pass `--fields` on Discovery** so the output includes event `id` and
excludes bodies. Default compact fields are `ts,kind,agent,session,ws,message` —
they omit `id` (so you cannot feed `traceary show` / `session refine --covers-to`)
and include `message` (body). Do **not** use bare `list` / `search` without
`--fields`, and do **not** put `message` in Discovery fields.

```sh
traceary list \
  --workspace "<current workspace>" \
  --from "<known start, if any>" \
  --to "<known end, if any>" \
  --limit 5 \
  --json \
  --fields id,ts,kind,session
```

For a literal query at Discovery, use the same metadata field set:

```sh
traceary search "<query>" \
  --workspace "<current workspace>" \
  --from "<known start, if any>" \
  --to "<known end, if any>" \
  --limit 5 \
  --json \
  --fields id,ts,kind,session
```

Both `list` and `search` accept `--session-id` when a session is already known.
Omit unknown filters rather than guessing them.

Use `traceary session latest` first when the task is specifically about the most
recent session; use `traceary session active` only when it asks about an open
session.

### 2. Inspection — bounded context for selected candidates

After Discovery identifies a narrow session/time slice, make one bounded
context read with a small `--limit` (normally `1`). Keep only filters supported
by `traceary context` and reduce the limit further when one candidate is
enough. Do not turn a broad result set into a full-body dump.

Example (`traceary context`, for bounded surrounding context):

```sh
traceary context \
  --workspace "<current workspace>" \
  --session-id "<selected session, if known>" \
  --limit 1
```

`traceary context` has no event-id filter. It can narrow context only with
`--workspace`, `--session-id`, and `--limit`. Use it only for bounded
surrounding context in a selected workspace/session. If Discovery selected one
event ID, use the Detail path below rather than implying that `context` can
target that event. If its output still leaves several plausible candidates,
refine the metadata query instead of requesting full bodies for all of them.

### 3. Detail — one selected event, with a reason

Retrieve full stored content only after stating why it is needed and selecting
one event ID (or an equivalently single-candidate scope). Prefer the CLI detail
path:

```sh
traceary show <event-id>
```

A full-body bulk read is an exception only when other supported filters produce
an equivalently single-candidate scope; do not use an unbounded history dump as
the first history query.

## Preferred tools

- `traceary session latest`: most recent session metadata for the current workspace
- `traceary session active`: only when the question is specifically about an open session
- `traceary list --json --fields id,ts,kind,session`: Discovery for recent metadata (always include `id`; never `message` at this stage)
- `traceary search "<query>" --json --fields id,ts,kind,session`: Discovery for a narrow literal query (same field rule)
- `traceary context`: bounded surrounding context narrowed only by workspace, session-id, and limit; it cannot target an event id
- `traceary show <event-id>`: full detail for one selected event
- `traceary report`: aggregate usage / activity report when the operator wants a rollup rather than event bodies

## Guardrails

- Prefer CLI reads before direct SQLite inspection.
- Follow Discovery → Inspection → Detail; scope by workspace first and add only filters supported by the chosen command.
- Discovery list/search must use `--fields` that include `id` and exclude `message`. Bare defaults break staged retrieval.
- Keep the first list/search limit small. A bounded `traceary context` is for candidate inspection, not bulk history retrieval.
- Use `traceary show <event-id>` for full detail after a single candidate and a reason have been identified.
- Use `traceary log` / `traceary audit` only when the user explicitly wants to record something.
- Automatic hooks already cover session boundaries and shell command audits.
