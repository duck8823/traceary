# Host-side lesson review template

[日本語](lesson-review-template.ja.md)

Use this only to draft/review a candidate; it is not an instruction to store, accept, activate, execute or install anything. Historical approvals and credentials are never transferable lessons.

## Candidate worksheet

- Candidate ID/version and exact proposed fact:
- Scope and validity window:
- Preconditions (when the rule applies):
- Failed attempt reference and independently checkable outcome:
- Successful attempt reference and independently checkable outcome:
- What was observed vs what is inferred:
- Known failure modes and counterexample tasks:
- Task-specific values/paths to remove; stale advice to reject:
- Contradictory/superseded evidence:
- Held-out task family/time split and frozen candidate hash:
- Matched no-lesson/current-summary/lesson trial results:
- Tokens, latency, generation/review effort and priced cost (unknown stays unknown):
- Incorrect application, repeated mistakes, uncertainty and untested cases:
- Decision: reject / defer / propose for explicit operator review:
- Rollback/retirement conditions:

## Map to existing Traceary fields

Keep the bounded final rule in `fact` with type `lesson`; use the existing workspace/agent/session-family scope, evidence refs to both attempts and artifact refs to the worksheet/outcome checks. Set existing validity explicitly where appropriate. The richer worksheet remains an artifact; do not add columns/schema or a generated skill to store it.

`memory store propose` creates a candidate. Operator-authored final distillation/review is separate and must inspect exact sanitized content, source evidence, scope and validity. A candidate/accepted label never changes tool permissions or authorizes execution. No template field triggers a CLI command, auto-acceptance, host activation, skill installation, model routing or instruction rewrite.
