# JSON and snapshot contract tests

[日本語](./json-contract-tests.ja.md)

Traceary uses golden tests to protect public JSON, newline-delimited JSON (NDJSON), and structured-text CLI output from accidental changes. A golden fixture is the reviewed byte-for-byte contract for one surface output.

The contract surfaces are:

- **CLI `--json` outputs** — `presentation/cli/testdata/<command>/<case>.golden.json`. Every new CLI `--json` flag must ship with a matching golden fixture in the same change. The covered representative surfaces include `event list` / `event search` / `event show`, `session list` / `session start` / `session end` / `session latest` / `session latest --active`, `memory list` / `memory search` / `memory show` and the full `memory inbox` / `memory admin hygiene` family, `sessions --snapshot --json`, `bundle import --json`, `timeline --json`, and `doctor --json`.
- **CLI structured-text outputs** — `presentation/cli/testdata/session_handoff/*.golden`, `presentation/cli/testdata/top/*.golden`, etc. Some commands (notably `traceary session handoff`) intentionally do not expose `--json` because their structured-text shape is the documented contract that prompt-injection / resume tooling parses directly. These goldens guard the field labels (`SESSION_ID:`, `WORKING_STATE:`, `RECENT_COMMANDS:`, `RECENT_COMMAND_ITEMS:`, `MEMORIES:`) and ordering.
- **MCP tool registry snapshot** — removed with the MCP server in v0.35.0 (#1871).

Starting in v0.30.0, CLI `traceary report --json` serializes the application `ReportSnapshot` read model. The report contract includes per-source aggregation extents; partial sources omit ratio fields instead of emitting misleading prefix-derived values. Starting in v0.32.0, that model also contains usage and deduplicated run-fact aggregates with explicit availability and cost-origin fields. The former MCP `get_report` twin was removed in v0.35.0 (#1871); update CLI goldens when the model changes.

`traceary sessions --snapshot --json` has a snapshot-specific contract; the `latest_event_*` fields belong only to this sessions snapshot contract. Starting in v0.14.0 the snapshot is wrapped in an envelope object with `sessions`, `failures`, `recent_commands`, and `candidates` (`{ count, items }`) keys so the snapshot carries secondary panes; consumers that previously read a bare top-level array of session nodes must read `sessions` from the envelope. Starting in v0.15.0 the envelope also includes `stale_memories` (`{ count, items }`) for the stale-memory section; stale rows reuse durable-memory summary fields plus a `reason`. Starting in v0.16.0, stale active session inspection can be enabled with `sessions --allow-stale`; stale session nodes then include optional `is_stale`, `stale_after_seconds`, and `stale_age_seconds` fields while existing session fields remain unchanged. The former `traceary session tree --json` contract was removed in v0.35.0 (#1869).

Starting in v0.19.0, the text snapshot for `traceary sessions --snapshot` includes `name="..."` before the raw `workspace=` / `agent=` metadata. This is covered by the text golden fixture; machine consumers that need positional stability should use the unchanged JSON envelope.

Starting in v0.20.1, JSON and text snapshot writers treat a downstream broken pipe as a normal early-close outcome. Commands such as `traceary sessions --snapshot --json | head -c 1` should exit silently instead of printing a misleading Traceary error, while query and JSON-encoding failures remain loud.

Starting in v0.21.0, the snapshot's `reliability.memory` object gains an additive `candidate_hygiene` object with `stale_count`, `duplicate_count`, `fragment_like_count`, `extracted_hidden_count`, and `likely_actionable_count`. The field is additive — existing snapshot consumers are unaffected — and is JSON-only (the text snapshot renderer is intentionally unchanged). The counts are bounded by the same memory scan as `scan_limit_reached`; the flag counts may overlap, and `likely_actionable_count` is the complement (a candidate with no hygiene flag set).

If a CLI command has no fixture for one of its public outputs, add one before merging rather than relying on ad-hoc string assertions.

## Run golden tests

Run a specific contract test while developing:

```sh
go test ./presentation/cli -run TestEventShow_JSON_Golden
```

Run CLI contract tests when a change is ready:

```sh
go test ./presentation/cli
```

The helpers compare the actual output with the fixture using a byte-for-byte string diff. They do not apply time, ID, ordering, or whitespace transformers, so test data must be deterministic before it reaches the assertion.

## Update fixtures

When the intended contract changes, rewrite fixtures with the standard Go test flag:

```sh
# CLI golden (JSON, NDJSON, or structured text)
go test ./presentation/cli -run TestEventShow_JSON_Golden -update
go test ./presentation/cli -run TestSessionHandoff_TextGoldens -update
```

Then re-run without `-update` to prove the checked-in fixture is clean:

```sh
go test ./presentation/cli -run TestEventShow_JSON_Golden
```

Review the fixture diff before committing it. The generated bytes are the API contract that downstream scripts may rely on.

## When not to use `-update`

Do not use `-update` just to make a failing test pass. A golden diff may reveal an accidental breaking change, such as:

- a renamed or removed CLI JSON field
- a changed timestamp format or reordered NDJSON stream
- a new whitespace shape in a structured-text contract (e.g. handoff)

Use `-update` only after deciding that the output change is intentional and should become the new public contract.

## Contract review process

1. Identify the surface (CLI command + flags or structured-text command) and the fixture affected by the diff.
2. Decide whether the output change is compatible. Add migration notes or release notes for breaking or user-visible CLI shape changes.
3. Update the fixture with `-update` only after that decision.
4. Re-run the focused golden test without `-update`.
5. Re-run the normal validation suite before committing:

```sh
go build ./...
go test ./...
go tool golangci-lint run
```

Pull requests that update golden fixtures should explain why the contract changed and call out any downstream impact.
