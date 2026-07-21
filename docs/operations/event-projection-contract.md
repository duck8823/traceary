# Event projection and truncation contract

[日本語](./event-projection-contract.ja.md)

**Status:** accepted design for v0.30.0  
**Issue:** #1439  
**Parent epic:** #1420

## Requirement summary

Traceary read surfaces currently restore full `model.Event` aggregates before
CLI or MCP response code applies a body limit. CLI JSON also serializes the
full event shape even when `--fields` selected only metadata for compact text.
Consequently, a request intended to inspect metadata can materialize and
re-emit large prompt, transcript, tool, or command bodies.

v0.30.0 adds an explicit metadata-only read contract without changing the
meaning of existing full-body controls:

- omitted MCP projection keeps the existing bounded-body default;
- `body_limit=0` and `full_body=true` continue to return the full **stored**
  body;
- metadata-only reads neither select body columns nor construct a full event
  aggregate;
- explicit detail reads such as `traceary show <event-id>` remain full-body
  paths;
- ingestion, storage-policy, and response truncation remain distinguishable.

Retention and deletion are outside this contract and remain in #1421.

## Conceptual model

| Concept | State | Behavior | Invariant |
|---|---|---|---|
| `EventMetadata` | identity, kind, attribution, source hook, timestamp, persisted size/truncation facts | supports list, context, and aggregate consumers without body hydration | never contains body text or body blocks |
| `StoredEvent` | metadata plus the stored body/body blocks | restores the domain event for consumers that need content | stored content may already be shorter than the original payload |
| `EventProjection` | `metadata`, `bounded`, or `full` | selects the read model and response shape | projection is decided before repository/query execution |
| `BodyExtent` | original, stored, and returned byte counts; optional rune counts | explains which layer removed content | unknown is distinct from zero |
| `TruncationProvenance` | ingestion, storage policy, response | identifies every layer that truncated content | response truncation never claims that omitted bytes are recoverable when storage already removed them |
| `ProjectionSnapshot` | filters plus a stable read snapshot | keeps paged reads and aggregates consistent | the same snapshot/filter has the same membership regardless of projection |

### Projection behavior

| Projection | SQLite result columns | Application value | Response body | Existing compatibility |
|---|---|---|---|---|
| `metadata` | metadata and persisted size/truncation columns only | `EventMetadata` | absent | new in v0.30.0 |
| `bounded` | stored event body plus metadata | full event/read row | present up to a positive response limit | current MCP default remains 500 runes |
| `full` | stored event body plus metadata | full event/read row | full stored body | current `body_limit=0`, `full_body=true`, and detail behavior |

`full` means full **stored** content. It cannot recover payload bytes removed at
ingestion or by a future retention policy.

## Persisted body facts

Metadata-only queries cannot calculate body size in Go without reading the
body. The implementation therefore adds additive persisted facts at the SQLite
boundary:

- `body_original_bytes`: nullable; original payload size when the ingest path
  knows it;
- `body_stored_bytes`: non-negative stored byte count for new/updated rows;
- `body_ingest_truncated`: whether ingestion removed content;
- `body_storage_truncated`: reserved for an explicit storage/retention rewrite;
- `projection_version`: version of tool-aware metadata extracted at ingest.

Historical rows backfill `body_stored_bytes` inside SQLite. Their original size
and ingest provenance stay unknown unless existing canonical audit metadata can
prove them. A migration must not invent `0` or `false` for unknown historical
facts.

Response code derives `body_returned_bytes` and
`body_response_truncated`; those are not persisted because they depend on each
request.

## Responsibility assignment

| Responsibility | Owner | Reason to change | Not owner |
|---|---|---|---|
| Projection vocabulary and validation | `application/types` | consumer-visible read semantics | Cobra/MCP handlers must not each invent modes |
| Metadata read interface | `application/queryservice` | consumer-oriented read model | domain repository must not grow a large optional-field interface |
| Column selection and row scanning | `infrastructure/sqlite` | database/schema detail | application must not contain SQL or table names |
| CLI flag and JSON field mapping | `presentation/cli` | CLI compatibility and serialization | query service must not know Cobra flags |
| MCP input and output mapping | `presentation/mcpserver` | MCP compatibility and schema | query service must not know MCP DTOs |
| Stored-body truncation facts | ingest/storage boundary | only that boundary knows what was persisted | response renderer cannot infer lost input |
| Response truncation facts | presentation serializer | request-specific limit | persistence must not store per-response state |

The existing `domain/model.Event` remains the content-bearing aggregate. A
metadata row is a read model, not a partially initialized domain event.

## Boundaries and interfaces

| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| `EventMetadataQueryService` | CLI/MCP lists, context packs, reports | SQL projection and schema | invalid limits/filters are typed validation errors; scan failures retain operation context |
| Full event query/repository | detail and content consumers | stored body/body blocks | not-found remains distinguishable from storage failure |
| Projection resolver | CLI/MCP adapter | legacy flag precedence | contradictory explicit options fail instead of silently returning a larger payload |
| Metadata serializer | JSON/MCP consumer | internal row/nullable representation | unknown facts are omitted/null, never encoded as known zero |

Interfaces must be consumer-oriented. The metadata interface exposes only the
operations needed by metadata consumers rather than a boolean `includeBody`
flag on every existing repository method.

### MCP compatibility

MCP inputs gain an additive `projection` enum:

- omitted: legacy behavior (`body_limit`/`full_body` resolver);
- `metadata`: no body or body-block fields;
- `bounded`: positive `body_limit`, defaulting to 500 when omitted;
- `full`: full stored body.

`projection=metadata` rejects `full_body=true` and positive `body_limit`.
Because the legacy integer input cannot distinguish omitted `body_limit` from
an explicit zero, zero is ignored when `projection=metadata`; outside explicit
projection mode it retains its full-body meaning.

### CLI compatibility

- Text output keeps its existing default fields and formatting.
- JSON `--fields` controls serialization in v0.30.0 instead of being ignored.
- A metadata-only preset/flag expands to a documented metadata field set.
- Selecting no body/message field routes to the metadata query.
- `--wide` and explicit full-detail surfaces keep their existing content
  contracts.

## Behavior tests

| Behavior | Given | When | Then | Level |
|---|---|---|---|---|
| Metadata does not hydrate body | two rows with identical metadata and radically different bodies | metadata list runs | returned values and allocated response size are body-size independent | SQLite integration |
| SQL omits body columns | a metadata projection | query is prepared/executed | result scanner has no body/body-block destination | SQLite integration |
| Membership is projection-independent | one fixed snapshot/filter | metadata and full lists run | event IDs and order match | query-service integration |
| Legacy MCP default is bounded | projection omitted | `list_events` runs | body is capped at 500 runes as before | MCP behavior |
| Legacy full body remains full stored body | `body_limit=0` or `full_body=true` | MCP list runs | stored body is returned without response truncation | MCP regression |
| Contradictory MCP options fail closed | `projection=metadata` and `full_body=true` | validation runs | request fails without querying | MCP behavior |
| JSON fields control serialization | CLI JSON selects metadata fields | list runs | message/body keys are absent | CLI behavior |
| Full detail remains explicit | an event has a large stored body | `traceary show` runs | full stored body is returned | CLI regression |
| Unknown is not zero | a historical row lacks original extent | metadata serializes | original size/provenance is null or absent | migration behavior |
| Self-inspection is bounded | 10,000 events contain large bodies | metadata-only list/context runs | output and Go-side allocations remain bounded by row metadata, not body size | end-to-end regression |

Tests assert observable output and query shape; they do not lock private helper
call order.

## TDD plan

| Slice | Red | Green | Refactor target |
|---|---|---|---|
| #1428 metadata query | failing SQLite tests prove body columns are not scanned and membership matches full reads | add metadata read model, schema facts, migration, and dedicated SELECT lists | share filter/snapshot construction without sharing scanners |
| #1433 CLI JSON | failing test shows `--fields` still emits `message` | map resolved fields to metadata query and serializer | one projection resolver for list/tail-compatible paths |
| #1433 MCP | failing compatibility and contradiction tests | add explicit projection mapping and metadata output | share application projection types, not presentation DTOs |
| #1433 scale regression | 10,000-row fixture grows with body size | route to metadata SQL and body-free serializer | keep benchmark/fixture deterministic and private-data free |

## Migration, compatibility, and rollback

- Schema changes are additive. Existing full-event queries remain available.
- New size/provenance columns are populated for new rows and conservatively
  backfilled for existing rows.
- The release does not delete or rewrite body content.
- Rollback to v0.29.x ignores additive columns and continues reading stored
  events.
- The new presentation mode can be disabled independently while retaining the
  additive schema if a CLI/MCP compatibility regression appears.

## Risks and review checkpoint

- **Accidental hydration:** a metadata presentation may still call the old full
  query. Tests must inspect query columns and body-size-independent behavior.
- **Primitive/boolean leakage:** do not add `includeBody bool` throughout the
  usecase/repository stack; use typed projections and dedicated read models.
- **Unknown-value corruption:** historical original sizes must not become zero.
- **Search misunderstanding:** metadata-only search may use body text in a
  SQLite predicate, but it must not return/materialize that body in Go.
- **Contract drift:** CLI and MCP may have different DTOs, but projection
  semantics and truncation vocabulary have one application owner.

Implementation of #1428 starts only after this note is reviewed and merged.

