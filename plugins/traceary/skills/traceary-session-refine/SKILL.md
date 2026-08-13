---
name: traceary-session-refine
description: Use when Traceary asks for a session refinement (consolidation threshold), or when the user asks to summarize/refine the current session so event bodies can later be discarded. Trigger phrases — "session refine", "refine this session", "session summary", "write a refinement", "セッション要約", "refinement を書いて". Do not use for durable L3 memory facts (that is traceary-memory-remember) or inbox review.
version: 1.0.0
---

# Traceary session refine

Use this skill to write an **agent-authored session refinement** (L2 summary).
Traceary stores the text you hand it; it never composes the summary itself.
A body is discardable only once a refinement covers it, so this skill is the
record pillar's write path.

## What the summary must contain

Required:

1. **Motivation** — why this work was undertaken.
2. **The change** — what was actually changed.

Optional, when there is room:

3. **How it went** — the course of events, including approaches tried and rejected.

A summary that answers 1 and 2 is worth keeping. A list of what happened without
saying why is not — that is what the mechanical fallback already produces for free.

## Workflow

1. **Identify the session**. Prefer the session named in the consolidation ask.
   Otherwise:

   ```sh
   traceary session active
   # or
   traceary session latest
   ```

2. **Discover coverage and prior summary**. Use staged reads from
   `traceary-session-history` (list → context → show) to learn the latest event
   that should be covered and whether a previous refinement already exists.
   When the consolidation message includes a previous summary and
   `covers_to=…`, treat that text as the base to **merge**, not a blank slate.

3. **Pick `--covers-to`**. Choose the latest event id this summary honestly
   covers. Do not claim coverage past events you did not read. Replaying the
   same covers-to range is a no-op.

4. **Compose the summary** with Motivation and The change first. Add How it
   went only if it still fits. When merging, keep durable facts from the prior
   summary and fold in new work since the previous `covers_to`.

5. **Store the refinement**:

   ```sh
   traceary session refine <session-id> \
     --covers-to <event-id> \
     --summary "<Motivation + The change; optional How it went>" \
     --produced-by agent
   ```

   Add `--keywords` when short search terms help later retrieval. Use `--json`
   when you need the outcome / generation fields.

6. **Confirm to the operator** that the refinement was stored (or was a no-op
   for an unchanged covers-to range).

## Guardrails

- **Required content only is mandatory.** Motivation and The change must be present; How it went is optional.
- **Merge, do not rewrite.** When a previous summary is supplied, extend it rather than discarding earlier motivation/change.
- **No durable L3 memory here.** Explicit "remember that …" facts go to `traceary-memory-remember`.
- **No secrets.** Do not put secret-shaped values into `--summary` or `--keywords`.
- **Do not invent coverage.** If you cannot name a real `--covers-to` event id, stop and re-run Discovery instead of guessing.
