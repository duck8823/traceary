# Contributing to Traceary

[日本語](./CONTRIBUTING.ja.md)

Thanks for contributing to Traceary.
This guide covers local setup, validation, and the pull-request workflow.

## Local setup

Use the Go version declared in `go.mod`, clone the repository, and work from a feature branch created off `main`.

```sh
git clone https://github.com/duck8823/traceary.git
cd traceary
git switch -c your-topic-branch
```

## Common validation commands

Run these before opening or updating a pull request.

```sh
go test ./...
go tool golangci-lint run --timeout=5m
python3 scripts/verify_docs_i18n.py
git diff --check
```

`make ci` runs the same repository-level checks contributors usually need locally.

## Documentation rules

Human-facing Markdown is maintained in English/Japanese pairs.

- repository-level docs such as `README.md`, `CHANGELOG.md`, and this guide need a matching `.ja.md` file
- docs under `docs/` also need an English/Japanese pair
- update both language variants in the same pull request

See [docs/README.md](./docs/README.md) for the detailed documentation convention.

## Pull request expectations

When you submit a change:

1. branch from `main`
2. keep the scope small and reviewable
3. prefer one commit per concern
4. open a draft PR first when the work is still in progress
5. include the motivation and the validation commands you ran
6. wait for CI to pass before merge

Do not push directly to `main`.

## Reporting security issues

If you believe you found a vulnerability, please avoid opening a public issue first.
Instead:

- email the maintainer at `duck8823@gmail.com`, or
- use GitHub private vulnerability reporting if it is enabled for this repository

Include the affected version or commit, reproduction steps or a minimal PoC, and the expected impact when possible.

Traceary is maintained on a best-effort basis. The goal is to acknowledge private reports within 7 days and then coordinate the fix as quickly as practical.
