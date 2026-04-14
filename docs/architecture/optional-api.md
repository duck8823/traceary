# Optional[T] API migration policy

[日本語](./optional-api.ja.md)

Traceary used a local `Optional[T]` API that predated the Go conventions documented in `duck8823/dotfiles/conventions/go/type-system.md`.
This guide records the gap, the chosen target API, and the rollout policy that moved the repository onto the convention surface without mixing the refactor into unrelated feature work.

## Current state in Traceary

`domain/types.Optional[T]` now exposes the convention entrypoints:

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

It also still carries compatibility aliases for the legacy surface:

- `types.Of(...)`
- `types.Empty[T]()`
- `IsPresent()`
- `Get()`
- `OrElse(...)`

The repository code now uses the convention entrypoints.
The legacy names remain only as compatibility aliases so downstream callers are not broken immediately.

## Convention target

The Go conventions tracked for this repository prefer:

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

The target is to align Traceary with that convention.
This document chooses migration, not a permanent local exception.

## Policy decision

Traceary has migrated the repository to the convention API in stages.

### Desired end state

New code should read naturally as:

```go
opt := types.Some(value)
none := types.None[string]()
value, ok := opt.Value()
```

### Transitional rule

To avoid blocking unrelated work, the migration used an explicit compatibility window:

1. add the convention entrypoints (`Some`, `None`, `Value`) alongside the legacy ones
2. migrate repository call sites incrementally in reviewable slices
3. keep the legacy names as documented compatibility aliases until the repository decides it is safe to remove them

## Rollout order

### Phase 1: compatibility surface

Added:

- `Some`
- `None`
- `Value`

without changing semantics.

### Phase 2: repository call-site migration

The repository call sites were migrated in focused batches rather than with a single big-bang rename.

- `domain/` and `application/`
- `infrastructure/`
- `presentation/`
- tests and helpers

The exact batching can still be adjusted in follow-up cleanup work, but the convention surface is now the default for repository code.

### Phase 3: legacy-name decision

After the repository migration, the current decision is:

- keep `Of`, `Empty`, `IsPresent`, and `Get` as compatibility aliases for now
- revisit removal in a dedicated cleanup issue instead of coupling it to unrelated feature work

This final step remains intentional. The repository should not drift into a half-migrated state with no documented policy.

## Rules for new code during the migration

Now that the repository has moved to the convention API:

- prefer `Some`, `None`, and `Value` in all new code
- do not introduce new uses of `Of`, `Empty`, `IsPresent`, or `Get`
- treat the legacy names as compatibility shims, not as acceptable style for new call sites

## Non-goals

This policy does **not** mean:

- changing Optional semantics
- replacing `Optional[T]` with pointers everywhere
- blocking runtime or feature work on a single repo-wide rename PR

## Related docs

- [Architecture principles](./README.md)
- [Documentation index](../README.md)
