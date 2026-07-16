# Decision: next host evaluation — Copilot CLI vs opencode (#1256)

[日本語](./next-host-evaluation.ja.md)

**Status:** evaluation complete for v0.26.0 — **no host implementation in this issue**  
**Date:** 2026-07-16  
**Issue:** #1256

## Decision

- **Preferred next host candidate:** GitHub Copilot CLI (hook surface is documented and CLI-supported).
- **Do not implement** Copilot CLI or opencode inside #1256 / v0.26.0. Split any implementation into dedicated child issues under a later milestone after package + doctor + contract work is planned.
- **opencode:** keep as secondary watch item; plugin/event model exists but differs from Traceary’s shell-hook packaging and still needs a sanitized live probe against `application/hostcoverage` before commit.

## Primary sources

### GitHub Copilot CLI

- Hooks reference: https://docs.github.com/en/copilot/reference/hooks-reference  
  Documents hook events, configuration formats, and payloads for Copilot CLI (local shell hooks).
- Using hooks with Copilot CLI: https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks
- Hooks tutorial (repo-scoped `.github/hooks/`): https://docs.github.com/en/copilot/tutorials/copilot-cli-hooks

### opencode

- Plugins / events: https://opencode.ai/docs/plugins/  
  Event subscription model (`command.executed`, message events, etc.) rather than Traceary’s existing shell hook install path.

## Evaluation against v0.25 host framework

| Criterion | Copilot CLI | opencode |
|---|---|---|
| Documented hook/lifecycle surface | Yes (official hooks reference) | Plugin events (different model) |
| Local install without marketplace secrets | Likely via repo/user hooks JSON | Plugin TypeScript path |
| Fit to `scripts/hooks` + `hostcoverage` matrix | High (command hooks map to session/prompt/audit/stop patterns) | Medium (needs adapter layer) |
| Implementation risk in evaluation PR | High if bundled | High if bundled |

## Follow-ups (not in v0.26.0)

1. Sanitized live probe of Copilot CLI hooks → fixtures under `presentation/cli/testdata/` and rows in `application/hostcoverage/matrix.json`.
2. Design note for packaging (`integrations/…`) + doctor checks.
3. Separate implementation issues (1 issue = 1 PR) after probe passes.

## Non-goals confirmed

- Shipping a new host package in the evaluation PR.
- Claiming full lifecycle parity without versioned live fixtures.
