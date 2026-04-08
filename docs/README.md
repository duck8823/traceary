# Documentation guide

[日本語](./README.ja.md)

Traceary keeps human-facing documentation in paired English and Japanese files.
This guide defines the naming and maintenance rules for repository docs.

## Scope

These rules apply to:

- repository-level Markdown docs such as `README.md` and `CHANGELOG.md`
- Markdown files under `docs/`

These rules do not apply to:

- machine-readable files such as JSON, YAML, or SQL
- examples under `examples/`
- generated content or third-party vendored files

## Naming

Use English as the default filename and add the Japanese variant with a `.ja.md` suffix.

Examples:

- `README.md` ↔ `README.ja.md`
- `CHANGELOG.md` ↔ `CHANGELOG.ja.md`
- `docs/hooks/README.md` ↔ `docs/hooks/README.ja.md`
- `docs/cli/search.md` ↔ `docs/cli/search.ja.md`

## Required pairing

When you add or rename a human-facing Markdown document in scope:

1. create both the English and Japanese files in the same change
2. keep the same relative path and basename
3. only change the language suffix (`.ja.md`) between the pair

Do not leave one language variant missing after the change lands on `main`.

## Language switch links

Every paired document must include a language switch link near the top of the file.

- English files should link to the Japanese variant with `[日本語](...)`
- Japanese files should link to the English variant with `[English](...)`

Keep the link immediately below the top-level title unless the document has a strong reason to do otherwise.
Use a same-directory relative link such as `./README.ja.md`.

## Update policy

When you update one language variant:

- update the other variant in the same pull request
- keep headings, examples, and version references aligned
- allow wording to differ for readability, but do not let the content drift

## CI enforcement

`python3 scripts/verify_docs_i18n.py` checks that:

- every in-scope English Markdown file has a Japanese pair
- every in-scope Japanese Markdown file has an English pair
- each file contains the expected language switch link near the top

GitHub Actions runs the same check in CI.

## Current document sets

- root overview: `README.md` / `README.ja.md`
- contribution guide: `CONTRIBUTING.md` / `CONTRIBUTING.ja.md`
- security policy: `SECURITY.md` / `SECURITY.ja.md`
- release history: `CHANGELOG.md` / `CHANGELOG.ja.md`
- hooks guide: `docs/hooks/README.md` / `docs/hooks/README.ja.md`
- MCP guide: `docs/mcp/README.md` / `docs/mcp/README.ja.md`
- release guide: `docs/release/README.md` / `docs/release/README.ja.md`
