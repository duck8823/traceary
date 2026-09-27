# Log-only migration validation and recovery

[日本語](./log-only-migration.ja.md)

Runtime target: `1eeea6d96f22cb6b9dc6d9cb382868877535fd1e` (merged #2407).
The maintainer approved adoption; the pending Claude review was cancelled, not recorded as approved.
P1 #2404, test isolation #2405, P2 #2406 and P3 #2407 are merged.

## Measured evidence

- Full unit/build/vet/lint, seven-host fixture maps, bilingual docs and fresh CI passed on implementation head `1bbf6151dea5842612f6b7e598002e309d667473`; merged wave passed on the runtime target.
- Selected `-race` registration/receipt/nested-capture/supervisor tests, count3, passed.
- Real integrated CLI: 18 controlled invocations passed on isolated synthetic stores/home/hook-state. Repeated start reused the real event; distinct ends stayed distinct; late/implicit logging and legacy-ended resume worked; generated handoff STATUS was absent; ordinary end could not change supervisor results.
- Encrypted export/import and repeated import preserved raw session fields and event counts in a fresh isolated recovery store. No production history was read or rewritten.
- CLI evidence digest: `44307bc3eb800fee392b173ea11e953f7638f7dc5836ac1946a5b574335708a9`. Raw outputs remain local; no credentials/encryption input are published.
Encrypted portability bundles are not full-store backups: the measured session_refinements count was source1/recovered0. L2 refinements/coverage are not exported by the current bundle format. A separate SQLite backup into a fresh isolated store preserved the actual refinement/coverage row and event counts; use full-store recovery when those records must be retained. No bundle schema expansion is included.

## Native host matrix

| Host | Fixture/map | Native capture | Limitation |
| --- | --- | --- | --- |
| Claude 2.1.281 | PASS | PASS | Per-invocation plugin, no tools/persistence; PATH and TRACEARY_BIN both pinned |
| Codex 0.157.1 | PASS | PASS | Ephemeral read-only execution |
| Antigravity 1.2.9 | PASS | PASS | Plan-mode native callbacks |
| Grok 1.0.41 stable | PASS | PASS | Plan-mode one-turn native callbacks |
| Muse 1.3.0-R3401.1 | PASS | PASS (echo provider) | Native frontend/hook execution, not Meta backend or numerical usage certification |
| Gemini 0.46.0 | PASS | NOT_RUN (completion) | Server UNSUPPORTED_CLIENT; no hook arrival; trust refusal also observed; no bypass/update attempted |
| Kimi 0.39.1 | PASS | NOT_RUN (completion) | Subscription 403; initial prompt/start arrived, complete response unavailable; no auth retry/update attempted |

Installed versions are observations, not latest-release claims.
Fixture/static map verification is not native certification.
The initial Claude probe omitted PATH pinning and an old writer terminalized only the isolated test row; it is a failed integration attempt, not PASS.
A fresh store with PATH/TRACEARY_BIN proxy proof passed and retained ordinary ended_at NULL.
Initial harness failures used an incorrect JSON key or invalid synthetic timestamp/UDF setup; corrected results do not erase those failures.
On 2026-09-28 (JST), the maintainer explicitly accepted Gemini/Kimi as fixture-only degraded coverage. P4 validation is accepted with that limitation; no seven-host complete native certification is claimed.
No install, login, subscription change, global configuration mutation or release publication was performed.

## Recovery boundaries

Keep raw legacy columns/events/refinements and receipt evidence.
An ended historical row is not a capture or retrieval gate; an open row is not proof a process is running.
One-shot outcomes remain recorded metadata with supervisor-only current writes, not authentication of historical actors.
Recover only into a fresh isolated copy and restore actual ancestors before descendants; a backfill placeholder cannot be promoted into one-shot ownership via a forgeable label.
Do not promise universal downgrade/roundtrip: older schema manifests may reject newer data, older binaries may discard optional spool receipt_id, and old GC/doctor/readers can reintroduce closure and eligibility rules.
Never partially roll back producers while consumers remain migrated.
Do not run old synthetic-close writers on live data or broaden empty/nonempty retention deletion.
Fully resolved ordinary workspace replay pinning remains a separate known gap.
