# Grok public marketplace submission

[日本語](./grok-marketplace-submission.ja.md)

Part of #1301 · parent cut #1360.

## Goal

List the Traceary native Grok package (`integrations/grok-plugin/`) in the
[xAI Plugin Marketplace](https://github.com/xai-org/plugin-marketplace) so
operators can install without cloning Traceary, while keeping
`./scripts/install-grok-plugin.sh` as the deterministic local fallback.

## Catalog entry

1. On the Traceary commit that ships the package (prefer a release tag):

```sh
./scripts/generate-grok-marketplace-entry.sh v0.28.0
# or HEAD while preparing a PR
./scripts/generate-grok-marketplace-entry.sh HEAD
```

2. Append the printed JSON object to
   `xai-org/plugin-marketplace` → `.grok-plugin/marketplace.json` → `plugins[]`.

3. In that repository:

```sh
python3 scripts/generate-plugin-index.py
python3 scripts/validate-catalog.py
```

4. Open a PR. CI validates the catalog; code-owner review is required by xAI.

### Source contract

| Field | Value |
|---|---|
| `source.url` | `https://github.com/duck8823/traceary.git` |
| `source.sha` | Full 40-char commit SHA of the tagged release |
| `source.path` | `integrations/grok-plugin` |
| `name` | `traceary` |

Never commit secrets. The package only invokes Traceary hook entrypoints and a
local MCP stdio server.

## Local verification before submission

```sh
go run ./cmd/repo-tooling integrations verify
./scripts/verify-grok-plugin-clean-home.sh
```

Clean-home covers install, update (reinstall), details, and uninstall under a
temporary `HOME`.

## After marketplace merge

1. Document the host marketplace install command in release notes if the host UI
   path is stable.
2. On each Traceary release, open a follow-up marketplace PR that **bumps `sha`**
   (and `version`) to the new tag commit.
3. Keep local-source install documented forever as the offline / pin-to-tag path.

## Related

- [Grok Build plugin guide](./grok-plugin.md)
- Template entry: [`../../integrations/grok-plugin/marketplace-entry.json`](../../integrations/grok-plugin/marketplace-entry.json)
