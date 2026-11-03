# Memory provenance boundaries

[日本語](provenance-boundaries.ja.md)

## Decision and threat model

Issue #2448 characterizes existing boundaries; it does not introduce an authentication system or report a confirmed exploit. Keep the candidate/review lifecycle. Defer content receipts, new schema and automatic trust classification until a separate design decision. An accepted memory is context, **never authorization** to execute a command, deploy, access credentials or expand permissions.

Assume prompt, transcript, imported document and tool content can contain hostile quoted instructions. A `remember-intent` source is a heuristic label, not proof that the authenticated user authored or approved those words. Evidence references identify material to inspect; they neither authenticate its author nor pin external content bytes. Unknown authorship remains unknown even after review.

## Source and trust matrix

| Input | Retained lineage | What must not be inferred |
|---|---|---|
| Direct user preference | Event/session refs, workspace; possibly remember-intent | Hook event origin does not authenticate nested quoted text |
| Quoted third-party instructions | Source event refs when extracted | A quoted “remember” request is not user authorization |
| Assistant summary | compact-summary/extracted source and original event ref | Summary is not an independent authority or exact original quotation |
| Tool/document content | Enclosing event/import refs, if eligible | Tool output cannot grant permission |
| Imported memory/instructions | imported source and supported file/path refs | Import does not refresh stale policy or make it accepted |
| Manually reviewed text | Explicit Accept keeps sanitized fact/source/refs; Distill uses operator-authored fact and unions source refs | Accepted status is not action permission; confidence is not identity proof |

## Lifecycle trace and operator responsibilities

1. Extraction and import create candidates. Even malicious-looking or quoted text may survive as a candidate; suppression of all such text is not a guarantee. Benign examples must remain reviewable.
2. Review the **sanitized displayed fact**, source, evidence, workspace and validity together. `memory show` retains source refs. Accept requires evidence but does not fetch referenced bytes or authenticate user intent.
3. Distill is an explicit accepted write using operator-authored content, selected scope and source-replacement policy. Source evidence is retained; source claims are not elevated to authority. Never approve generated text without inspecting it.
4. Current scoped retrieval excludes foreign scopes and stale/terminal statuses under the requested filters. Operators can explicitly request historical/other scopes; that is an audit capability, not a bypass guarantee for arbitrary clients.
5. Export is accepted-only and current-validity by default. Its Markdown contains fact, memory ID and scope, **not full source/evidence/validity metadata**. The store/detail view remains necessary for provenance review. Host activation is a separate explicit operation, not a side effect of extraction, Accept, Distill or Export.
6. If content/evidence changes after review, stop and re-open the exact fact and evidence before confirming again. File/URL refs are mutable pointers, not content receipts. Existing APIs do not enforce an immutable review snapshot or stale-evidence rejection. Scope changes or superseded policy re-import require fresh review; importing text cannot restore prior authorization.

Secret-like material is redacted through existing rules; synthetic redaction checks do not establish detection of all secrets. No real logs, credentials or production memories are used here.

## Reproducible deterministic coverage

```sh
go test ./application/usecase -run 'TestMemoryProvenance|TestMemoryUsecase_ImportCodex_DoesNotResurrectRejected|TestMemoryBridgeImport' -count=1 -v
go test ./infrastructure/sqlite -run TestMemoryProvenanceExport -count=1 -v
```

The new suite has seven proposal/review cases, six extraction cases, explicit distillation, changed-evidence characterization, export-criteria coverage, and a real SQLite export containing one valid row plus five exclusion cases. It tests public usecases and persisted query behavior, not private call ordering. A synthetic file is changed between proposal and Accept to demonstrate the **absence** of content pinning, not a new protection.

Legitimate direct intent, summary and benign preference inputs must produce candidates (three positive extraction checks). Quoted/authority/tool-like inputs may produce candidates but must never auto-accept. Proposal/review cases preserve seven benign/adversarial texts, with secret-like content sanitized. Candidate counts are logged, not scored as attack rejection. Review burden, human intent false-positive rate and model susceptibility are unmeasured; no zero-failure security claim follows.

Optional host-model trials are **not run**. A future trial must freeze synthetic cases, record host/model versions, tested counts, false positives, unknowns, total tokens/time and reviewer effort; keep it separate from deterministic results. Existing source-structure tests do not certify arbitrary host behavior.

## Compatibility and rollback

No runtime, public API, schema, permission or production-memory changes. Revert this tests/docs PR to roll back. Coordinate with Desktop D8 without changing its UI or reopening completed extraction-noise work. Sensitive discoveries follow [SECURITY.md](../../SECURITY.md); do not publish live-user proof here.
