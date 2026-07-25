# インデックス利用のメタデータ list/context クエリ

[English](index-backed-metadata-list.md)

## Structure-Behavior 設計メモ

### 要求

- 一般的なメタデータ list/context 読取は、RFC3339Nano の境界を正しく並べつつ全件一時ソートを行わない。
- workspace/session 絞り込みと offset ページングでも本文を読まない。
- スキーマ更新は追加だけで行い、アプリケーションのロールバック後も既存ストアを読める。

### 概念と責務

| 概念 | 責務 | 不変条件 |
| --- | --- | --- |
| 正規化時刻順 | 永続化した event 時刻列と SQLite インデックス | `created_at_norm, id` を唯一の順序契約にする |
| 絞り込み済みメタデータページ | metadata datasource | `events.body` を SELECT しない |
| インデックス配布 | migration 000031 | `created_at_norm` を追加・バックフィルし、trigger で維持する |

### 対応するクエリ計画

| クエリ形状 | 順序インデックス |
| --- | --- |
| 一般メタデータ list（limit/offset を含む） | `idx_events_created_at_norm_id_desc` |
| workspace list/context | `idx_events_workspace_created_at_norm_id_desc` |
| session list/context | `idx_events_session_created_at_norm_id_desc` |
| workspace + session context | `idx_events_workspace_session_created_at_norm_id_desc` |
| source-hook list | `idx_events_source_hook_created_at_norm_id_desc` |

公開済みフィルタの意味を保つため、list の条件は任意のままとする。計画は順序インデックスを走査し、scope だけのページでは `offset + limit` に達したら終了する。scope 以外のフィルタはその順序走査中に適用する。組み合わせごとの SQL を増やして結果の意味を変えない。

### 振る舞いテスト

1. 代表的な list/context の `EXPLAIN QUERY PLAN` が対象インデックスを使い、order-by 用の一時 B-tree を作らないことを確認する。
2. 通常 list と scope/offset ページの時刻順を確認する。
3. metadata SQL の SELECT リストで `body` と command payload 列を禁止する。
4. 000031 前のストアを更新し、固定幅時刻がバックフィルされ、insert と時刻更新後も維持されることを確認する。

### ロールバックと残リスク

000031 は追加かつ冪等である。旧アプリは追加列、trigger、置換インデックスを無視するため、アプリのロールバック後もストアを読める。巨大ストアでのインデックス作成には一時的に容量と書込みロックが必要である。p95 は環境依存の時間計測ではなく、インデックスを使う計画で回帰を防ぐ。
