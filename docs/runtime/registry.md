# Shared headless registry backend

[日本語](registry.ja.md)

The backend implements [R0](../adr/2026-11-03-headless-runtime-r0.md) for issue
#2472. It is shared application infrastructure, not a Desktop dependency or a
new public CLI command. Daemon wiring and operator commands follow in their own
implementation issues. No agent process, native hook, model request, core SQLite
migration, or host configuration is changed by registering metadata.

## Ownership and usage

- `domain/runtime.Registry` owns project/worktree membership, immutable identities,
  duplicate/admission rules, defensive snapshots and explicit deletion plans.
- `application/runtime.Registry` provides RegisterProject, RegisterWorktree, List,
  Export, Resolve, Revalidate, PlanDeleteProject/Worktree and ApplyDelete.
- `infrastructure/runtime.RegistryResolver` checks canonical Git worktree/common-dir,
  current owner, file type and non-group/world-writable modes. Git plumbing uses
  a bounded context and does not inherit `GIT_*` selection/config variables.
- `infrastructure/runtime.RegistryStore` persists version 1 `registry.json` under
  an explicitly supplied private root, independent of the core store.

Canonical registration resolves symlink aliases. A project is identified by its
registered root and Git common directory with physical owner/device/inode witness.
A worktree binds a canonical Git root in that project to an existing owned regular
store file. Paths outside a Git worktree root, foreign common directories, missing
or replaced identities and unsafe permissions are rejected. Store bytes are not
opened as a database or mutated; only their identity is inspected.

A Target is a worktree snapshot plus an explicit profile ID. Claude and Codex may
select the same registered worktree independently: there is no mutable global
profile selection. Profile reference syntax is checked here; the profile owner
must resolve and validate the registered executable/version before daemon launch.
Revalidate the saved target and expected profile immediately before use. Registry
metadata is not input/stop authorization and does not close the spawn/control
filesystem race by itself; the runtime owner must preserve its operation boundary.
Deleting a record cannot mutate an existing target value or a running process.

## Persistence and failure behavior

The metadata root is opened component-by-component without following symlinks.
Trusted ancestors must be owned by root/current UID and not group/world-writable
(except root-owned sticky temporary directories). The final root must be owned by
the current UID with mode 0700. Existing unsafe permissions are not repaired.
Root physical identity is pinned; replacing its path invalidates the instance.
Metadata and lock files must be single-link regular files, current-UID-owned and
mode 0600. Symlink/FIFO/device paths are rejected rather than followed.

A fixed-size process-local lock stripe and an OS file lock serialize independent
instances and processes. Lock waits observe cancellation. Failed or canceled
mutations before rename preserve the prior envelope. Writes use an exclusive
random temporary file, file sync, same-directory atomic rename and directory
sync. A post-rename directory-sync failure or lost pinned root identity returns `ErrCommitUncertain`: reload
and reconcile rather than assuming nothing changed. No silent retry is performed.

Strict decoding rejects unknown fields, duplicate object keys, noncanonical field case, invalid UTF-8,
missing/null required fields, trailing JSON, forged identity, invalid membership, oversized data and unsupported
versions without overwriting them. Limits are 100 projects, 1,000 worktrees and
at most 128 MiB of registry metadata. A constructor budget may tighten, never
raise that ceiling. When additional runtime envelopes are introduced, the daemon
composition must enforce the R0 aggregate metadata budget as well. This backend
currently writes only the registry envelope and a zero-length lock file.

Export returns a defensive application snapshot; transport consumers map accessors
to their own typed DTOs. Deletion requires a preview tied to the exact revision
and registry content. Abandoning/canceling a plan changes nothing; changing the
registry invalidates the plan. A project with registered worktrees cannot be
removed. ApplyDelete removes only metadata, never Git worktrees/branches, the
store file, terminal output or host history. Running-resource deletion policy
belongs to the later runtime owner, not an implicit registry cleanup job.

## Validation and limits

Public-API tests cover canonical aliases, linked/foreign repositories, separate
profile selections, stale targets/store replacement, both entry limits, snapshot
isolation, explicit preview/cancel/delete, corrupt/newer metadata, symlinks and
permissions, budget preservation, cancellation and concurrent independent
instances/processes. Fixtures use only disposable local repositories/store files.

This is not native host/resume certification or a claim of protection from a
malicious same-UID process. Cross-UID/ACL and full IPC/process control integration
remain separate runtime validation concerns. No UI/PTY/native model run is needed
for this backend. Rollback disables registry mutations and retains/export its
metadata; it never force-cleans user resources.
