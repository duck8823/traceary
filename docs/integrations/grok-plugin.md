# Grok Build plugin

[日本語](./grok-plugin.ja.md)

Traceary v0.23.0 adds a native Grok Build integration. The package under
[`integrations/grok-plugin/`](../../integrations/grok-plugin/) installs seven
verified lifecycle hooks, one local Traceary MCP server, and the three shared
memory/session skills. Recorded hook events use `client=hook` and `agent=grok`.

## Supported coverage

The package targets the live-verified Grok Build 0.2.99 contract.

| Grok event | Traceary behavior |
| --- | --- |
| `SessionStart` | Starts or refreshes the native Grok session |
| `UserPromptSubmit` | Records the user prompt |
| `PreToolUse` | Validates the tool payload without writing a completed audit |
| `PostToolUse` | Records one completed command audit, including observed missing-file and denial result variants |
| `Stop` | Records a best-effort transcript from `updates.jsonl` and a turn boundary; it does not end the session |
| `PreCompact` / `PostCompact` | Records phase-specific compact markers; Grok exposes no summary body |

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

2. Check out the same Traceary release as the installed CLI, then install the
   native package. The installer validates the package, replaces an existing
   `traceary` plugin, and prints the installed inventory.

```sh
git clone --branch v0.23.0 --depth 1 https://github.com/duck8823/traceary.git
cd traceary
./scripts/install-grok-plugin.sh
```

The installer runs `grok plugin install --trust`. Review the checked-out
package before running it: trust allows the seven packaged command hooks to
invoke the local `traceary` executable. It does not grant Traceary access to
Grok credentials or browser state; the packaged hook commands only invoke the
documented Traceary hook entrypoints.

3. Verify the effective installation from the project that Grok will open.

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
git -C traceary fetch --tags
git -C traceary checkout v0.23.0 # replace with the installed Traceary version
./traceary/scripts/install-grok-plugin.sh
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

## Official references

- Grok Build hooks: https://docs.x.ai/build/features/hooks
- Grok Build skills, plugins, and marketplaces: https://docs.x.ai/build/features/skills-plugins-marketplaces
