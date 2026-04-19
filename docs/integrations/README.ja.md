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
| Codex | `plugins/traceary/` | Codex CLI 公式の `/plugins` flow を使い、リポジトリ内の marketplace `.agents/plugins/marketplace.json` から install。plugin manifest で同梱 `hooks.json` を参照するため session / prompt / audit hook が自動配線される。`traceary integration codex install` は非推奨の互換経路として残る。 |
| Gemini CLI | `integrations/gemini-extension/` | `gemini-extension.json` を root にした Gemini extension archive |

## host 別ガイド

- [Claude Code plugin](./claude-plugin.ja.md)
- [Codex plugin](./codex-plugin.ja.md)
- [Gemini CLI extension](./gemini-extension.ja.md)

## 検証と smoke test

このリポジトリでは、ネイティブ連携パッケージ向けに 2 段階の検証を用意しています。

1. `python3 scripts/verify_integrations.py` による構造検証
2. `./scripts/smoke_test_integrations.sh` によるローカル smoke test

smoke test では、各 host の導入経路に合わせて次を確認します。

- Claude Code: marketplace validate と一時 home での install
- Gemini CLI: extension validate と一時 home での link
- Codex: plugin manifest の構造検証（`hooks: "./hooks.json"` / commands / skills）と、非推奨 `traceary integration codex install/uninstall` 互換経路の smoke（v0.7.x の window 中は移行経路として残す）
