# Workspace identity contract

[日本語](./workspace-identity-contract.ja.md)

Status: design contract for v0.30.0 implementation issues #1435 and #1429.

## Requirement summary

Traceary currently stores `workspace` on both sessions and events, but the two values do not have an explicit semantic distinction.
Hook state also tends to copy the workspace selected at session start onto later events.
That keeps a session internally consistent, but it cannot faithfully represent an agent that starts in one repository and executes a command in another.

This contract defines two separate facts:

- a session has one immutable **canonical workspace**;
- every event has the **effective workspace** where that event occurred.

The contract preserves existing session IDs, event IDs, foreign-key relationships, `workspace` columns, and default filters.
It does not delete duplicate rows, infer aliases from body equality, or change one-shot terminal lifecycle rules.

## Current behavior and terminology

The existing physical schema already provides one workspace column per aggregate:

| Existing column | v0.30 semantic name | Meaning |
|---|---|---|
| `sessions.workspace` | canonical workspace | Stable session attribution selected when the session row is first created |
| `events.workspace` | effective workspace | Event-local attribution selected when the event is recorded |

The migration does not rename these columns.
Renaming would break older binaries and scripts without adding information.
Application and presentation code may add explicit `CanonicalWorkspace` and `EffectiveWorkspace` vocabulary while retaining `Workspace` as the compatibility accessor during the v0.x compatibility window.

An empty string means that the recorder did not know the workspace.
It does not mean the filesystem root, the user's home directory, or a global scope.

## Conceptual model

| Concept | State | Behavior | Invariant |
|---|---|---|---|
| Session canonical workspace | one workspace value, possibly unknown | selected at first successful session start | immutable for a session ID |
| Event effective workspace | one workspace value, possibly unknown | resolved for each delivered event | never rewrites session canonical attribution |
| Workspace observation | stable observation ID, raw host value, normalized workspace, host/source facts | records how an attribution was obtained | append-only provenance; no event body |
| Workspace relationship | exact, descendant, ancestor, explicit alias, conflict, or unknown | classifies an observation relative to the canonical workspace | classification does not change either workspace value |
| Compatibility workspace filter | surface-specific legacy selector | applies the v0.29 meaning of `--workspace` / `workspace` | no result-set widening by default |

The canonical workspace answers “where did this session begin and remain anchored?”
The effective workspace answers “where did this event happen?”
A session spanning repositories is represented by one canonical value and any number of event-local effective values under the same session ID.

### Workspace value rules

Workspace strings remain opaque domain values after trimming.
Recognized Git remote identities such as `github.com/org/repo` and normalized absolute paths are both valid.
Traceary must not automatically claim that a remote identity and a local checkout path are aliases, because multiple worktrees, forks, and remotes make that inference ambiguous.

Path normalization may collapse redundant separators and `.` segments using the host's path syntax.
It must not resolve symlinks, access the network, or require a repository to exist at read time.
The raw observed string belongs in observation provenance when it differs from the normalized value.

## Attribution decisions

### Session canonical workspace

The first successful session-row insert fixes the canonical workspace.
Later retries, resume hooks, event deliveries, and end hooks cannot replace it.

The start path selects the first available value in this order:

1. an explicit operator-provided `TRACEARY_WORKSPACE` value;
2. a host-native session-start workspace or workspace path;
3. repository identity detected from the session-start current working directory;
4. the normalized absolute session-start current working directory;
5. unknown (`""`).

An idempotent start retry may supply a different workspace.
The retry retains the stored canonical workspace and records the new value as an observation classified against the canonical value.
It must not append another logical `session_started` event once cross-host delivery identity from #1435 proves the delivery is a retry.
Workspace and other attribution fields are excluded from the delivery fingerprint, so a changed retry attribution cannot turn one logical start into an identity conflict.
The new attribution gets its own supplemental observation only when its attribution fingerprint is new.

A child session selects its own canonical workspace from explicit child-start evidence.
When the child start carries no workspace evidence, it inherits the parent canonical workspace.
The parent value is a fallback, not a requirement that parent and child events occur in the same repository.

### Event effective workspace

Each event resolves an effective workspace independently from the session canonical workspace.
The first available event-local value wins:

1. an explicit operator-provided event override;
2. an event-specific tool working directory or host workspace field;
3. the hook payload current working directory;
4. repository identity detected from that event-local directory;
5. the stored session canonical workspace;
6. unknown (`""`).

Host adapters normalize their payload into this decision input.
The application rule does not inspect host JSON field names.
The selected effective workspace is stored on the event and never copied back into the session row.
Once inserted, that event workspace is immutable; retry attribution is retained only through supplemental observations and never rewrites the original event.

Boundary events use the session canonical workspace unless the host provides an unambiguous boundary-local workspace.
In either case the session row remains unchanged.

### Relationship classification

Relationship classification is diagnostic and must not block event persistence.
The following order produces one classification:

1. `unknown`: either value is empty;
2. `exact`: normalized values are identical;
3. `explicit_alias`: an operator-reviewed alias links the values;
4. `descendant`: both values are absolute local paths and the effective path is below the canonical path;
5. `ancestor`: both values are absolute local paths and the effective path contains the canonical path;
6. `conflict`: both values are known and none of the preceding relations apply.

Remote identifiers only become aliases through an explicit reviewed record.
Body equality and temporal proximity are never workspace-alias evidence.

## Responsibilities and boundaries

| Responsibility | Owner | Not owned by |
|---|---|---|
| canonical/effective value objects and relationship rules | domain/application types | hook handlers, SQLite scans |
| selection orchestration from normalized evidence | session/event use cases | host-specific JSON adapters |
| host payload field extraction | host adapter in `presentation/cli` | domain model |
| immutable persistence and observation transaction | repository boundary | CLI/MCP serializer |
| compatibility filter mapping | query criteria and query service | individual Cobra handlers |
| diagnostic rendering | doctor/report presentation | persistence layer |

The application boundary should receive a consumer-oriented attribution input instead of boolean flags:

```text
SessionWorkspaceAttribution {
    explicit_workspace?
    host_workspace?
    event_local_directory?
    session_canonical_fallback?
    raw_observation?
    source_client
    source_hook
}
```

This is a conceptual interface, not a required Go DTO name.
Host adapters may construct smaller commands for session start and event recording as long as the selection order and unknown-value contract remain observable.

Raw workspace attribution is required provenance, not a best-effort diagnostic.
Every successful path that creates a workspace-bearing event atomically commits that event and its primary workspace observation; a genuine schema, I/O, or constraint failure fails that transaction rather than leaving an event with falsely complete provenance.
An exact delivery-identity retry is an idempotent success and never adds a second event.
It adds no observation when attribution is unchanged, and adds one supplemental observation when the attribution fingerprint is new.

Relationship classification and aggregate reports are derived diagnostics.
Malformed optional evidence records `unknown` plus a `diagnostic_reason` while retaining the raw observation; it does not reject an otherwise valid event.
Failure to update a secondary aggregate report must not block primary event and raw-provenance persistence.

## Additive migration and backfill

The implementation migration is additive and leaves both existing `workspace` columns in place.
It adds append-only provenance and reviewed-alias tables rather than duplicating canonical/effective values into new columns:

```sql
CREATE TABLE session_workspace_aliases (
    session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
    alias_workspace TEXT NOT NULL,
    reviewed_at TEXT NOT NULL,
    reviewed_by TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, alias_workspace)
);

CREATE TABLE hook_deliveries (
    delivery_record_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    reported_delivery_id TEXT NOT NULL,
    delivery_fingerprint TEXT NOT NULL,
    identity_status TEXT NOT NULL
        CHECK (identity_status IN ('accepted', 'conflict')),
    observed_event_id TEXT NOT NULL,
    accepted_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT '',
    UNIQUE (session_id, reported_delivery_id, delivery_fingerprint)
);

CREATE UNIQUE INDEX idx_hook_deliveries_accepted_identity
    ON hook_deliveries(session_id, reported_delivery_id)
    WHERE identity_status = 'accepted';

CREATE TABLE hook_delivery_attempts (
    delivery_record_id TEXT NOT NULL,
    attempted_event_id TEXT NOT NULL,
    outcome TEXT NOT NULL
        CHECK (outcome IN ('accepted', 'conflict', 'exact_redelivery')),
    observed_at TEXT NOT NULL,
    PRIMARY KEY (delivery_record_id, attempted_event_id)
);

CREATE TABLE session_workspace_observations (
    observation_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    raw_workspace TEXT,
    observation_kind TEXT NOT NULL
        CHECK (observation_kind IN ('primary', 'supplemental')),
    observation_origin TEXT NOT NULL
        CHECK (observation_origin IN ('runtime', 'backfill')),
    observed_relationship TEXT NOT NULL
        CHECK (observed_relationship IN ('exact', 'descendant', 'ancestor', 'explicit_alias', 'conflict', 'unknown')),
    observed_event_id TEXT,
    delivery_record_id TEXT,
    attribution_fingerprint TEXT NOT NULL,
    diagnostic_reason TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_session_workspace_observations_relationship
    ON session_workspace_observations(observed_relationship, observed_at DESC, session_id);

CREATE UNIQUE INDEX idx_session_workspace_observations_delivery_attribution
    ON session_workspace_observations(delivery_record_id, attribution_fingerprint)
    WHERE delivery_record_id IS NOT NULL AND delivery_record_id <> '';

CREATE UNIQUE INDEX idx_session_workspace_observations_primary_event
    ON session_workspace_observations(observed_event_id)
    WHERE observation_kind = 'primary'
      AND observed_event_id IS NOT NULL AND observed_event_id <> '';
```

The tables store attribution metadata only.
It must not copy command input, output, prompt text, transcript text, or other event body content.
Reports calculate counts from observation rows; a count is not a deduplication decision.

An alias is scoped to one session and requires a reviewed operation with reviewer and timestamp.
Adding an alias does not rewrite historical `observed_relationship` values.
Reports may expose both the relationship observed at ingestion and the current relationship derived by joining reviewed aliases.

`observed_event_id` in both provenance tables is an immutable value rather than a foreign key.
Deleting or archiving an event therefore cannot mutate append-only delivery or workspace provenance; reports discover current event availability through a left join.

An accepted reported delivery identity has the namespace `<client>:<hook-kind>:<native-session-id>:<native-delivery-id>` (or an equivalent typed representation).
Host adapters must keep it stable for one logical delivery and must not reuse a native ID across hook kinds or session IDs.
When a host provides no stable native ID, no `hook_deliveries` row is created and repeated equal bodies remain distinct legitimate deliveries.

The delivery fingerprint covers the normalized semantic delivery envelope, including hook kind and a body digest when present, but excludes workspace and other attribution fields that may legitimately improve on retry.
Body equality is only used to validate a stable reported identity; it is never sufficient identity or deduplication evidence by itself.
The separate attribution fingerprint covers normalized workspace, raw workspace, and other stable attribution inputs.

The repository handles a reported identity in this order:

1. If `(session_id, reported_delivery_id, delivery_fingerprint)` already exists, it is the same logical delivery. Do not add an event. Insert a supplemental observation only when `(delivery_record_id, attribution_fingerprint)` is new; otherwise return an exact idempotent success.
2. If the reported identity has no accepted row, atomically insert an `accepted` delivery row, the event, and one `primary` observation.
3. If an accepted row exists with another delivery fingerprint, atomically insert a `conflict` delivery row keyed by the new fingerprint, preserve the new legitimate event and primary observation, and record `diagnostic_reason=delivery_identity_conflict`.

The delivery table's triple uniqueness makes a retry of an already recorded conflict resolve through step 1, so it cannot amplify events.
The partial accepted-identity index identifies the first accepted fingerprint; the application comparison and full ledger, not that index alone, implement idempotency.
The primary-event index permits one primary attribution per persisted event, while supplemental observations may retain improved retry attribution for that same event.

Concurrent delivery handling is also part of the repository contract.
If an accepted-identity or triple-identity unique collision occurs after the initial lookup, the repository rolls back that attempted write, reloads the append-only ledger in a new transaction, and performs the decision once more:

- the same fingerprint follows step 1 as an idempotent success;
- a different fingerprint follows step 3 and preserves one conflict event;
- a triple-identity collision while inserting that conflict reloads the now-existing conflict row and follows step 1.

Each named collision permits one constraint-driven reload; a delivery can therefore re-enter the decision at most twice (once after an accepted-identity race and once after a conflict-triple race), never in an unbounded retry loop.
Only the two named identity uniqueness conflicts take this branch; busy timeouts, check violations, schema errors, and I/O failures remain genuine transaction failures.
The observation `session_id` is deliberately not a foreign key: historical and direct event writes can contain a session ID without a materialized `sessions` row, and migration must preserve those rows.
Such observations classify as `unknown` until a session row exists; reviewed aliases still require an existing session through their foreign key.

The schema migration only creates the additive tables and indexes in a short transaction.
It does not scan all events while holding the schema transaction.
After that transaction, a resumable catch-up worker processes at most 1,000 events without a primary observation per write transaction, using stable `(created_at, id)` keyset pagination and `INSERT ... ON CONFLICT DO NOTHING` for `backfill:<event-id>` observations.
The batch size is configurable for migration tests and constrained environments.
Database initialization may succeed after the schema transaction and before catch-up reaches full coverage.
Catch-up yields between bounded batches; incomplete coverage never blocks normal ingestion and is exposed only as incomplete diagnostics.

Catch-up performs the following work:

1. keep every `sessions` and `events` row unchanged;
2. insert one primary observation with `observation_origin=backfill` for each event that has no primary observation, using `backfill:<event_id>` as `observation_id`; an existing primary observation or backfill ID is an idempotent already-complete result;
3. keep the event timestamp and event ID as `observed_at` and `observed_event_id`;
4. create no delivery-ledger row and leave `raw_workspace` unknown because historical rows do not preserve stable delivery identity;
5. classify against `sessions.workspace` using only exact and local ancestor/descendant rules;
6. classify a known nonmatching pair as `conflict`, and use `unknown` only when the session row or either workspace value is unavailable;
7. create no alias rows automatically and never use equal bodies as alias or delivery evidence.

The backfill source client/hook fields come from each event when available.
Every upgraded initialization resumes catch-up for missing observations, so events written during a rollback to an older binary are filled on the next upgrade.
Diagnostics expose catch-up coverage and never claim complete historical measurement until the missing count reaches zero.

The implementation must include migration tests for empty workspaces, events without materialized session rows, sessions spanning multiple effective workspaces, Windows paths, exact-redelivery identity comparison, changed retry attribution, repeated identity-conflict delivery, reviewed aliases, immutable observed event IDs after event deletion, bounded batch commits, interrupted catch-up, and idempotent re-open after migration.

### Rollback

Normal rollback means deploying the previous binary and retaining but ignoring the additive delivery, observation, and alias tables.
The previous binary continues to read and write the unchanged `sessions.workspace` and `events.workspace` columns.
No down migration may rewrite either workspace column or delete events.
Events written by the previous binary create a temporary observation gap; resumable catch-up fills that gap after re-upgrade.
Dropping the additive tables is not a rollback operation because runtime-only raw attribution and delivery provenance cannot be reconstructed from events.
Any later destructive retirement requires a separately reviewed export-and-merge migration that preserves both rollback-period events and the exported provenance; restoring a database backup alone is insufficient.

Rollback is required if the migration changes existing row counts or IDs, blocks normal event ingestion, widens default workspace filters, or classifies more than 5% of sampled known pairs as conflicts during dogfooding without an explained host fixture.

## Query and output compatibility

v0.30 keeps the current selector meaning per surface:

| Surface | Existing `workspace` selector matches | Compatibility rule |
|---|---|---|
| `list`, `search`, `tail`, MCP event reads | event effective workspace | exact filter remains exact |
| `sessions`, session tree | session canonical workspace | exact filter remains exact |
| `session handoff`, memory/context pack | canonical session first, then existing local descendant evidence fallback | no remote alias inference |
| event JSON `workspace` | effective workspace | field meaning is documented, field name retained |
| session JSON `workspace` | canonical workspace | field meaning is documented, field name retained |

Future explicit selectors may add `workspace_scope=effective`, `canonical`, or `either`.
They must be enum-like criteria, not boolean flags.
`either` returns a union keyed by event/session identity so one row cannot appear twice.
No new selector changes the v0.30 default.

Additive output fields may expose `canonical_workspace`, `effective_workspace`, and relationship facts after their producer and fixtures are implemented.
Unknown facts are omitted or rendered as `unknown`; they are not serialized as false equivalence.

### Handoff and context-pack result membership

The v0.30 handoff resolver keeps the following precise contract for optional session ID `S`, requested workspace `W`, and `active_only`:

1. Query canonical sessions by `S`, exact `W`, and `active_only`, ordered by `started_at DESC, session_id DESC`; the first row wins.
2. Only when that query is empty and `W` is an absolute local path, visit its ancestors from closest parent to filesystem root.
3. For each ancestor `A`, visit only sessions whose canonical workspace is exactly `A`, in `started_at DESC, session_id DESC` pages of 50, and select the first candidate whose event history contains at least one event with session ID equal to the candidate and effective workspace exactly equal to `W`.
4. `S`, when present, and `active_only` remain filters during fallback. Remote identities never use ancestor fallback.
5. If no candidate qualifies, return no matching session.

An exact canonical match therefore wins over a newer ancestor session with effective-workspace evidence.
The selected session ID determines the handoff session metadata, recent command items, compact summary, and other event sections; those sections may contain events from every effective workspace in that session and are not re-filtered to `W`.
Memory candidates use deduplicated scopes in this order: selected canonical workspace, requested `W` when different, selected session family, then each valid selected-session agent.
The context pack exposes the selected canonical workspace and retains `W` as the requested-workspace match note.

Integration tests must lock the selected session ID, candidate ordering, event-section membership, memory-scope ordering, exact-over-fallback precedence, session-ID and active filters, no-match behavior, and the absence of remote fallback.

## Cross-host fixtures

Each host fixture uses canonical repository A and effective repository B.
The acceptance assertion is the same: one session row remains anchored to A, the event is stored under B, and a conflict or reviewed alias observation is visible without changing either value.

| Host | Canonical evidence | Event-local effective evidence | Required fixture assertion |
|---|---|---|---|
| Codex | `SessionStart.cwd` resolves to repository A | `PostToolUse.cwd` resolves to repository B | audit event B; session A; repeated delivery keeps same logical event |
| Claude | `SessionStart.cwd` resolves to A | `PostToolUse.tool_input` working directory or hook `cwd` resolves to B | command/tool event B; prompt without event-local evidence falls back to A |
| Antigravity | first `PreInvocation.workspacePaths` entry is A | `PreToolUse.toolCall.args.Cwd` is B and is paired with `PostToolUse` | paired audit B; subsequent `PreInvocation` cannot replace session A |
| Gemini CLI | `SessionStart.cwd` resolves to A | `AfterTool.cwd` for `run_shell_command` resolves to B | audit B; `AfterAgent` without a new cwd uses A |
| Grok Build | session-start `cwd` resolves to A | tool completion `cwd` resolves to B | audit B; replayed hook delivery is idempotent |
| Kimi Code | `SessionStart.cwd` resolves to A | `PostToolUse.cwd` resolves to B | audit B; `source=resume` preserves session A |

Fixtures must use the versioned, sanitized host payload shapes already kept under `presentation/cli/testdata` and the field inventory in `docs/hooks/host-contract.json`.
If a host does not supply event-local working-directory evidence for a hook, the fixture must assert canonical fallback rather than invent B.

## Observable behavior tests

| Given | When | Then | Level |
|---|---|---|---|
| a new session with canonical A | an event reports effective B | session remains A and event is B | application + SQLite integration |
| an existing session A | a resume/start retry reports B | canonical stays A and observation records B | application behavior |
| canonical local parent path | event occurs in child path | relationship is `descendant` | domain table test |
| canonical remote identity and local checkout | no reviewed alias exists | relationship is `conflict` | domain table test |
| a reviewed alias connects the values | event occurs at the alias | relationship is `explicit_alias` | application + SQLite integration |
| legacy DB rows | additive migration runs | IDs, foreign keys, row counts, and workspace columns are unchanged | migration test |
| legacy event filter for B | session canonical is A and event effective is B | event appears exactly once | query integration |
| legacy session filter for B | session canonical is A | session does not appear unless an explicit new scope is requested | query integration |
| event-local workspace is absent | event is recorded | effective workspace falls back to canonical A | host fixture |
| both values are unknown | event is recorded | event persists and relationship is `unknown` | integration |

Tests protect observable values and result membership.
They must not assert private helper call order or require a specific host adapter implementation.

## TDD and implementation sequence

This design issue ships only this contract.
Production changes remain split by the existing issue hierarchy:

1. **#1435 schema-first ingest identity and host attribution**: first add the additive schema, then red tests for immutable canonical selection, event-local effective selection, exact-retry comparison, identity conflict preservation, and cross-host delivery retries; implement domain/application attribution, atomic event/observation writes, resumable catch-up, and host adapters while keeping default queries unchanged.
2. **#1429 diagnostics and measurement**: expose conflict/alias diagnostics, catch-up coverage, and dry-run historical analysis, then dogfood copied local data. It consumes the observation schema from #1435 rather than introducing that schema after runtime writers.
3. **release QA #1437**: run all host fixtures, migration-copy tests, filter compatibility tests, and sampled conflict review before v0.30.0.

For each behavior, the red test should assert the stored canonical/effective values or public result membership.
The smallest green implementation may reuse existing `workspace` columns.
Refactoring then introduces explicit vocabulary and removes host handlers that decide core relationship rules.

## Structure review checkpoint

The design is rejected if a hook handler owns canonical/effective precedence, if a use case switches behavior through multiple booleans, if SQLite DTOs leak into domain rules, or if tests pin private call order.
The design is also rejected if session canonical workspace changes as a side effect of event recording, if derived classification rejects an otherwise valid event, or if event persistence can claim complete provenance without atomically retaining the required raw attribution.

The implementation should prefer one attribution decision owner, small host adapters, and repository transactions with visible failure contracts.
Aliases require an explicit reviewed operation; no host adapter may create them heuristically.

## Dogfood and release evidence

Dogfooding uses a copied database, never the active store.
The report records:

- session and event row counts before and after migration;
- count and rate of exact, descendant, ancestor, explicit-alias, conflict, and unknown observations by client/source hook;
- sampled conflict event IDs without event bodies;
- false-positive review for every supported host fixture;
- default-filter result counts before and after the change.

If known conflict attribution exceeds 5% overall or one host differs materially from its fixture, file a follow-up issue and implement it before the v0.30.0 tagged release.
The separate exact-delivery duplicate target remains below 1% as required by #1429.

## Non-goals

- deleting or merging historical events;
- treating body equality as identity or alias evidence;
- changing session terminal-state semantics;
- resolving symlinks or querying remote providers to normalize workspaces;
- widening legacy workspace filters;
- storing command, prompt, or transcript bodies in workspace diagnostics.
