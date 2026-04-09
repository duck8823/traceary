# Traceary

Local-first CLI and MCP server for recording AI agent work logs, session boundaries, and shell command audits.

## Tech stack

- Go (see `go.mod` for version)
- SQLite (`modernc.org/sqlite`, pure Go)
- Cobra (CLI framework)
- Architecture: hexagonal / DDD-inspired (domain → application → infrastructure / presentation)

## Source layout

| Directory | Role |
|---|---|
| `domain/` | Models and value objects (no external dependencies) |
| `application/usecase/` | Write-side use cases |
| `application/queryservice/` | Read-side query services |
| `infrastructure/sqlite/` | SQLite persistence |
| `presentation/cli/` | Cobra CLI commands |
| `presentation/mcpserver/` | MCP server (stdio) |
| `integrations/claude-plugin/` | Claude Code plugin package |
| `integrations/gemini-extension/` | Gemini CLI extension package |
| `plugins/traceary/` | Codex plugin package |
| `schema/sqlite/migrations/` | SQL migration files |

## Build and test

```sh
go build ./...
go test ./...
go tool golangci-lint run
```

## Project conventions

### Language

- Code, CLI messages, error messages, commit messages, issue/PR titles: **English**
- Documentation under `docs/`: bilingual (English + Japanese paired files)
- Issue/PR body: English or Japanese (either is fine)

### Issue management

- Issue titles are in English
- Sprint-style sub-issues use the pattern: `v{major}.{minor}-{seq}: {description}`
  - Parent issue: `v0.1.8: operational safety and public usability`
  - Child issues: `v0.1.8-1: merge Traceary hooks into existing client config safely`
- Use GitHub sub-issues (parent/child hierarchy) when available
- Labels and milestones are not currently used; version prefix in issue title serves as the grouping mechanism

### Git workflow

- Branch naming: `feature/`, `fix/`, `maintenance/` prefixes
- Commits: conventional-style (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`)
- PRs: merge (not squash), draft first
- AI commits include `Co-authored-by` trailer

### Issue closing policy

- Implementation PRs close **sub-issues** only (`Closes #<sub-issue>`)
- Parent (version) issues are closed by the **release PR** that updates metadata and CHANGELOG (`Closes #<parent>`)
- Never put `Closes #<parent>` in an implementation PR — the parent stays open until all sub-issues are done and the release is prepared

### Code style

- `go tool golangci-lint run` must pass before committing
- `go test ./...` must pass before committing
- Test names use Japanese descriptions (table-driven with subtests)
- No `panic()` in runtime paths — reserved only for programming errors in init-time assertions
