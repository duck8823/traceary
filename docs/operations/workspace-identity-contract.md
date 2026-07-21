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

Boundary events use the session canonical workspace unless the host provides an unambiguous boundary-local workspace.
In either case the session row remains unchanged.

### Relationship classification

Relationship classification is diagnostic and must not block event persistence.
The following order produces one classification:

1. `unknown`: either value is empty;
2. `exact`: normalized values are identical;
3. `explicit_alias`: an operator-reviewed alias links the values;
4. `descendant`: the effective local path is below the canonical local path;
5. `ancestor`: the effective local path contains the canonical local path;
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

Persistence commits the event and its workspace observation atomically when an observation is present.
A diagnostic write failure must fail that transaction rather than leave an event with falsely complete provenance.
Classification failure caused by malformed optional evidence may fall back to `unknown`, but it must retain the raw observation and a diagnostic reason.

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

CREATE TABLE session_workspace_observations (
    observation_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    raw_workspace TEXT,
    observed_relationship TEXT NOT NULL
        CHECK (observed_relationship IN ('exact', 'descendant', 'ancestor', 'explicit_alias', 'conflict', 'unknown')),
    event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    delivery_id TEXT,
    observed_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_session_workspace_observations_relationship
    ON session_workspace_observations(observed_relationship, observed_at DESC, session_id);

CREATE UNIQUE INDEX idx_session_workspace_observations_delivery
    ON session_workspace_observations(session_id, delivery_id)
    WHERE delivery_id IS NOT NULL AND delivery_id <> '';

CREATE UNIQUE INDEX idx_session_workspace_observations_event
    ON session_workspace_observations(event_id)
    WHERE event_id IS NOT NULL AND event_id <> '';
```

The tables store attribution metadata only.
It must not copy command input, output, prompt text, transcript text, or other event body content.
Reports calculate counts from observation rows; a count is not a deduplication decision.

An alias is scoped to one session and requires a reviewed operation with reviewer and timestamp.
Adding an alias does not rewrite historical `observed_relationship` values.
Reports may expose both the relationship observed at ingestion and the current relationship derived by joining reviewed aliases.

Runtime observation IDs come from the stable delivery identity defined by #1435 when available, with a domain-generated ID otherwise.
The partial unique indexes make an exact redelivery idempotent and permit at most one attribution observation per persisted event without merging legitimate deliveries that lack the same delivery ID.
The observation `session_id` is deliberately not a foreign key: historical and direct event writes can contain a session ID without a materialized `sessions` row, and migration must preserve those rows.
Such observations classify as `unknown` until a session row exists; reviewed aliases still require an existing session through their foreign key.

Backfill runs in the schema transaction:

1. keep every `sessions` and `events` row unchanged;
2. insert one observation per existing event, using `backfill:<event_id>` as `observation_id`;
3. keep the event timestamp and event ID as `observed_at` and `event_id`;
4. leave `delivery_id` and `raw_workspace` unknown because historical rows do not preserve them;
5. classify against `sessions.workspace` using only exact and local ancestor/descendant rules;
6. use `unknown` when the session row or either workspace value is unavailable;
7. create no alias rows automatically and never use equal bodies as alias or delivery evidence.

The backfill source client/hook fields come from each event when available.

The implementation must include migration tests for empty workspaces, events without materialized session rows, sessions spanning multiple effective workspaces, Windows paths, exact-redelivery uniqueness, reviewed aliases, deleted-event `SET NULL` behavior, and idempotent re-open after migration.

### Rollback

Rollback means deploying the previous binary and ignoring or dropping the additive observation and alias tables after taking a backup.
The previous binary continues to read and write the unchanged `sessions.workspace` and `events.workspace` columns.
No down migration may rewrite either workspace column or delete events.

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

1. **#1435 ingest identity and host attribution**: add red tests for immutable canonical selection, event-local effective selection, and cross-host delivery retries; implement domain/application attribution; adapt hosts; keep default queries unchanged.
2. **#1429 diagnostics and measurement**: add the observation-table migration and backfill; expose conflict/alias diagnostics and dry-run historical analysis; dogfood copied local data.
3. **release QA #1437**: run all host fixtures, migration-copy tests, filter compatibility tests, and sampled conflict review before v0.30.0.

For each behavior, the red test should assert the stored canonical/effective values or public result membership.
The smallest green implementation may reuse existing `workspace` columns.
Refactoring then introduces explicit vocabulary and removes host handlers that decide core relationship rules.

## Structure review checkpoint

The design is rejected if a hook handler owns canonical/effective precedence, if a use case switches behavior through multiple booleans, if SQLite DTOs leak into domain rules, or if tests pin private call order.
The design is also rejected if session canonical workspace changes as a side effect of event recording or if diagnostic classification can block the primary event before raw evidence is retained.

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
