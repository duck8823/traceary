# ネイティブ連携パッケージ

[English](./README.md)

Traceary は、Claude Code / Codex / Gemini CLI 向けにネイティブ連携パッケージを用意しています。

これらのパッケージは、次の共通ランタイム契約でそろえています。

- `traceary` CLI が `PATH` 上にあることを前提にする
- MCP は共通で `traceary mcp-server` を起動する
- セッション境界と shell command audit は同梱 hook で記録する
- SQLite ストア、CLI フラグ、`traceary doctor` の流れは host ごとに分けない

## 共通の機能

| 機能 | 共通の振る舞い |
| --- | --- |
| MCP server | `traceary mcp-server` で Traceary の read/write tools を公開する |
| session hook | session start/end（Codex は `Stop`）を Traceary event として記録する |
| shell audit hook | `traceary audit` を通して shell command 実行を記録する |
| doctor flow | `traceary doctor --client <host>` を共通のトラブルシュート入口にする |
| versioning | integration package は Traceary の release と同じ version で公開する |

## host ごとの package root

| Host | Package root | 実際の配置先 |
| --- | --- | --- |
| Claude Code | `integrations/claude-plugin/` | `.claude-plugin/marketplace.json` を基点にした Claude marketplace |
| Codex | `plugins/traceary/` | Codex CLI 公式の `/plugins` flow を使い、リポジトリ内の marketplace `.agents/plugins/marketplace.json` から install。plugin manifest で同梱 `hooks.json` を参照するため session / prompt / audit hook が自動配線される。`traceary integration codex install` helper は v0.14.0 で、cleanup 専用 `traceary integration codex uninstall` は v0.15.0 で廃止。いずれも非表示の stub になり、Codex 公式の `/plugins` flow と [docs/integrations/codex-plugin.ja.md](./codex-plugin.ja.md) の手動 cleanup 手順を案内するのみ。 |
| Gemini CLI | `integrations/gemini-extension/` | `gemini-extension.json` を root にした Gemini extension archive |

## host 別ガイド

- [Claude Code plugin](./claude-plugin.ja.md)
- [Codex plugin](./codex-plugin.ja.md)
- [Gemini CLI extension](./gemini-extension.ja.md)
- [Anthropic native memory tool (experimental)](./anthropic-memory-tool.ja.md)

## 検証と smoke test

このリポジトリでは、ネイティブ連携パッケージ向けに 2 段階の検証を用意しています。

1. `go run ./cmd/repo-tooling integrations verify` による構造検証
2. `./scripts/smoke_test_integrations.sh` によるローカル smoke test

smoke test では、各 host の導入経路に合わせて次を確認します。Gemini の link/list flow は browser 認証 prompt を開くことがあるため、headless な release prep では既定で skip し、必要なときだけ明示的に opt-in します。

- Claude Code: marketplace validate と一時 home での install
- Gemini CLI: `TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1` を設定したときだけ、認証済み CLI で extension validate と一時 home での link を確認
- Codex: plugin manifest の構造検証（`hooks: "./hooks.json"` / commands / skills）と、`traceary integration codex install`（v0.14.0 廃止）/ `traceary integration codex uninstall`（v0.15.0 廃止）の retired-stub probe（移行ヒントが古くならないように smoke で固定）
