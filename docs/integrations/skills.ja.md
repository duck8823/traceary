# 共有 integration skill

[English](./skills.md)

Traceary は **4** つの skill を出荷します（1 job につき 1 skill）。
`integrations/*` と `plugins/traceary` の各 host package が同じセットを持ちます。
skill は **Traceary CLI** 経由（MCP ではない）で案内し、shell 呼び出しが監査可能なままになるようにします。

| Skill | Job | 主な CLI |
| --- | --- | --- |
| `traceary-session-history` | 過去 session / event / audit の参照 | `list`, `search`, `context`, `show`, `report` |
| `traceary-session-refine` | session refinement を書き、後から body を discard できるようにする | `session refine` |
| `traceary-memory-review` | memory inbox の curate。任意で inline の session recap | `memory inbox list` / `accept` / `reject`（+ 対話 `review`） |
| `traceary-memory-remember` | 明示的な user ask のときだけ durable memory を propose | `memory store propose`（`memory store remember` は使わない） |

## 内容の契約

- **session-history** は Discovery → Inspection → Detail: まず小さな metadata 読み、bounded な `context`、最後に 1 件の `show`。
- **session-refine** は **Motivation** と **The change** が必須。**How it went** は任意。以前の summary がある場合は書き直しではなく merge。
- **memory-remember** は常に `status=candidate` で inbox に載せ、後から review する。
- **memory-review** は operator の id 単位の指示なしに candidate を auto-accept しない。

## package 配置

各 skill は `SKILL.md` を含むディレクトリです。

```text
integrations/{claude-plugin,gemini-extension,grok-plugin,kimi-plugin,antigravity-plugin}/skills/<skill>/SKILL.md
plugins/traceary/skills/<skill>/SKILL.md
```

同一 skill の host 間コピーは byte 同一に保ちます。host 固有の help command
（Gemini `/traceary-help` や Codex `/traceary:help` など）はこの skill 面とは別で、
CLI / hooks / doctor の案内に使います。

## 件数チェック

skill 名は 4、host package は 6 → **24** 個の `SKILL.md`。
`traceary-help` skill は存在しません。

## refine 依頼が agent に届く経路

仕事量ベースの pressure が due のとき、Traceary は agent に
`traceary-session-refine` を読むよう依頼します。どの channel の本文も
`[Traceary] Session <id>` で始まるので、host ごとの `SKILL.md` を編集せずに
skill の step 1 が成立します。

| Host | Channel | Delivery token |
| --- | --- | --- |
| Claude | `Stop` exit 2 + stderr | `stop_exit_2` |
| Codex | `Stop` exit 2 + stderr（主経路）。次の `UserPromptSubmit` の plain-text stdout（非割り込みの第二経路） | `stop_exit_2` / `additional_context` |
| Kimi | `Stop` exit 2 + stderr | `stop_exit_2` |
| Gemini | `BeforeAgent` `hookSpecificOutput.additionalContext` | `additional_context` |
| Antigravity | `Stop` `{decision:continue,reason}` | `additional_context` |
| Grok | `Stop` `{decision:block,reason}` を stdout に書き **exit 0**。出荷済み。host が受け取るかは未検証（docs.x.ai は passive event で stdout 無視と記述）。live probe で確認できるまで、Grok では skill はユーザーの言い回し経由に限られる場合があります。 | `additional_context` |

Stop envelope と prompt context は同じ `additional_context` token を共有し、
channel は `(client, delivery)` から復元します。
