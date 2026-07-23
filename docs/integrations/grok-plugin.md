# Grok Build plugin

[日本語](./grok-plugin.ja.md)

Traceary v0.23.0 adds a native Grok Build integration. The package under
[`integrations/grok-plugin/`](../../integrations/grok-plugin/) installs seven
verified lifecycle hooks, one local Traceary MCP server, and the three shared
memory/session skills. Recorded hook events use `client=hook` and `agent=grok`.

## Supported coverage

The lifecycle package targets the live-verified Grok Build 0.2.99 hook
contract. Usage capture additionally pins the Grok Build 0.2.106 headless
terminal contract.

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
metadata fails closed and creates no substitute observation. If `modelUsage`
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
the package, replaces an existing `traceary` plugin, and prints the installed
inventory.

```sh
git clone --branch v0.27.0 --depth 1 https://github.com/duck8823/traceary.git
cd traceary
./scripts/install-grok-plugin.sh
```

The installer runs `grok plugin install --trust`. Review the checked-out
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
`grok-hook-trust`, `grok-hooks`, `grok-mcp`, and `grok-skills`. The separate
`grok-event-coverage` check evaluates recent database evidence. With fewer
than three recent sessions it reports that coverage is not judged yet rather
than claiming a false pass.

## Project hook route and trust

The native plugin is the recommended route because it wires hooks, MCP, and
skills together. Traceary can also install hooks only:

```sh
# project route: <project>/.grok/hooks/traceary.json
traceary hooks install --client grok --project-dir .

# user route: ~/.grok/hooks/traceary.json
traceary hooks install --client grok --global
```

Grok treats project hooks as a separate trust boundary. When the project route
is intentional, inspect the file and use Grok's `/hooks-trust` flow in that
project. `grok-hook-trust` warns when a project hook file exists but the host
reports the project as untrusted. Do not copy plugin hooks into the project
route as a fallback; duplicate routes can record the same event twice.

## Update or remove

Traceary CLI and plugin versions are released together. After upgrading the
CLI, check out the matching tag and rerun the installer:

```sh
brew upgrade traceary
git fetch --tags
git checkout v0.23.0 # replace with the installed Traceary version
./scripts/install-grok-plugin.sh
traceary doctor --client grok --project-dir . --json
```

To remove only the native Grok package:

```sh
grok plugin uninstall traceary
```

Project/global hook-only files are independent of the plugin and must be
removed separately if they were installed.

## Troubleshooting

| Doctor check | Meaning and action |
| --- | --- |
| `grok-cli` fails | Install Grok Build and ensure `grok` is on `PATH` |
| `grok-plugin` warns | Install/reinstall the package; a version mismatch requires the package from the same Traceary release |
| `grok-hook-trust` warns | Review the project hook file and use `/hooks-trust`, or remove the unused project route |
| `grok-hooks` warns | The installed hook file is missing or has drifted from the exact seven-event contract; reinstall the plugin |
| `grok-mcp` / `grok-skills` warns | The installed package inventory is incomplete; reinstall it |
| `grok-event-coverage` warns | Inspect recent `agent=grok` events and pending hook/transcript queues; a healthy install alone does not prove runtime delivery |

Useful read-only checks:

```sh
grok plugin list --json
grok plugin details traceary
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
checks the plugin/MCP/skill inventory with `grok inspect`, then uninstalls it.

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
