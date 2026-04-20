# Memory blocks: 評価と決定

[English](./memory-blocks.md)

letta-ai/claude-subconscious などが提案する「型付きブロック」を durable memory に導入すべきかを評価し、決定を記録します。

**結論**: **不採用**。blocks 軸が約束するユーザー体験（*resume* / *review* / *incident* 相当の取り出しモード）は、既存の `type` + `scope` の組み合わせと v0.8-5 (#570) で計画されている retrieval preset だけで十分に実現できます。独立した `block` 軸を足すと `type` と責務が重複し、複雑さだけが増えて純粋な利得がありません。

## memory-blocks 軸とは

letta-ai/claude-subconscious 等は「単一 blob な durable memory は性能が悪く、用途別のブロックに分けた方が取り出し品質が上がる」と主張します。典型的なブロック例:

- `guidance` — agent の振る舞い指示
- `preferences` — ユーザー好み
- `project-context` — プロジェクトの現況
- `recurring-patterns` — 繰り返し出現するパターンから抽出した教訓
- `unfinished-work` — 後で拾う open タスク

このブロック識別が「記憶をどの状況でどのように注入するか」を明示的に制御する、というのが blocks の提案です。

## Traceary の既存軸

Durable memory は現状 4 つの軸で記述されています（スキーマ: `schema/sqlite/migrations/000008_create_memories.sql`）。

| 軸 | 値 | 責務 |
| --- | --- | --- |
| `type` | `preference`, `decision`, `constraint`, `lesson`, `artifact` | **どういう種類の知識か** |
| `scope` | `workspace`, `agent`, `session_family` | **どの範囲で有効か** |
| `status` | `candidate`, `accepted`, `rejected`, `superseded`, `expired` | **ライフサイクル状態** |
| `confidence` | `low`, `medium`, `high`, `verified` | **どれくらい信頼できるか** |

retrieval pipeline (`application/usecase/context_pack_builder.go`, `MemoryListCriteria`) は既に `scope` / `status` / `type` / `source` でフィルタしています。

## ブロック提案を既存軸に写像する

| letta のブロック | Traceary での既存表現 |
| --- | --- |
| `guidance` | `type=decision` もしくは `type=lesson`、scope は `agent` / `workspace` |
| `preferences` | `type=preference`（そのまま一致） |
| `project-context` | `scope=workspace`（scope 軸そのものが project-context に相当） |
| `recurring-patterns` | `type=lesson` |
| `unfinished-work` | 綺麗に写像できない。これは *status* に近い概念（「未完了」）で、durable fact ではなく pending-task signal のたぐい |

提案されたブロックのうち 4 つは既存の `type` + `scope` の組み合わせで表現できます。残り 1 つ (`unfinished-work`) は構造が異なり、そもそも durable-memory 基盤の外にあるべきもの（audit 事象 / session handoff / 専用の作業キュー種別）です。

## `block` 軸を足すと何が悪くなるか

1. **`type` との責務重複**: `type` は既に分類軸です。さらに `block` を重ねると、生成側（prompt / hook / MCP）は似たような分類判断を二度する必要があり、一貫性が崩れます。
2. **`scope` との責務重複**: `project-context` ブロックは実質 `scope=workspace` の言い換えで、retrieval の意図が scope と block でぶつかります。
3. **インデックス圧**: 現状のインデックスは `(scope_kind, scope_value, status, updated_at, id)` と `(type, status, updated_at, id)` です。高 cardinality なフィルタを 1 軸足すと、インデックスを増やすか特異度を犠牲にするかの二択になります。
4. **利得のない migration**: `block` カラムを足すなら `ALTER TABLE` + 既存行への back-fill が要ります。back-fill の根拠は前述の「type → block」写像そのもので、双方向に可逆 — つまり新軸は既存軸以上の情報を持っていないことの証拠です。
5. **Host bridge の負担**: MCP / CLI / CLAUDE.md / AGENTS.md / GEMINI.md への bridge すべてが新軸を学び直す必要があります。`type` と重複した分類を足すために回すコストではありません。

## Traceary として代わりに進めること

memory blocks が約束する「*open work を resume*」「*過去の決定を review*」「*インシデント関連を一括抽出*」は、**retrieval presets (v0.8-5, #570)** の方がきれいに実現できます:

- **`resume` preset**: session-family scope の `type=decision` / `type=lesson` に加え、audit events から未完了 signal を拾う（新 memory block ではなく event 側で処理）。
- **`review` preset**: `type=decision` / `type=constraint` を広い時間窓で取る。
- **`incident` preset**: 時刻範囲を絞って `type=lesson` + 直近失敗 + 関連決定を束ねる複合クエリ。

これら preset は application 層の拡張で済み、schema 変更不要です。運用フィードバックを受けて柔軟に調整できます。

## v0.8-3 temporal validity (#565) への波及

`unfinished-work` を memory block として扱わないと決めたことで、その自然な居場所は audit event / session summary の pending-work signal になります。そのため durable memory の `valid_from` / `valid_to` が「この open task はまだ open」を表現する必要はなくなり、v0.8-3 の設計範囲を狭く保てます。

## 決定

- Durable memory に `block` 軸を **導入しない**。
- 代わりに v0.8-5 (#570) **retrieval presets** を進め、resume / review / incident の提案された用途を preset 側で吸収する。
- `type` / `scope` / `status` / `confidence` の組み合わせで表現できない具体的な retrieval ニーズが新たに出てきた場合にのみ再評価する。

## 参考

- letta-ai/claude-subconscious: memory block 構造の議論（公開 repo / blog）
- 現状の durable-memory model: `domain/model/memory.go`, `domain/types/memory_type.go`, `domain/types/memory_scope.go`, `domain/types/memory_status.go`
- Retrieval pipeline: `application/usecase/context_pack_builder.go`, `application/types/memory_list_criteria.go`
- Schema: `schema/sqlite/migrations/000008_create_memories.sql`
- Retrieval-preset follow-up: #570 (v0.8-5)
