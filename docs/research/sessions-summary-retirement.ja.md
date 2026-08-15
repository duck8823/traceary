# `sessions.summary` は要約の正本ではない

[English](./sessions-summary-retirement.md)

#1706 は `sessions.summary` をセッション要約の読取元から外す。

## 決定

- 読者（session list / tree / lineage、およびそれを使う handoff）は `session_refinements.summary` を使う。
- `session end --summary` は refinement を書く（`produced_by=cli:session-end`、`covers_to` は session-ended イベント）。
- `SetSummaryIfEmpty` は削除。post-compact はもともと refinement を書いており、列への二重書き込みはしない。
- 列は残す。DROP は data-dependent offline migration であり、events があるストアの暗黙 migrate は拒否される（#1852）。履歴の残りは読まれない。

## 列を残す理由

DROP は events があるすべてのストアに `traceary store init` を強いる。v0.36 の暗黙 open ではやらない。
