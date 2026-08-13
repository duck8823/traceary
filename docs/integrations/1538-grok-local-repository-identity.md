# Design note: Grok local-repository identity migration (#1538)

[日本語](./1538-grok-local-repository-identity.ja.md)

## Problem

Grok Build installs a local checkout at repository scope. Without its
`#integrations/grok-plugin` subdirectory selector, the host can register the
repository identity instead of the `traceary-grok` package name. A later
canonical install is then rejected as already installed.

## Boundary and decision

The plugin list has two separate concepts:

| Concept | Source | Use |
| --- | --- | --- |
| Package name | manifest package entry | identifies canonical `traceary-grok` |
| Local-repository identity | `repo_key` plus local `source` inventory metadata | identifies an older repository-scope install |

Doctor reads only this inventory metadata and hook contract metadata. It does
not inspect installed plugin files, prompts, transcripts, credentials, or
plugin payloads.

The default installer is non-destructive. It stops with exit 78 when the
current checkout's plugin subdirectory is registered as a non-canonical local
identity. The explicit `--migrate-local-repo-identity` operation removes only
that exact source match, then installs `repository#integrations/grok-plugin`.
A legacy package with another source is never selected or removed.

## Observable acceptance tests

- clean home installs the canonical package via the subdirectory selector;
- canonical package refresh replaces only `traceary-grok`;
- an old local-repository identity stops without an uninstall;
- an explicit migration removes only the exact local source and converges to
  canonical hooks and skills;
- a legacy `traceary` package with another source remains untouched.
