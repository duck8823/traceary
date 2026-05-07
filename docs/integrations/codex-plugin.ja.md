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
- 文脈に効く skill: `traceary-session-history` / `traceary-memory-review` / `traceary-memory-remember`。`traceary-memory-review` は review 意図の発話 (「Traceary inbox」「review memory candidates」「session recap」など) で発火し inbox の curate を案内、`traceary-memory-remember` は明示 write 発話 (「覚えておいて」「remember that」など) のみで発火します。

## Memory activation strategy

Codex は v0.12 で Traceary の full host-native activation を備えた最初の host でした。v0.13.0 では同じ activation 契約を Claude / Gemini に拡張し、これらは二ファイル import-stub 戦略を採用しています。Codex は single-file target のままです。accepted memory は引き続き Traceary の SQLite store を source of truth とし、activation command は Codex memory target（既定は `~/.codex/memories/traceary.md`）へ Traceary 管理ブロックだけを書きます。

```sh
traceary memory activate --target codex --status
traceary memory activate --target codex --dry-run --diff
traceary memory activate --target codex --apply
traceary doctor --client codex --json
```

apply path は必要に応じて target directory/file を作成し、管理ブロック外の user-authored content を保持します。accepted memory set が変わっていなければ冪等に no-op となり、新しい marker version の管理ブロックは上書きしません。完全な安全契約は [host-native memory activation ADR](../architecture/host-native-memory-activation.ja.md) を、host 横断比較や `invalid` からの復旧は [durable memory ガイド](../memory/README.ja.md#ホスト別-activation-strategy)を参照してください。

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

## 旧 install の削除 (v0.14.0)

`traceary integration codex install` は **v0.14.0** で削除されました (#920)。実行可能な install path としては機能しません。新規 install は上記の公式 `/plugins` flow を使ってください。旧コマンドを実行すると、v0.14.0 での削除と Codex 公式の `/plugins` flow を案内する usage error を返して終了します。

### cleanup 専用 uninstall (hidden、v0.15 で削除予定)

`traceary integration codex uninstall` は、削除済みの install から移行するユーザー向けの cleanup 専用 command として hidden で残してあります。`traceary integration codex --help` には表示されないため新規ユーザーには見えませんが、cleanup スクリプトからは引き続き実行できます。

```sh
cd ~/src/traceary
traceary integration codex uninstall
```

cleanup 専用 uninstall は、Traceary が入れた plugin cache、`~/.codex/config.toml` の `[plugins."traceary@local-traceary-plugins"]` エントリ、`~/.codex/hooks.json` の Traceary 管理下の hook を取り除きます。ユーザー自身の hook や `[features].codex_hooks` フラグはそのまま残ります。

hidden な uninstall command は **v0.15** で削除予定です。公式 `/plugins` install に移行した後に 1 回だけ実行して prompt / audit の二重記録を解消し、自動化からは呼び出しを外してください。
