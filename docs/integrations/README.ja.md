# ネイティブ連携パッケージ

[English](./README.md)

Traceary は、Claude Code / Codex / Gemini CLI（レガシー）/ Antigravity / Grok Build / Kimi Code 向けにネイティブ連携パッケージを用意しています。

> **v0.21.1 注記:** Gemini CLI はレガシーの Google AI エージェントホストです。**Antigravity**（`/Applications/Antigravity.app`）が後継ホストです。v0.21.1 以降、Traceary は Antigravity を実際の hook client としてサポートし、文書化された公開 hook surface に対する packaged plugin を提供します。詳細は [Antigravity hooks / plugin ガイド](./antigravity.ja.md) を参照してください。

これらのパッケージは、次の共通ランタイム契約でそろえています。

- `traceary` CLI が `PATH` 上にあることを前提にする
- MCP は共通で `traceary mcp-server` を起動する
- セッション境界と shell command audit は同梱 hook で記録する
- SQLite ストア、CLI フラグ、`traceary doctor` の流れは host ごとに分けない

## 共通の機能

| 機能 | 共通の振る舞い |
| --- | --- |
| MCP server | `traceary mcp-server` で Traceary の read/write tools を公開する |
| session hook | session start/end を Traceary event として記録する（Codex の `Stop` は応答ごとの turn 境界でありセッション終了ではない — #1170）|
| shell audit hook | `traceary audit` を通して shell command 実行を記録する |
| doctor flow | `traceary doctor --client <host>` を共通のトラブルシュート入口にする |
| versioning | integration package は Traceary の release と同じ version で公開する |

## host ごとの package root

| Host | Package root | 実際の配置先 |
| --- | --- | --- |
| Claude Code | `integrations/claude-plugin/` | `.claude-plugin/marketplace.json` を基点にした Claude marketplace |
| Codex | `plugins/traceary/` | Codex CLI 公式の `/plugins` flow を使い、リポジトリ内の marketplace `.agents/plugins/marketplace.json` から install。plugin manifest で同梱 `hooks.json` を参照するため session / prompt / audit hook が自動配線される。旧 `traceary integration` コマンドツリー（codex install/uninstall stub 含む）は v0.25.0 で完全削除 (#1266)。Codex 公式の `/plugins` flow と [docs/integrations/codex-plugin.ja.md](./codex-plugin.ja.md) の手動 cleanup 手順を使う。 |
| Gemini CLI | `integrations/gemini-extension/` | `gemini-extension.json` を root にした Gemini extension archive — v0.21.0 以降は**レガシー互換のみ**。アクティブな委譲パスではない |
| Antigravity | `integrations/antigravity-plugin/` | v0.21.1 でサポート。hook の直接設定は `<project>/.agents/hooks.json` または `~/.gemini/config/hooks.json` を対象とします。同梱 plugin は version 付き manifest、Traceary MCP server、共有の memory/session skill 3 件を追加します。`traceary doctor --client antigravity --json` で hook 経路、MCP 登録、plugin version の一致を確認できます。 |
| Grok Build | `integrations/grok-plugin/` | v0.23.0 でサポート。ネイティブ plugin は実環境で検証した lifecycle hook 7件、Traceary MCP server 1件、共有の memory/session skill 3件を同梱します。`scripts/install-grok-plugin.sh` で導入し、`traceary doctor --client grok --json` で hook 契約、trust、パッケージ内容、バージョン一致を確認します。 |
| Kimi Code | `integrations/kimi-plugin/` | v0.29.0 でサポート。ネイティブ plugin は 1 つの `kimi.plugin.json` manifest に、実環境で検証した lifecycle hook 10 件（session / prompt / tool audit（失敗含む）/ transcript / compact marker / subagent）、Traceary MCP server 1 件、共有の memory/session skill 3 件を宣言します。`scripts/install-kimi-plugin.sh` で導入し、`traceary doctor --client kimi --json` で確認します。 |

## host 別ガイド

- [Claude Code plugin](./claude-plugin.ja.md)
- [Codex plugin](./codex-plugin.ja.md)
- [Gemini CLI extension（レガシー）](./gemini-extension.ja.md)
- [Antigravity hooks / plugin](./antigravity.ja.md)
- [Grok Build plugin](./grok-plugin.ja.md)
- [Kimi Code plugin](./kimi.ja.md)
- [Anthropic native memory tool (experimental)](./anthropic-memory-tool.ja.md)

## 検証と smoke test

このリポジトリでは、ネイティブ連携パッケージ向けに 2 段階の検証を用意しています。

1. `go run ./cmd/repo-tooling integrations verify` による構造検証
2. `./scripts/smoke_test_integrations.sh` によるローカル smoke test

smoke test では、各 host の導入経路に合わせて次を確認します。Gemini の link/list flow は browser 認証 prompt を開くことがあるため、headless な release prep では既定で skip し、必要なときだけ明示的に opt-in します。

- Claude Code: marketplace validate と一時 home での install
- Gemini CLI: `TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1` を設定したときだけ、認証済み CLI で extension validate と一時 home での link を確認
- Codex: plugin manifest の構造検証（`hooks: "./hooks.json"` / commands / skills）と、削除済み `traceary integration` サブツリーが unknown command として失敗することの probe（v0.25.0, #1266）
- Grok Build: ネイティブパッケージの検証と、一時 home での導入・内容確認・削除
