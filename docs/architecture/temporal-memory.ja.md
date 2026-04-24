# Temporal memory モデル — 評価と最小限の graph overlay

[English](./temporal-memory.md)

#567 の一部 · #573 の評価フェーズを閉じるドキュメント。

## 背景

v0.8 (#565) で、すべての accepted memory に半開区間 `[valid_from, valid_to)` が付きました。`traceary memory list --as-of <date>` で時間をさかのぼれ、`memory hygiene scan` の `validity_overlap_supersede` 検出も同じ窓を使って置換チェーンを提案します。

しかし、窓だけでは「時点 T で X について何を信じていたか」には答えられますが、**なぜ X が supersede されたか**、**X が隣接する事実とどう関係するか**は記録できません。Zep / Letta / Graphiti のような 2026 年の temporal memory システムは、memory をノード、関係を型付き edge としてモデル化し、次のようなクエリを実現します:

- 「DATE 時点で X について何を信じていたか。supersedes チェーンを辿って最新バージョンまで」
- 「Y と矛盾する事実は何か」
- 「事実 Z に依存する決定はどれか。Z が無効になったら再評価すべきものは？」

本ドキュメントは #573 の評価で、リファレンス設計を読み、Traceary の local-first SQLite モデルに何が実際に転用できるかを判断し、実用イテレーションの基点になる最小限の overlay を出荷するためのものです。

## リファレンス設計

### Zep (2024 以降)

- **temporal knowledge graph が主要ストア**。事実は first-class ノード、edge は関係種別と validity 窓を持つ。
- **バッキングストアは graph-native** (Neo4j / Graphiti)。hosted 版あり。self-host には graph DB の運用が必要。
- **基本クエリ**: 「件名 X に関する事実を時点 T で、<relation> edge を深さ N まで辿る」。

### Letta (旧 MemGPT)

- **block 構造のメモリ階層** (core / archival / external) — graph モデルとは別次元だが、階層分離 + 明示的参照で多くの運用では full graph の代替になることを示す。
- **ブロック間参照は ID のみで型無し**。

### Graphiti

- **OSS ライブラリ**。Neo4j ベースで Zep 流を self-host 化。
- **LLM 抽出 pass によるエンティティ / 関係抽出**が default ingestion。valid_from / valid_to は LLM が claim を抽出する際に推論する。

## Traceary に何を持ち込めるか

Traceary は **local-first、single SQLite、hosted 面なし、LLM ingestion 必須でない**。これが除外するもの:

- 関係モデリングだけのために Neo4j / Kuzu / graph 付き DuckDB を立てる。
- LLM 往復がないと ingestion が失敗する設計。
- hosted / マルチテナント前提の fan-out クエリ。

一方で中核アイデア — memory 間の型付き edge にそれ自身の validity 窓 — は、SQLite の 2 つ目のテーブルに綺麗に落ちます:

```sql
CREATE TABLE memory_edges (
    id              TEXT PRIMARY KEY,
    from_memory_id  TEXT NOT NULL REFERENCES memories(memory_id) ON DELETE CASCADE,
    to_memory_id    TEXT NOT NULL REFERENCES memories(memory_id) ON DELETE CASCADE,
    relation_type   TEXT NOT NULL,
    valid_from      TEXT NOT NULL,
    valid_to        TEXT,
    created_at      TEXT NOT NULL
);
```

`relation_type` は安定語彙 (後述)。`valid_from` / `valid_to` は memories と同じ理由 (#664) で、固定幅 9 桁ナノ秒タイムスタンプで保存します。

## 結論: additive overlay として採用

SQLite + 現行 `memories` テーブルを canonical な事実ストアとして維持し、その上に **型付き edge overlay** を重ねます。以下は **しない**:

- memory を graph 主ストレージに移す。
- LLM 抽出を必須化する。
- graph DB を deploy 依存にする。

overlay は **opt-in** — edge を記録しないユーザーには挙動変化ゼロ。

## 今リリースで出荷する最小限 overlay

### 関係語彙 (v1)

| Relation | 意味 |
|---|---|
| `supersedes` | `from` が `to` を置換。既存の `supersedes_memory_id` カラムを洗練する alias。カラムがチェーンを、edge は「この supersede の理由は R だった」(reason カラムが後で入る場合) を表す。 |
| `contradicts` | `from` が `to` と直接矛盾する。`hygiene scan` が矛盾を検出する素材になる。 |
| `supports` | `from` が `to` の根拠を提供する。「なぜ X を信じるか」チェーンを作れる。 |
| `related-to` | 弱い汎用リンク。より具体的な関係が使えるならそちらを優先する。 |
| `causes` | `from` が `to` を引き起こした。因果 / 依存性に限定。 |

未知の relation 型は round-trip で保存しますが、default view ではフィルタで除外します (`EventBodyBlockType` と同じ forward-compat ルール)。

### ストレージ

migration `000013_create_memory_edges.sql` でテーブルと以下 2 つの index を作成:

- `idx_memory_edges_from (from_memory_id, valid_from DESC)` — 「時点 T で X から出る edge」
- `idx_memory_edges_to (to_memory_id, valid_from DESC)` — 逆向き

### CLI

- `traceary memory graph add <from-memory-id> --to <to-memory-id> --relation <type> [--from <ts>] [--to <ts>]`
- `traceary memory graph list [--memory-id <id>] [--relation <type>] [--as-of <ts>] [--depth 1]`

depth > 1 は **v0.9 では scope 外**。v1 は 1 hop のみ。多段トラバースは運用フィードバック後に足す。

### MCP

- `memory_graph_add` と `memory_graph_query` ツールで CLI と同じ機能を MCP に露出。両方 `as_of` を honor する (`memory list --as-of` と同じ半開区間)。

## 明示的な scope 外

- **depth > 1 の多段トラバース**。実運用でどのチェーンが必要か見えてから実装。
- **サイクル検出**。opt-in edge はサイクルを作り得るが、許容する。呼び出し側が健全にモデル化する責任を負う旨を docs に記載。
- **LLM 駆動の edge 抽出**。自動抽出なし。edge は `memory remember` と同じく意図的に書く。
- **グラフ可視化**。replay HTML に edge を描画しない。運用需要が出てから追加。
- **edge の validity 重複 hygiene 検出器**。edge 履歴がたまってからの future follow-up。
- **edge の supersedes セマンティクス**。v1 では append-only。関係を「更新」したいときは新しい `valid_from` で新 edge を作る。

## Follow-up チケット (出荷時に作成)

- `memory graph walk` for 多段トラバース
- `hygiene scan edge_overlap` 検出器
- replay HTML の edge 可視化
- LLM 駆動の edge 推論 (明示的 flag の裏側)

## 再評価タイミング

v0.10 / v1.0 プランニングゲートでこの doc を再評価。実運用で edge overlay が「使われていない (defer / 削除)」「限界を超えている (first-class に昇格)」のどちらかが見えたらその時点で scope を調整する。先回りで広げない。
