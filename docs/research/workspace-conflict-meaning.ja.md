# 決定: workspace conflict は何のためか (#1768)

[English](./workspace-conflict-meaning.md)

**Status:** 決定済み。`store workspace-alias` は残す。actionable な単位は pair。

**Date:** 2026-08-15

**Issue:** #1768

## 決定

workspace の `conflict` は、operator が review する pair `(session_id, effective workspace)` です。effective は既知で、session の canonical と異なり、`exact` / `descendant` / `ancestor` / review 済み `explicit_alias` のいずれでもありません。

store の欠陥ではなく、`traceary store workspace-alias` を廃する理由でもありません。分類器は契約どおりです。remote identity と local path は operator が review するまで別物で、祖先でも子孫でもない 2 本の local tree も別物です。

`report workspace-identity` は observation 行数を volume として残します。あわせて **distinct な conflict pair** を出し、sample は **pair あたり 1 行**（workspace 付き）にして、是正コマンドに届くようにします。

## 回答

### 1. conflict は actionable か

はい。review の単位は observation 行ではなく pair です。

| 形 | `conflict` になる理由 | 正しい操作 |
|---|---|---|
| remote identity 対 local path（`github.com/org/repo` 対 `/abs/path`） | 契約が自動 alias を禁じる | 同じ checkout なら alias。違えば残す |
| 祖先でも子孫でもない 2 本の local tree | 本当の cross-workspace event | conflict のまま、または既知 worktree なら alias |
| 同じ pair が毎 `post_tool_use` で繰り返される | hook の頻度であり新しい問題ではない | pair を一度 review する |

Antigravity が distinct pair を多く、Codex が行を多く出すのは hook の回数であり、分類器が 2 つあるわけではありません。`post_tool_use` は tool 呼び出しごとに observation を書き、`stop` / `stop_transcript` は turn に 1 回です。ソースで説明がつきます。この Issue は live store を照会しません。

### 2. report は行ではなく pair を数えるべきか

**両方**です。observation 行の合計は残します。これは volume であり、重複排除の決定ではありません（契約どおり）。現行の `conflict` projection における distinct `(session_id, workspace)` が actionable な件数です。sample は pair あたり最新の 1 observation で、`workspace` を含むので `doctor --alias-add --session … --workspace …` を report から実行できます。管理 surface は v0.42.0（#2075）で `store workspace-alias` から移しました。

行ベースの `conflict_rate` は変えません。

### 3. `store workspace-alias` はどうするか

v0.42.0（#2075）で管理 surface は `doctor --alias-add` / `--alias-remove` / `--alias-list` に移しました。alias 行と conflict 契約は変わりません。

review 済み alias の mechanism は残します。追加・撤回・一覧する唯一の public 経路です。既存 alias は read（`explicit_alias`）でも以後の write でも意味を持ちます。remote を path に自動正規化する、family 単位の規則を置く、は契約違反です。conflict 契約を撤回すると mechanism が凍結し、代替がありません。

## 対象外

- review 済み alias mechanism の廃止（CLI 名 `store workspace-alias` は #2075 で畳みました）
- live store の照会や書き換え
- remote から checkout への自動 alias
- 行ベースの `relationships.conflict` や `conflict_rate` の変更
