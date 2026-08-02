# Safe store compaction

Garbage collection and archive deletion reclaim logical rows only. They no
longer run an in-place `VACUUM`. To reclaim filesystem space, stop all Traceary
processes (including older versions that do not participate in the lease) and
use the explicit workflow:

```sh
traceary store compact plan --db-path /path/to/traceary.db
traceary store compact apply RUN_ID --db-path /path/to/traceary.db
traceary store compact status RUN_ID --db-path /path/to/traceary.db
```

`plan` is non-destructive. `apply` uses `VACUUM INTO` beside the source,
fsyncs and verifies both SQLite files through the same compatibility policy,
then performs an atomic same-filesystem exchange. The original inode is kept as
a rollback artifact. A source with `-wal`, `-shm`, or `-journal` sidecars, an
active search-maintenance transition, insufficient free space, changed file
identity, or an unsupported atomic-exchange platform fails closed.

After interruption, use `resume`; it derives file orientation from identities.
Use `rollback` to atomically restore the retained original. Never delete the
external `.traceary-compaction` journal or rollback artifact until the run has
been inspected and accepted.

