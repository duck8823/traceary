# Clean My Agent relay 互換性: 評価と決定

[English](./clean-my-agent-compatibility.md)

このドキュメントは、Traceary の session / event / bundle / replay の各サーフェスを [Clean My Agent](https://github.com/blain3white/clean-my-agent) の universal relay schema に揃えるべきかを評価し、決定を記録します。このリファレンスは Clean My Agent のメンテナーが #1170 からプロジェクトをリンクしたことで浮上しました。比較をこの 1 箇所に閉じることで、実装イシュー (#1170, #1171, #1172, #1173, #1169) を実装に集中させます。

**結論**: **参照のみ（reference-only）**。Traceary は下記のマッピングを文書化し、Clean My Agent のストレージ形式ドキュメントを host 側 session ストレージの外部 ground truth として利用します（#1170 の回帰フィクスチャ、#1171/#1174 の診断）。しかし v0.21.0 では `clean-my-agent.universal-session.v1` のエクスポートは**行いません**。upstream のスキーマが独立した versioned spec として安定した時点で再評価します。

## Clean My Agent とは

Clean My Agent は local-first のデスクトップアプリです（MIT ライセンス。確認済み最新リリース `v0.1.3`、2026-06-09 公開）。Codex / Claude Code / Cursor / Gemini / OpenCode の session データをスキャンし、利用分析・バックアップ・エクスポート・クリーンアップ提案・アプリ管理の Trash・リストアを提供します。

Traceary に関係する成果物は 2 つです。

1. **`docs/agent-storage-formats.md`** — host の session ストレージのルートとレコード形状を schema-only で文書化したもの。プロンプト本文・ツール出力・認証データ・ユーザーファイル内容のコピーを明示的に避けています。
2. **`UniversalRelayDocument`** (`src/shared/types.ts`) — アプリの relay/export 機能が生成する、`clean-my-agent.universal-session.v1` という識別子を持つエクスポートスキーマ。

一次情報の検証（2026-06-10）で判明した正確性に関する注意が 2 点あります。

- universal relay schema は `docs/agent-storage-formats.md` には**定義されていません**。アプリの TypeScript 型と relay view コンポーネントの中にのみ存在します。独立した JSON Schema や互換性ポリシーはまだありません。
- `docs/agent-storage-formats.md` は Codex / Claude Code / Cursor / OpenCode を文書化していますが、**Gemini のセクションはありません**。#1171 から参照されている Gemini ストレージルート（`~/.gemini`, `~/.config/gemini`, `~/Library/Application Support/Gemini`）は、同ドキュメントではなくアプリの Gemini scanner provider のコード由来です。

## universal relay schema（v0.1.3 時点）

`UniversalRelayDocument` のフィールド（`src/shared/types.ts` より）:

| フィールド | 形状 |
| --- | --- |
| `schema` | `"clean-my-agent.universal-session.v1"` |
| `exportedAt` | timestamp |
| `source` | agent 識別子（例: `codex`） |
| `session` | `SessionRecord`: `id`, `source`, `title`, `projectName`, `projectPath`, `branch`, `storagePath`, `storageKind` (`file`/`directory`/`database`), `storageState`, `createdAt`, `lastUpdated`, `messageCount`, `tokens`, `sizeBytes`, `backupStatus`, `tags`, `searchText`, `metadata` |
| `messages` | `UniversalRelayMessage` の配列: `id`, `role` (`system`/`user`/`assistant`/`tool`/`unknown`), `createdAt`, `text`, `raw` |
| `files` | `{path, reason, lastSeenAt}` の配列 |
| `commands` | `{command, cwd, createdAt}` の配列 |
| `git` | `{branch, projectPath, diff}` |
| `attachments` | `{path, mediaType, sizeBytes}` の配列 |
| `warnings` | 文字列の配列 |

## マッピング: Traceary サーフェスと relay 概念

| Traceary サーフェス | relay 概念 | 適合度 | 備考 |
| --- | --- | --- | --- |
| `Session` (`session_id`, `started_at`, `ended_at`, `runtime_mode`, `terminal_reason`, `client`, `agent`, `workspace`, `label`, `summary`) | `session` (`id`, `createdAt`, `lastUpdated`, `source`, `projectPath`/`projectName`, `title`) | 部分的 | `ended_at`、`runtime_mode`、`terminal_reason` に対応する relay スロットはありません（終了時刻に最も近いのは `lastUpdated`）。`summary` にもありません（強いて言えば `session.metadata`）。relay の `tokens`, `sizeBytes`, `storageState`, `backupStatus` には Traceary 側の情報源がありません。 |
| `Session` の subagent 系譜 (`parent_session_id`, `spawn_event_id`, `subagent_kind`, `spawn_order`) | なし | なし | relay ドキュメントはフラットな session 単位エクスポートです。Traceary の session ツリー（親子の spawn 系譜）には relay 上の表現がなく、エクスポートすると subagent session が親から切り離されます。 |
| `Event` `kind=prompt` | `messages[]` の `role=user` | 良好 | Traceary は `id`, `createdAt`, `text` を埋められます。 |
| `Event` `kind=transcript` | `messages[]` の `role=assistant` | 良好 | 同上。 |
| `Event` `kind=compact_summary` | `warnings[]`（または `messages[]` の `role=system`） | 欠損あり | compaction 境界は Traceary のライフサイクル概念で、relay に等価物はありません。 |
| `Event` `kind=note` / `reviewed` / `session_started` / `session_ended` | なし（強いて言えば `warnings[]`） | なし | ライフサイクル・レビューイベントに relay 側の置き場はありません。 |
| `CommandAudit` / `Event` `kind=command_executed` | `commands[]` (`{command, cwd, createdAt}`) | 欠損あり | relay は Traceary の最も豊かな監査データ（`exit_code`, `failed`, `input`, `output`, truncation/redaction フラグ）を落とします。`cwd` は workspace がローカルパスの場合にのみ近似できます。 |
| workspace メタデータ (`workspace`) | `git.projectPath` / `session.projectPath` | 部分的 | workspace がファイルシステムパスの場合は対応します。リモート URL workspace に対応するスロットはありません。 |
| branch メタデータ | `git.branch` / `session.branch` | ギャップ | Traceary は git branch を永続化していません。host payload には含まれます（例: Codex `session_meta.payload.git.branch`）が、その捕捉は v0.21.0 のスコープ外です。 |
| body truncation/redaction メタデータ (`input_truncated`, `output_truncated`, `input_redacted`, `output_redacted`。#1173 で拡張予定) | `warnings[]` | 良好（先例として） | relay の `warnings` は有用な先例です: ingest 時の truncation は下流の消費者から見えるべきで、無言であるべきではありません。 |
| durable memory (`Memory`: type/scope/status/confidence) | なし | なし | relay は session 単位の会話エクスポートで、durable memory は session 横断の知識です。役割が異なります。 |
| bundle export (`manifest_version=2`、NDJSON テーブルの tar.gz) | なし | なし | Traceary の bundle は conflict policy を持つストア全体のバックアップ/移送形式で、session 単位の relay ドキュメントではありません。 |
| replay (`traceary replay` の HTML) | なし | なし | replay はオペレーター向けのレビュービューで、機械可読の交換形式ではありません。 |

## 決定

**スキーマを参照する。ただし v0.21.0 ではエクスポートしない。**

理由:

1. **スキーマの成熟度。** スキーマは `v0.1.x` のアプリケーションの TypeScript 型の中にインラインでのみ存在します。独立した versioned spec も JSON Schema も互換性ポリシーの文書もありません。今これをターゲットにすると churn を追いかけることになります。
2. **サーフェス規律。** Traceary の MCP tool 数は凍結されており、CLI サーフェスも意図的に厳格です。（bundle と replay に続く）3 つ目のエクスポートサーフェスには「スキーマが存在する」以上の正当化が必要です。
3. **情報形状のミスマッチ。** relay ドキュメントの半分は空になり（`branch`, `tokens`, `sizeBytes`, `files`, `attachments` には Traceary 側の情報源がない）、Traceary が最も豊かな半分（exit code・失敗フラグ・truncation メタデータ付きの command audit）は `{command, cwd, createdAt}` に平坦化されます。
4. **消費者需要の不在。** サードパーティの `universal-session.v1` ドキュメントを消費するツールは現在存在しません。

再評価のトリガー（いずれか）:

- upstream がスキーマを互換性ポリシー付きの独立した versioned spec（JSON Schema 等）として公開する
- このフォーマットの 2 つ目の producer または consumer が現れる
- Traceary が branch メタデータを永続化し始め、最大のマッピングギャップが解消される
- Traceary の session を中立形式で別ツールに渡す具体的なオペレーターワークフローが生まれる

互換エクスポートを後日実装する場合でも、MCP tool は追加してはなりません（tool サーフェスは凍結）。その時点で明示的な additive CLI サーフェスとして設計します — この note は意図的にコマンド形状を事前確約しません。

## v0.21.0 がこのリファレンスから採用するもの

- **フィクスチャと診断のための host ストレージ ground truth。** Codex ルート（`~/.codex/sessions/YYYY/MM/DD/*.jsonl`, `~/.codex/archived_sessions/*.jsonl`, `~/.codex/session_index.jsonl`。レコードは `session_meta` / `response_item` / `event_msg` / `turn_context` の union）は #1170 の回帰フィクスチャの根拠になります。Claude Code ルート（`~/.claude/projects/<encoded-project-path>/*.jsonl`, `~/.claude/transcripts/*.jsonl`）は #1174 の coverage gap 診断を支えます。Gemini scanner ルートは #1171 の root-cause 比較を支えます。
- **フィクスチャポリシー。** フィクスチャは schema-shaped のみ: 実際のプロンプト本文・ツール出力・認証情報・ユーザーファイル内容を含めません。これは Clean My Agent のドキュメント方針とも #1170/#1171 の受け入れ基準とも一致します。
- **memory hygiene (#1169) のための安全セマンティクスの参照。** Clean My Agent のモデル — read-before-write、明示的なクリーンアップ提案、リスクあるクリーンアップ前のバックアップ、アプリ管理の Trash/リストア — は Traceary の dry-run-first クリーンアップと evidence-first レビューに対応します。Traceary は既存の姿勢を維持します: bulk accept はせず、この比較を理由に破壊的クリーンアップを追加しません。
- **truncation 可視性の先例 (#1173)。** relay は変換上の注意を `warnings` で表面化します。Traceary の ingest 時 truncation メタデータも同様に、無言ではなく CLI/MCP 出力で見えるべきです。
- **session liveness 語彙の確認 (#1172)。** relay の `session.storageState` は、下流の消費者が明示的な liveness/state フィールドを求めることを示しています。#1170/#1172 の status 語彙（例: 終了後の late events）も同じ理由で明示的であるべきです。

## 非ゴール（#1177 から変更なし）

- Clean My Agent を vendor しない。
- デスクトップのクリーンアップ UI を実装しない。
- 破壊的なクリーンアップ挙動を追加しない。
- 実際のローカル agent ログをテストに取り込まない。

## 情報源

- リポジトリ: <https://github.com/blain3white/clean-my-agent>（MIT、`v0.1.3` 2026-06-09 リリース。2026-06-10 検証）
- `docs/agent-storage-formats.md`（ストレージルートとレコード形状）
- `src/shared/types.ts`（`UniversalRelayDocument`, `SessionRecord`, `UniversalRelayMessage`, `ExportFormat`）
- `src/features/relay/RelayView.tsx`（relay/export 機能）

## 関連文書

- [アーキテクチャ原則](./README.ja.md)
- [イベントライフサイクル](../lifecycle.ja.md)
- [Hook contract](../hooks/contract.ja.md)
- [バックアップガイド](../backup/README.ja.md)
- 実装イシュー: #1170, #1171, #1172, #1173, #1169
