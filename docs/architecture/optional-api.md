# Optional[T] API migration policy

[日本語](./optional-api.ja.md)

Traceary used a local `Optional[T]` API that predated the Go conventions documented in `duck8823/dotfiles/conventions/go/type-system.md`.
This guide records the gap, the chosen target API, and the rollout that moved the repository onto the convention surface.

## Current state in Traceary

`domain/types.Optional[T]` exposes the convention entrypoints:

- `types.Some(...)`
- `types.None[T]()`
- `Value()`
- `OrElse(...)`

The legacy compatibility aliases (`Of`, `Empty`, `IsPresent`, `Get`) **were removed in v0.10**. The repository is now fully on the convention surface.

## Convention target

The Go conventions tracked for this repository prefer:

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

Traceary is fully aligned with that convention.

## Migration history

### Phase 1: compatibility surface

Added the convention entrypoints (`Some`, `None`, `Value`) alongside the legacy ones, without changing semantics.

### Phase 2: repository call-site migration

The repository call sites were migrated in focused batches:

- `domain/` and `application/`
- `infrastructure/`
- `presentation/`
- tests and helpers

### Phase 3: legacy-name removal (v0.10)

`Of`, `Empty`, `IsPresent`, and `Get` were removed. Pre-1.0 cleanup; no compatibility shim retained.

## Rules for new code

```go
opt := types.Some(value)
none := types.None[string]()
value, ok := opt.Value()
```

- use `Some`, `None`, and `Value` in all new code
- do **not** reintroduce the legacy names

## Non-goals

This policy does **not** mean:

- changing Optional semantics
- replacing `Optional[T]` with pointers everywhere

## Related docs

- [Architecture principles](./README.md)
- [Documentation index](../README.md)
