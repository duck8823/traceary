# 設計メモ: Grok subagent parent/child hook 契約 (#1299)

[English](./1299-grok-subagent-parent-child.md)

Status: unavailable としてクローズ（live re-probe 2026-07-16）
Risk: Medium（host contract / docs のみ。runtime 変更なし）

## 要求

Grok Build が parent/child 識別子付きの `SubagentStart` / `SubagentStop` payload を
発行し、Traceary が関係を合成せずに配線できるかを検証する。

## Live 証拠（Grok Build 0.2.101）

sanitized な空 workspace で `grok --permission-mode plan --print` を実行し、
ユーザー prompt で単一の subagent spawn を依頼した:

- ホストは `spawn_subagent` **tool** を使用した（`PostToolUse` 経由の
  `command_executed` として記録）。
- Traceary へ `SubagentStart` / `SubagentStop` hook event は届かなかった。
- subagent hook payload 上に専用の parent/child 識別子 field も無かった。

## 決定

- Grok の `SubagentStart` / `SubagentStop` は **traceary_support = unavailable**。
- tool 名や session ID だけから parent/child 対応を**発明しない**。
- 機械可読な分類は `docs/hooks/host-contract.json` に維持する。
- 新しい Grok Build が専用 hook を live 発行し、文書化された identity 契約と
  sanitized fixture が揃ったときだけ再オープンする。

## Non-goals

- ホストが既に許可している範囲を超える external-agent policy の変更。
- ネストした tool audit から lifecycle event を捏造すること。
