# Optional[T] API migration policy

[日本語](./optional-api.ja.md)

Traceary currently uses a local `Optional[T]` API that predates the Go conventions documented in `duck8823/dotfiles/conventions/go/type-system.md`.
This guide records the gap, the chosen target API, and the rollout policy for closing it without mixing a repo-wide refactor into unrelated features.

## Current state in Traceary

`domain/types.Optional[T]` currently exposes:

- `types.Of(...)`
- `types.Empty[T]()`
- `IsPresent()`
- `Get()`
- `OrElse(...)`

That API is used broadly across domain, application, infrastructure, presentation, and tests.
At the time this policy was written, the repository had roughly a few hundred references to the legacy names, so a direct big-bang rename would create a noisy and risky PR.

## Convention target

The Go conventions tracked for this repository prefer:

- `types.Some(...)`
- `types.None[T]()`
- `Value()`

The target is to align Traceary with that convention.
This document chooses migration, not a permanent local exception.

## Policy decision

Traceary will migrate to the convention API in stages.

### Desired end state

New code should read naturally as:

```go
opt := types.Some(value)
none := types.None[string]()
value, ok := opt.Value()
```

### Transitional rule

To avoid blocking unrelated work, the migration should use an explicit compatibility window:

1. add the convention entrypoints (`Some`, `None`, `Value`) alongside the legacy ones
2. migrate call sites incrementally in reviewable slices
3. remove the legacy names only after repository call sites have been converted, or explicitly re-scope that cleanup if the repository decides to keep the aliases longer

## Rollout order

### Phase 1: compatibility surface

Add:

- `Some`
- `None`
- `Value`

without changing semantics.
During this phase, do **not** change every call site in one PR.

### Phase 2: repository call-site migration

Migrate the repository in focused batches, for example:

- `domain/` and `application/`
- `infrastructure/`
- `presentation/`
- tests and helpers

The exact batching can be adjusted, but each PR should stay reviewable.

### Phase 3: legacy-name decision

After the repository is migrated, make an explicit decision:

- remove `Of`, `Empty`, `IsPresent`, and `Get`
- or keep them as compatibility aliases with clear local documentation

That final step must be intentional. The repository should not drift into a half-migrated state with no documented policy.

## Rules for new code during the migration

Until the migration is finished:

- prefer `Some`, `None`, and `Value` in new code once they exist
- avoid introducing new uses of `Of`, `Empty`, `IsPresent`, and `Get` unless the touched file has not yet been migrated and consistency in that small patch is more important
- do not mix Optional API cleanup into unrelated bug fixes unless the issue explicitly includes that scope

## Non-goals

This policy does **not** mean:

- changing Optional semantics
- replacing `Optional[T]` with pointers everywhere
- blocking runtime or feature work on a single repo-wide rename PR

## Related docs

- [Architecture principles](./README.md)
- [Documentation index](../README.md)
