# Architecture principles

[日本語](./README.ja.md)

This document records the software architecture rules that Traceary should keep as the repository evolves. It exists so future refactors — especially around hook runtime behavior — can be reviewed against explicit boundaries instead of convention by memory.

## Why this document exists

Traceary already follows a four-layer structure, but the repository still has a few places where runtime behavior and helper assets blur together. The most visible example is the hook packaging model: the repository still keeps compatibility shell wrappers under `scripts/hooks/` even though Traceary's primary runtime is Go.

That packaging model works today, but it should be treated as a transitional compatibility path, not as the architectural target. This document defines the target.

## The four layers

Traceary uses four named layers:

```text
presentation -> application -> domain <- infrastructure
```

| Layer | What belongs here | What does not belong here |
| --- | --- | --- |
| `presentation/` | CLI command wiring, MCP server handlers, hook-host payload parsing, operator-facing formatting, transport-specific validation | domain rules, persistence logic, long-lived business state |
| `application/` | write-side use cases, read-side query-service contracts, orchestration across domain objects, shared read models/DTOs | SQL, filesystem mutation details, transport-specific payload parsing |
| `domain/` | entities, value objects, repository contracts, invariants, business errors | Cobra, SQLite, JSON transport handling, shell integration details |
| `infrastructure/` | SQLite implementations, filesystem adapters, platform-specific file handling, external dependency adapters | business decisions that should live in domain/application, ad-hoc CLI flow control |

### Dependency direction

- `presentation` may depend on `application` and `domain/types`.
- `application` may depend on `domain`.
- `domain` must not depend on the other layers.
- `infrastructure` may depend on `application` contracts and `domain`, but the inner layers must not depend on `infrastructure`.

The goal is not to make every file abstract. The goal is to keep business rules and runtime entrypoints in predictable places.

## Where runtime behavior belongs

Runtime behavior should live in normal Go packages, not in ad-hoc helper scripts, unless there is an explicit and documented exception.

### Preferred runtime entrypoints

First-class runtime entrypoints belong in `presentation/`.

Examples:
- `traceary` CLI commands
- `traceary mcp-server`
- future `traceary hook ...` subcommands

These entrypoints may parse host-specific payloads or user input, then hand normalized data to application use cases.

### Hook-host adapters

Hook-host adapters are part of the presentation layer when they interpret host-specific runtime payloads.

That means:
- Claude / Codex / Gemini event names and payload shapes are presentation concerns
- stdin/env/JSON parsing for hook invocations is a presentation concern
- mapping those payloads into normalized application inputs is a presentation concern

What should *not* happen is embedding host-specific shell behavior as the primary home of Traceary's runtime logic.

### Outbound integration assets

Some integration assets are still generated or installed from outside `presentation/`, for example hook config files and compatibility shell wrappers. Those belong to infrastructure as delivery/install concerns, not as the canonical implementation of runtime behavior.

A useful rule of thumb:
- **"What should happen when the user invokes Traceary?"** -> `presentation` / `application`
- **"How do we install or materialize files so a host can invoke Traceary?"** -> `infrastructure`

## The role of `scripts/`

`scripts/` is for developer and release helpers.

Typical examples:
- verification helpers
- release-preparation utilities
- packaging/install convenience scripts
- CI-oriented maintenance helpers

`scripts/` is **not** the preferred long-lived home for production runtime logic.

If a script is required at runtime, treat it as a temporary compatibility asset and document:
- why it still exists
- what the intended Go entrypoint is or will be
- what issue tracks its removal or narrowing

## Current exception: packaged hook shell assets

Today the repository still ships compatibility shell wrappers from `scripts/hooks/` as packaged integration assets. That is an explicit temporary exception.

The intended direction is:
1. move hook runtime behavior into Go subcommands (`traceary hook ...`)
2. keep shell assets only as thin compatibility wrappers when still needed
3. eventually stop treating `scripts/hooks/` as the canonical source of packaged hook assets

Until that migration lands, reviews should treat `scripts/hooks/` as compatibility infrastructure, not as the place to introduce new business/runtime behavior by default.

## `internal/` policy

`internal/` is a visibility tool, not a substitute for architecture.

Traceary does **not** require an `internal/` tree by default.

Use `internal/` only when one of these is true:
- a package is implementation detail that would be actively harmful to import from sibling modules
- visibility restriction materially simplifies the public package surface
- the restriction is clearer than the existing layer/package naming

Do **not** add `internal/` just to make a design look cleaner on paper. If the layer boundaries are unclear, fix the boundaries first.

## Design checklist for new work

When adding or refactoring functionality, check these questions:

1. Is the primary runtime behavior implemented in Go packages rather than helper scripts?
2. Does transport/host-specific parsing stay in `presentation/`?
3. Does orchestration stay in `application/`?
4. Do domain rules remain free from SQLite/CLI/MCP details?
5. Is `infrastructure/` implementing contracts instead of inventing business behavior?
6. If a script still exists, is it clearly a helper or a temporary compatibility layer?
7. If `internal/` is proposed, is there a concrete visibility reason beyond taste?

If the answer to any of these is "no", document the exception explicitly before merging.

## Related docs

- [Documentation index](../README.md)
- [Optional API migration policy](./optional-api.md)
- [Memory blocks: evaluation and decision](./memory-blocks.md)
- [Host-native memory activation contract](./host-native-memory-activation.md)
- [Hook contract](../hooks/contract.md)
- [Event lifecycle](../lifecycle.md)
- [Native integrations guide](../integrations/README.md)
- [Durable memory guide](../memory/README.md)
