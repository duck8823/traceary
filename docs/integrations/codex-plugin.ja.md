# Codex plugin

[English](./codex-plugin.md)

Traceary の Codex 向け plugin は `plugins/traceary/` にあり、Codex CLI 公式の `/plugins` flow に乗せて使えます。
MCP server / slash command / session-history skill / session・prompt・transcript・audit の hook は、公式 flow で plugin を install した時点で自動配線されます。

## Codex 公式 /plugins flow で入れる (primary)

1. 先に Traceary CLI を入れます（agent hook が `traceary` バイナリを呼ぶため）。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. このリポジトリを取得します。リポジトリは `.agents/plugins/marketplace.json` にローカル marketplace を持ち、`plugins/traceary/` に plugin 本体を持っています。

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
```

3. リポジトリ内で Codex を起動し、同梱 marketplace を発見させます。

```sh
cd ~/src/traceary
codex
```

4. Codex 内で `/plugins` を開き、marketplace として `Traceary Plugins` を選び、`Traceary` plugin を install します。Codex が plugin を管理下の cache に展開し、manifest に記述された hook を登録します。

5. 新しい thread を開いて確認します。

```sh
traceary doctor --client codex --json
```

## 公式 flow が自動で組み込むもの

- `traceary mcp-server` を呼ぶ `traceary` MCP server
- `SessionStart`, `UserPromptSubmit`, `Stop`（session 終了 + transcript）, `PostToolUse` hook（`plugins/traceary/hooks.json` で宣言、manifest から参照）
- slash command: `/traceary:help`, `/traceary:doctor`
- 文脈に効く skill: `traceary-session-history` / `traceary-memory-review` / `traceary-memory-remember`。`traceary-memory-review` は review 意図の発話 (「Traceary inbox」「review memory candidates」「session recap」など) で発火し inbox の curate を案内、`traceary-memory-remember` は明示 write 発話 (「覚えておいて」「remember that」など) のみで発火します。旧 `traceary-memory-capture` は deprecated stub として残存（v0.12 で削除予定）。

## 更新

リポジトリを更新して、次回 Codex が `/plugins` をリフレッシュした時に新しいバージョンを取り込みます。

```sh
cd ~/src/traceary
git pull --ff-only
```

plugin を一度外して入れ直したい場合は `/plugins` 画面から再インストールできます。

## Doctor と smoke test

プライマリなランタイム確認:

```sh
traceary doctor --client codex --json
```

maintainer 向けの構造確認（plugin manifest や hook、marketplace を変更したとき）:

```sh
python3 scripts/verify_integrations.py
```

## 旧 install（非推奨。互換目的のみ）

`traceary integration codex install` は旧リリースから移行するユーザー向けの過渡的な経路として残してあります。stderr に deprecation banner を出し、**v0.8.0 以降のタイミングで削除予定**です。

旧コマンドは従来どおり全工程を手動で行います — `~/.agents/plugins` に plugin をコピーし、`~/.codex/plugins/cache/local-traceary-plugins/traceary/local` にアクティブ cache を展開し、`~/.codex/config.toml` で plugin を有効化し、`~/.codex/hooks.json` に Traceary hook をマージします。stdout を parse している既存スクリプトはそのまま動作します（stderr の banner だけが新規）。

### 旧 install からの移行手順

1. 上記の公式 `/plugins` install を実行します。
2. Codex 側で plugin が有効になったら、旧状態を 1 回だけ片付けます。

```sh
cd ~/src/traceary
traceary integration codex uninstall
```

uninstall 側はこの cleanup 用途のために意図的に残してあります。Traceary が入れた plugin cache、`~/.codex/config.toml` の `[plugins."traceary@local-traceary-plugins"]` エントリ、`~/.codex/hooks.json` の Traceary 管理下の hook だけを取り除き、ユーザー自身の hook や `[features].codex_hooks` フラグはそのまま残します。これで両経路が同時に有効な期間でも prompt / audit の二重記録を避けられます。

`traceary integration codex install` と `traceary integration codex uninstall` は、どちらも v0.7.x の window 終了後に削除予定です。そのリリースライン内で移行を終わらせてください。
