# Grok Build plugin

[日本語](./grok-plugin.ja.md)

Traceary v0.23.0 adds a native Grok Build integration. The package under
[`integrations/grok-plugin/`](../../integrations/grok-plugin/) declares seven
lifecycle hooks (live-verified on 0.2.99/0.2.101; the current 1.0.5 host does
not dispatch plugin hooks — see the caveat below), one local Traceary CLI, and
the four shared skills (see [skills](./skills.md)). Recorded hook events use
`client=hook` and `agent=grok`.

## Supported coverage

The lifecycle package targets the live-verified Grok Build 0.2.99 hook
contract. Usage capture additionally pins the Grok Build 0.2.106 headless
terminal contract.

> **Grok Build 1.0.5 recording route:** on Grok Build 1.0.5 the host
> discovers and enables the plugin but does **not** dispatch plugin-provided
> hooks (`inspect --json` lists them as `hookType=file`, event `(plugin)`).
> The host **does** execute `hookType=command` files from `~/.grok/hooks/`.
> Install that recording route with `traceary hooks install --client grok
> --global` (the plugin installer now does this). Do not delete
> `~/.grok/hooks/traceary.json` while inspect still shows the plugin as a
> listing. See [host coverage](../hooks/host-coverage.md).

| Grok event | Traceary behavior |
| --- | --- |
| `SessionStart` | Starts or refreshes the native Grok session |
| `UserPromptSubmit` | Records the user prompt |
| `PreToolUse` | Validates the tool payload without writing a completed audit |
| `PostToolUse` | Records one completed command audit, including observed missing-file and denial result variants |
| `Stop` | Records a best-effort transcript from `updates.jsonl` and a turn boundary; it does not end the session |
| `PreCompact` / `PostCompact` | Records phase-specific compact markers; Grok exposes no summary body |

## Usage availability

Grok's native hooks do not expose provider usage. Traceary therefore records
an excluded `unavailable` call observation at a `Stop` boundary only when the
verified `promptId` supplies a stable identity. It does not estimate tokens
from the transcript, compact markers, retries, subagents, or response text.
A Stop without a stable identity creates no synthetic usage row.

For a bounded headless run, use Grok's verified terminal stream through the
Traceary-owned one-shot lifecycle:

```sh
traceary session run -- \
  grok --no-auto-update -p "your prompt" --output-format streaming-json
```

Traceary forwards stdout byte-for-byte and reads only the terminal `end`
metadata. One `end.usage` is stored per `requestId`/`sessionId`, including
input, cache-read input, output, reasoning, and total tokens. Incremental
thought/text events, cost fields, error bodies, and transcript content are
discarded. A missing terminal usage object becomes one excluded
`unavailable` run observation rather than zero while retaining the same
portable provider identity. Malformed, conflicting, or oversized terminal
metadata fails closed and creates no substitute observation. If a supervised
`streaming-json` run cannot be decoded, Traceary still writes one idempotent,
excluded run observation with unavailable counters using the supervised
delivery identity. The decode diagnostic is reported, but it never replaces
the child process exit status. If `modelUsage`
names exactly one model, that model is retained; multi-model aggregate usage remains
model-unattributed and is never split or counted twice.
The provider `requestId`/`sessionId` pair is normalized to a bounded portable
identity, so replaying the same terminal result under another Traceary wrapper
session remains idempotent; changed counters for that identity fail closed.

Retry and subagent activity is included only to the extent that Grok's
terminal aggregate includes it. Traceary does not add incremental events or
infer cardinality. Compact hooks remain lifecycle-only because their counts,
when present, are context-compression measurements rather than provider usage.
No TUI usage path is claimed.

`SessionEnd`, standalone failure hooks, and subagent parent/child correlation
are not claimed in v0.23.0 because their payloads were not live-verified.
Traceary does not synthesize unavailable lifecycle relationships. See the
[host coverage matrix](../hooks/host-coverage.md) and the machine-readable
[Grok contract](../hooks/host-contract.json) for the field-level status.

## Install

1. Install the Traceary CLI and confirm that `traceary` is on `PATH`.

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

### A. Public marketplace (preferred when listed)

When Traceary is present in the [xAI Plugin Marketplace](https://github.com/xai-org/plugin-marketplace)
catalog, install from Grok Build without cloning this repository:

```sh
# Browse / install via Grok's plugin UI, or the host's marketplace install path.
# After install, confirm inventory and version parity:
traceary doctor --client grok --project-dir . --json
```

Catalog contribution metadata lives in-repo:

- Template: [`integrations/grok-plugin/marketplace-entry.json`](../../integrations/grok-plugin/marketplace-entry.json)
- Pin current commit: `./scripts/generate-grok-marketplace-entry.sh [git-ref]`
- Submission steps: [Grok marketplace submission](./grok-marketplace-submission.md)

Remote source shape (SHA must be a full 40-char commit of this repository; package path is `integrations/grok-plugin`).

### B. Local-source install (deterministic fallback)

Always available from a matching Traceary release tag. The installer validates
the package, replaces only an existing `traceary-grok` package, and prints the
installed inventory. It intentionally leaves a legacy package named `traceary`
untouched because that name can belong to another host integration.

```sh
git clone --depth 1 --branch "v$(traceary -v | awk '{print $2}')" https://github.com/duck8823/traceary.git
cd traceary
./scripts/install-grok-plugin.sh
```

The installer runs `grok plugin install --trust` with the repository
`#integrations/grok-plugin` selector. Review the checked-out
package before running it because trusted command hooks execute locally. The
current package invokes only the documented Traceary hook entrypoints and does
not read or transmit Grok credentials or browser state.

### Clean-home verification

```sh
./scripts/verify-grok-plugin-clean-home.sh
```

Uses a temporary `HOME`, runs validate → install → details → reinstall → uninstall,
and never touches operator credentials or browser state.

### Doctor

```sh
traceary doctor --client grok --project-dir . --json
```

A healthy installation reports `pass` for `grok-cli`, `grok-plugin`,
`grok-hook-trust`, and `grok-skills`. On 1.0.5 `grok-hooks` stays WARN
(plugin listing is not dispatch) and `grok-hooks-user` must be `pass` — that
user-level file is the recording route. The separate `grok-event-coverage`
check evaluates recent database evidence. With fewer than three recent
sessions it reports that coverage is not judged yet rather than claiming a
false pass.

## Project and user hook routes

On Grok Build 1.0.5 the **user-level command file** is the recording route.
The native plugin still ships skills and the declared hook file for hosts
that dispatch plugin hooks. Keep both until inspect shows plugin-source
`hookType=command` entries for `traceary-grok`.

```sh
# recording route on 1.0.5: ~/.grok/hooks/traceary.json
traceary hooks install --client grok --global

# project route: <project>/.grok/hooks/traceary.json
traceary hooks install --client grok --project-dir .
```

`scripts/install-grok-plugin.sh` installs the plugin **and** the user-level
recording route. It does not write `cmux-session.json`.

Keep **exactly one executed** Traceary route:

- A listed plugin (`hookType=file`, event `(plugin)`) is not an executed route.
- On 1.0.5, keep `~/.grok/hooks/traceary.json`. Deleting it after installing
  the plugin is what produced zero live capture in v0.47.0 dogfood.
- Remove the user file only when `grok inspect --json` dispatches
  plugin-source **command** hooks for `traceary-grok`. Two executed Traceary
  routes can record the same event twice.

`traceary doctor --client grok` reports:

- `grok-hooks` — native plugin hook coverage
- `grok-hooks-user` — user-level file at `~/.grok/hooks/traceary.json`.
  `pass` when present; `warn` when absent and no dispatched plugin-source or
  project route exists (1.0.5 recording gap); `skip` only when another
  executed Traceary route is already active
- `grok-hooks-routes` — warns when more than one **executed** route is active
  (dispatched plugin-source, project, user). A listed plugin does not count.

Grok treats project hooks as a separate trust boundary. When the project route
is intentional, inspect the file and use Grok's `/hooks-trust` flow in that
project. `grok-hook-trust` warns when a project hook file exists but the host
reports the project as untrusted.

## Update or remove

Traceary CLI and plugin versions are released together. After upgrading the
CLI, check out the matching tag and rerun the installer:

```sh
brew upgrade traceary
git fetch --tags
git checkout "v$(traceary -v | awk '{print $2}')"
./scripts/install-grok-plugin.sh
traceary doctor --client grok --project-dir . --json
```

The native package is named `traceary-grok`, deliberately distinct from the
Claude package named `traceary`. The installer replaces only `traceary-grok`;
it never removes a legacy `traceary` package because Grok can resolve that
same-name package from another host. A converged native installation reports
seven hook boundaries, one CLI surface, and three skills.

#### Local-repository identity migration

Older local installs that omitted the `#integrations/grok-plugin` selector can
be shown by Grok as a repository identity such as `traceary`, rather than the
package name `traceary-grok`. `traceary doctor --client grok --json` reports
this as a body-free `grok-plugin-resolution` warning. It reads only inventory
names, repository keys, source paths, hook paths, and component counts; it
does not read plugin payloads, prompts, transcripts, or credentials.

The normal installer stops without removing anything. After reviewing
`grok plugin list --json`, run this bounded migration from the same checkout:

```sh
./scripts/install-grok-plugin.sh --migrate-local-repo-identity
```

It removes only an identity whose source is exactly that checkout's
`integrations/grok-plugin` directory, then installs the canonical package from
the repository subdirectory selector. It never selects or removes a `traceary`
package from another source. Re-run doctor and confirm that `grok-plugin`,
`grok-plugin-resolution`, `grok-hooks`, and `grok-skills` pass.

To remove only the native Grok package:

```sh
grok plugin uninstall traceary-grok
```

Project/global hook-only files are independent of the plugin and must be
removed separately if they were installed.

## Troubleshooting

| Doctor check | Meaning and action |
| --- | --- |
| `grok-cli` fails | Install Grok Build and ensure `grok` is on `PATH` |
| `grok-plugin` warns | Install/reinstall the package; a version mismatch requires the package from the same Traceary release |
| `grok-plugin` fails | `grok plugin list --json` reports a `source` that is a local path which no longer exists on disk; the cached version does not prove the plugin can load. Reinstall with `scripts/install-grok-plugin.sh` (remote git URL sources are never treated as missing) |
| `grok-plugin-resolution` warns | Grok resolved a non-native path class, a same-name legacy package, or a local-repository identity. For a local-repository identity, review `grok plugin list --json` and run `scripts/install-grok-plugin.sh --migrate-local-repo-identity` from that checkout; otherwise run the normal installer and confirm `traceary-grok` is the enabled route. Doctor reads only inventory metadata. |
| `grok-hook-trust` warns | Review the project hook file and use `/hooks-trust`, or remove the unused project route |
| `grok-hooks` warns | The plugin is listing-only on this host, or the hook file drifted from the seven-event contract. On 1.0.5 the recording route is `traceary hooks install --client grok --global`. |
| `grok-hooks-user` warns | No executed Traceary route. On 1.0.5 run `traceary hooks install --client grok --global`. A listed plugin is not execution. |
| `grok-hooks-user` fails | `~/.grok/hooks/traceary.json` exists but is unreadable or not valid JSON; fix or remove it |
| `grok-hooks-routes` warns | More than one **executed** route is active (dispatched plugin-source command hooks plus user or project). Retain exactly one executed route. Do not delete the user file while the plugin is listing-only. |
| `grok-skills` warns | The installed package inventory is incomplete; reinstall it |
| `grok-event-coverage` warns | Inspect recent `agent=grok` events and pending hook/transcript queues; a healthy install alone does not prove runtime delivery |

### Stop final-turn transcript disposition

Grok can emit `Stop` while its `updates.jsonl` file is still receiving the
final assistant message. Traceary records a ready final message once. Otherwise
it starts one bounded transcript worker (20 checks at 100ms). A missing path,
malformed wire, worker cancellation, or a final message that remains absent is
recorded as a body-free **partial** final-turn disposition and has no pending
retry job. `traceary doctor --client grok --json` reports aggregate disposition
counts only; it never exposes the transcript path, session ID, prompt ID, or
assistant body. A `recorded` disposition by itself is a healthy idempotency
receipt and leaves doctor passing. Pending work, partial dispositions, or
unreadable queue state warn. Re-delivering the same Stop after any terminal
disposition (`recorded`, `unavailable`, `malformed`, or `cancelled`) creates no
new job or worker. Start a new marker turn with `TRACEARY_HOOK_DEBUG=1` when a
warning remains rather than copying or editing queue files.

Useful read-only checks:

```sh
grok plugin list --json
grok plugin details traceary-grok
grok --cwd . inspect --json
traceary list --agent grok --limit 20
traceary doctor --client grok --project-dir . --json
```

`Stop` transcript capture is deliberately asynchronous when Grok has not yet
appended the final message. A retained job is reported by doctor and never
causes the host hook to block indefinitely. Raw prompts and transcripts are
not included in doctor output.

Follow-up work is tracked separately for the
[subagent parent/child contract](https://github.com/duck8823/traceary/issues/1299),
[unobserved lifecycle hooks](https://github.com/duck8823/traceary/issues/1300),
and [public marketplace publication](https://github.com/duck8823/traceary/issues/1301).

## Package validation

Maintainers can validate the repository package and its isolated install
surface without using a real project:

```sh
go run ./cmd/repo-tooling integrations verify
./scripts/smoke_test_integrations.sh grok
```

The smoke test uses a temporary home, validates and installs the package,
checks the plugin/hook/skill inventory with `grok inspect`, then uninstalls it.

## v0.23.0 dogfood result

Verified 2026-07-14 against Grok Build 0.2.99:

- a sanitized live core run recorded one native `agent=grok` session with
  `session_started`, `prompt`, `command_executed`, and `transcript`; the
  transcript retry queue and hook spool were empty after completion
- nine sanitized fixtures cover the five core routes, missing/denied
  `PostToolUse` result variants, and compact pre/post markers
- an isolated temporary-home install, inspect, doctor, and uninstall passed;
  all seven `grok-*` checks reported `pass`
- no raw prompt, transcript, credential, private hook target path, or temporary
  workspace path is committed as dogfood evidence
- the subagent probe was not run because the external-agent policy gate denied
  it; subagent correlation remains unavailable rather than simulated

The minimized execution record is attached to
[Issue #1279](https://github.com/duck8823/traceary/issues/1279#issuecomment-4961391647).

## Official references

- Grok Build hooks: https://docs.x.ai/build/features/hooks
- Grok Build skills, plugins, and marketplaces: https://docs.x.ai/build/features/skills-plugins-marketplaces
