# Report aggregation contract

[日本語](./report-aggregate-contract.ja.md)

## Structure-Behavior Design Note

### Requirement summary

- `report` must never present a capped prefix as a complete period aggregate.
- Internal database page size and caller-requested result cap are different concepts.
- CLI and MCP must return the same aggregate schema for the same interval, filters, page size, and cap.
- Report scans must use body-free rows and expose the observed range and truncation provenance.
- Finalized usage must select only the current snapshot head, keep excluded evidence visible without summing it, and separate estimated from provider-reported cost.
- Full aggregation remains the default. A positive result cap is an explicit sampling request.

### Conceptual model

| Concept | State | Behavior | Invariant |
|---|---|---|---|
| Report criteria | interval, workspace, client, page size, result cap | validates and resolves one shared request | page size is positive; cap is zero (unlimited) or positive |
| Source window | body-free session, event, command, and usage rows | loads all rows or at most cap plus one sentinel row | every source uses the same effective interval and filters |
| Result extent | coverage, observed count/range, page size, cap, truncation reason | distinguishes complete from partial | `partial` requires `result_cap` truncation evidence |
| Report snapshot | aggregates plus source extents | omits percentages whose denominator is partial | CLI and MCP serialize the same read model |
| Usage aggregate | current finalized observation and immutable run facts | groups tokens/cost and deduplicates bytes per run | excluded evidence is not summed; missing values are not zero |

### Responsibility assignment

| Responsibility | Owner | Not owner |
|---|---|---|
| Request validation and default seven-day interval | application report criteria | CLI/MCP adapters |
| One-read-snapshot, body-free, capped scans and current usage snapshot selection | SQLite report query adapter | use case and presentation |
| Aggregation, usage accounting, run deduplication, and complete-denominator rules | report use case | SQL and output writers |
| Flags/tool input, localization, text rendering | CLI/MCP presentation | application core |

### Boundaries and interfaces

| Boundary | Consumer | Hidden detail | Error contract |
|---|---|---|---|
| `ReportQueryService.LoadReportWindow` | report use case | SQL, paging, sentinel row, transaction | rejects invalid criteria and wraps read failures |
| `ReportUsecase.Generate` | CLI and MCP | source aggregation and percentage eligibility | returns one shared report snapshot |
| CLI `report` | operators/scripts | legacy `--limit` compatibility alias | conflicts between `--limit` and `--page-size` are rejected |
| MCP `get_report` | agent hosts | application/infrastructure types | uses `page_size` and `result_cap` only |

### Behavior tests

| Given | When | Then | Level |
|---|---|---|---|
| more rows than `result_cap` | generate report | coverage is `partial`, observed range is present, percentages are absent | use case/integration |
| no result cap | generate report | coverage is `complete` and percentages are present | use case/integration |
| identical CLI and MCP inputs | serialize report | decoded JSON values are equal | presentation integration |
| large stored bodies | aggregate report | SQL projection does not select body columns | datasource/integration |
| superseded snapshot and excluded alternative | aggregate usage | only the current head is read and excluded evidence contributes no tokens | datasource/use case |
| repeated observations for one run | aggregate run facts | packet/tool bytes contribute once | use case |
| estimated and provider-reported cost | aggregate usage | origins remain separate | use case |
| token, cost, or run-byte sum exceeds `int64` | aggregate usage | generation fails instead of wrapping | use case |
| `--limit` and `--page-size` together | run CLI | command fails before querying | CLI |

### TDD plan

1. Add failing application tests for complete/partial extents and omitted rates.
2. Add failing SQLite tests for cap detection, observed range, and body-free scanning.
3. Add failing CLI/MCP schema parity and flag-contract tests.
4. Implement criteria and extent value objects, then the report query adapter and use case.
5. Replace CLI-local aggregation and add the MCP tool without changing complete-report compatibility fields.

### Risks and rollback

- Procedural risk: duplicating interval, cap, or rate decisions in both adapters. Mitigation: one application criteria and snapshot type.
- Premature abstraction risk: a generic reporting framework would exceed the issue. Only the report window and extent are shared.
- Compatibility: `period.from` / `period.to` and complete-report numeric fields remain unchanged. Legacy `--limit` remains a deprecated hidden alias for page size.
- Rollback trigger: CLI/MCP schema drift, percentages on partial data, integer wraparound, or body hydration during aggregation. The change is additive and has no migration.
