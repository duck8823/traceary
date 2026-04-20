# Backup guide

[日本語](./README.ja.md)

Traceary's current backup / export / import story is intentionally simple:

- the supported export format is a compact SQLite backup file
- `traceary backup create` writes that file explicitly
- `traceary backup restore` restores it to a Traceary DB path and reapplies migrations if needed

There is no separate JSON / CSV export format yet.

## Create a backup

```sh
traceary backup create --output /tmp/traceary-backup.db
```

Useful flags:

- `--db-path` to back up a non-default DB
- `--force` to overwrite an existing backup file

`backup create` expects the source DB to exist already. If you have never recorded anything yet, create the DB intentionally first (for example with `traceary init` or your normal logging flow).

## Restore a backup

```sh
traceary backup restore --input /tmp/traceary-backup.db --force
```

Useful flags:

- `--db-path` to restore into a non-default destination
- `--force` to overwrite an existing destination DB
- `--yes` to skip the interactive confirmation prompt when you intentionally overwrite an existing destination DB from a TTY

Restore copies the backup file into the destination path, then runs the normal store initialization flow so newer migrations are applied automatically.
When you use `--force`, treat restore as a destructive replacement of the destination DB and take a fresh backup first if that data still matters.
On an interactive terminal, Traceary asks for confirmation before the overwrite unless you also pass `--yes`.

## Moving between machines

One practical flow is:

1. run `traceary backup create --output /path/to/traceary-backup.db` on the source machine
2. copy that SQLite file to the new machine with your normal file transfer method
3. run `traceary backup restore --input /path/to/traceary-backup.db --force` on the destination machine
4. point hooks / MCP clients at the restored DB path if you do not use the default location

## Operational notes

- the backup file is a SQLite database, not a line-oriented export format
- restores overwrite the destination only when you pass `--force`
- the destination DB path still follows the normal resolution order: `--db-path` → `TRACEARY_DB_PATH` → `~/.config/traceary/traceary.db`
- if you need off-machine storage, use your existing encrypted disk / backup tooling around the SQLite file
- **`traceary backup create` output is safe to copy as a single file** — it is produced via `VACUUM INTO`, so it has no WAL sidecar to worry about
- **copying a live DB directly requires the WAL sidecars too** — the runtime DB uses `journal_mode=WAL`, which generates `<db>-wal` and `<db>-shm` files next to the main DB. External file-level tooling that snapshots only the `.db` file can produce an inconsistent copy; prefer `traceary backup create` or include both sidecars in the copy

## Non-goals for this release

This release does **not** add:

- structured JSON / CSV export
- partial import of selected events
- cloud backup or sync
