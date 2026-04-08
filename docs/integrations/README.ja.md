# ネイティブ連携パッケージ

[English](./README.md)

Traceary v0.1.12 では、Claude Code / Codex / Gemini CLI 向けのネイティブ連携パッケージを追加します。

これらのパッケージは、次の共通ランタイム契約で揃えています。

- `traceary` CLI が `PATH` 上にあることを前提にする
- MCP は共通で `traceary mcp-server` を起動する
- session 境界と shell command audit は同梱 hook で記録する
- SQLite store、CLI flag、`traceary doctor` の流れは host ごとに分岐させない

## 共通の機能面

| 機能 | 共通の振る舞い |
| --- | --- |
| MCP server | `traceary mcp-server` で Traceary の read/write tools を公開する |
| session hook | session start/end（Codex は `Stop`）を Traceary event として記録する |
| shell audit hook | `traceary audit` を通して shell command 実行を記録する |
| doctor flow | `traceary doctor --client <host>` を共通のトラブルシュート入口にする |
| versioning | package manifest の version は Traceary の release tag に揃える |

## host ごとの package root

| Host | Package root | 配布の基点 |
| --- | --- | --- |
| Claude Code | `integrations/claude-plugin/` | `.claude-plugin/marketplace.json` を基点にした Claude marketplace |
| Codex | `plugins/traceary/` | `.agents/plugins/marketplace.json` を基点にした Codex marketplace 形式 |
| Gemini CLI | `integrations/gemini-extension/` | `gemini-extension.json` を root にした Gemini extension archive |

## host 別ガイド

- [Claude Code plugin](./claude-plugin.ja.md)
- [Codex plugin](./codex-plugin.ja.md)
- [Gemini CLI extension](./gemini-extension.ja.md)

## 検証と smoke test

この repository では、native package 向けに 2 層の検証を持たせます。

1. `python3 scripts/verify_integrations.py` による構造検証
2. `./scripts/smoke_test_integrations.sh` によるローカル smoke test

smoke test では、現時点で各 host が公開している install surface に合わせて確認します。

- Claude Code: marketplace validate と一時 home での install
- Gemini CLI: extension validate と一時 home での link
- Codex: marketplace / plugin manifest の構造検証を基本にし、plugin-enabled build がある場合だけ runtime probe を追加実行する
