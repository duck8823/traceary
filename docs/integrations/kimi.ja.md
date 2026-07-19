# Kimi Code プラグイン

[English](./kimi.md)

Traceary v0.29.0 で native な Kimi Code 連携を追加しました。[`integrations/kimi-plugin/`](../../integrations/kimi-plugin/) のパッケージは、1 つの `kimi.plugin.json` manifest を通じて、live 検証済みの 10 の lifecycle hook、1 つのローカル Traceary MCP server、3 つの共有 memory/session skill をインストールします。記録される hook event は `client=hook` / `agent=kimi` になります。

## サポートするカバレッジ

live 検証済みの Kimi Code 0.27.0 の contract が対象です（[機械可読な contract](../hooks/host-contract.json)）。

| Kimi Code event | Traceary の動作 |
| --- | --- |
| `SessionStart` | native の Kimi session を開始します。`source=resume` は同一 session id で再発火し、冪等に記録されます |
| `SessionEnd` | session を終了します（`reason=exit`） |
| `UserPromptSubmit` | user prompt を記録します（content block 配列をテキスト化） |
| `PreToolUse`（`matcher = "Agent"`） | subagent の子 session を相関付けて開始します |
| `PostToolUse` | 完了した command audit を 1 件記録します（`tool_output` を捕捉） |
| `PostToolUseFailure` | 失敗した audit を `error{code,message,retryable}` を平坦化した詳細とともに記録します |
| `Stop` | assistant の transcript を main agent の session wire log（Kimi 自身の `~/.kimi-code/session_index.jsonl` → session の `agents/main/wire.jsonl`）から best-effort で記録します。turn 境界であり、session 終了ではありません |
| `SubagentStop` | active な子 session を終了します（latest-active fallback。Claude と同じ semantics） |
| `PreCompact` / `PostCompact` | compact marker を記録します（`trigger` = auto/manual）。Kimi は token 数を公開しますが summary 本文はありません |

`Notification`、`Interrupt`、`StopFailure`、`PermissionRequest`、`PermissionResult` は配線していません。live 未観測、または Traceary の lifecycle に対応しないためです。専用の `SubagentStart` event も配線していません — 相関 ID を持たないため、代わりに Agent tool の `PreToolUse` を開始信号にしています。フィールド単位の状態は [host coverage matrix](../hooks/host-coverage.md) を参照してください。

## インストール

1. Traceary CLI をインストールし、`traceary` が `PATH` 上にあることを確認します（plugin の hook と MCP server がこれを呼び出します）。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
```

2. 対応する Traceary の release tag の checkout から、plugin をインストールします（packaged hook の互換性を保つため、Traceary CLI と一致する tag を指定してください）。

```sh
git clone --depth 1 --branch v0.29.0 https://github.com/duck8823/traceary
cd traceary
./scripts/install-kimi-plugin.sh
```

インストーラは公式の local install を再現します。`~/.kimi-code/plugins/managed/` 配下に generation directory としてパッケージを配置し、`traceary` symlink を atomic に切り替え、`plugins/installed.json` の record を既存の `enabled` 状態を保持したまま更新します。再実行は冪等です。

あるいは Kimi Code 公式の flow も使えます。Kimi Code 内で `/plugins install <path-or-url>` を実行し、対応する tag の checkout 内の `integrations/kimi-plugin/` ディレクトリを指定してください（manifest はこのサブディレクトリにあり、リポジトリ直下ではありません）。

3. 新しい session を開始（または `/plugins reload`）してから、検証します。

```sh
traceary doctor --client kimi
```

正常なインストールであれば、`kimi-cli` / `kimi-plugin` / `kimi-hooks` / `kimi-mcp` / `kimi-skills` がすべて PASS します。Kimi は opt-in の doctor client なので、通常の `traceary doctor`（claude/codex/gemini）は変わりません。

## 手動での hook 設定（代替）

plugin をインストールしたくない場合は、生成された TOML rule を `~/.kimi-code/config.toml` に自分で追記できます。

```sh
traceary hooks print --client kimi
```

`traceary hooks install --client kimi` は意図的に fail-closed です。Traceary はユーザーの設定に TOML をマージしません。

## トラブルシューティング

- **hook は発火するが何も記録されない**: plugin は `PATH` 上の `traceary` を呼び出します。kimi 対応の Traceary バイナリ（v0.29.0 以降）が `PATH` の先頭にあることを確認してください。`traceary doctor` が PATH の不一致を報告します。
- **transcript event が出ない**: transcript は Kimi 自身の `~/.kimi-code/session_index.jsonl` から解決した main agent の wire log から best-effort で復元します。index にエントリがない古い session は設計どおり静かにスキップされます。
- **plugin はインストール済み表示だが hook が発火しない**: `~/.kimi-code/plugins/installed.json` の `traceary` エントリが `"enabled": true` かつ `"state": "ok"` であることを確認し、`/plugins reload` を実行してください。
- **アップグレード後の不整合**: 管理下の plugin version が実行中の Traceary バイナリと一致しない場合、`traceary doctor --client kimi` が警告します。対応する release tag から `scripts/install-kimi-plugin.sh` で再インストールしてください。
