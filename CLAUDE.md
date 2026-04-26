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

- **1 issue = 1 branch = 1 PR (no exceptions)**
  - Create a dedicated branch for each issue before starting implementation
  - A single PR must close exactly one sub-issue
  - Never bundle multiple issues into one branch or PR
  - At sprint start, create all branches upfront before coding
- Branch naming: `feature/`, `fix/`, `maintenance/` prefixes
- Commits: conventional-style (`feat:`, `fix:`, `docs:`, `refactor:`, `chore:`)
- PRs: merge (not squash), draft first
- AI commits include `Co-authored-by` trailer

### Code review policy

- Maintainers should obtain an additional AI review (e.g., Gemini scout or Codex verifier) before merge when available
- External contributors are not required to use specific AI tools
- Review comments should include the reviewing AI name when applicable (e.g., `Gemini scout: ✅`)

### Issue closing policy

- Implementation PRs close **sub-issues** only (`Closes #<sub-issue>`)
- Parent (version) issues stay open through release-preparation PRs and are closed only after the **tagged release workflow** publishes the actual release
- Never put `Closes #<parent>` in an implementation PR or release-preparation PR

### Release process

1. `make release/bump VERSION=X.Y.Z` — update VERSION file and plugin manifests
2. Create release branch, commit, push, open PR
3. Multi-AI review → merge release PR
4. `git tag vX.Y.Z && git push origin vX.Y.Z` — trigger release workflow
5. `gh run watch` — wait for release workflow to complete
6. Verify: `gh release view vX.Y.Z` — release published
7. Verify the **Homebrew formula PR** (auto-created on `maintenance/homebrew-vX.Y.Z`) is merged — the release workflow enables auto-merge, but confirm it completed
8. `brew update && brew upgrade traceary` — verify Homebrew installation
9. Verify: `traceary -v` — correct version
10. Confirm parent release issue is auto-closed

### Code style

- `go tool golangci-lint run` must pass before committing
- `go test ./...` must pass before committing
- Test names use English descriptions (table-driven with subtests)
- No `panic()` in runtime paths — reserved only for programming errors in init-time assertions

### Durable memory capture (agent guidance)

Memory capture in v0.11.0+ is split into two narrow skills plus hook-driven auto-extraction:

- `traceary-memory-review` — list and curate inbox candidates, write a short session recap. Trigger phrases are review-intent only ("Traceary inbox", "review memory candidates", "session recap").
- `traceary-memory-remember` — write durable memory **only** when the user explicitly asks ("remember that", "覚えておいて"). Lands in `status=candidate` for review, never auto-accepted.

Hook-driven auto-extraction (planned in v0.11.0 #810 / #811) populates the inbox so the LLM does not have to. The deprecated `traceary-memory-capture` skill is retained as a stub through v0.11 and removed in v0.12.
