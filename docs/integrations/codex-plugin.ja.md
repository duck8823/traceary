# Codex plugin

[English](./codex-plugin.md)

Traceary の Codex 向け plugin は `plugins/traceary/` にあり、Codex CLI 公式の `/plugins` flow に乗せて使えます。
MCP server / slash command / session-history skill は、公式 flow で plugin を install した時点で自動配線されます。hook 登録は条件付きで、Codex 側の `plugin_hooks` feature が stable + 有効なビルドでのみ自動配線されます。`codex features list` で `plugin_hooks` が `under development` のままだったり、`~/.codex/config.toml` に `[features].plugin_hooks = false` が明示されているビルドでは、plugin 配下の hook manifest は `~/.codex/hooks.json` に展開されないため、手動 install による fallback が必要です。詳細は下記の **Hook fallback (plugin_hooks が利用できない環境向け)** セクションを参照してください。

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
- `SessionStart`, `UserPromptSubmit`, `Stop`（turn 境界の transcript。session 終了ではない — #1170）, `PostToolUse` hook（`plugins/traceary/hooks.json` で宣言、manifest から参照） — **Codex 側の `plugin_hooks` feature が有効なビルドに限る**。それ以外は下記 **Hook fallback (plugin_hooks が利用できない環境向け)** セクションを参照
- slash command: `/traceary:help`, `/traceary:doctor`
- 文脈に効く skill: `traceary-session-history` / `traceary-memory-review` / `traceary-memory-remember`。`traceary-memory-review` は review 意図の発話 (「Traceary inbox」「review memory candidates」「session recap」など) で発火し inbox の curate を案内、`traceary-memory-remember` は明示 write 発話 (「覚えておいて」「remember that」など) のみで発火します。

## Hook fallback (plugin_hooks が利用できない環境向け)

Codex 側の plugin-managed hook サポートは `plugin_hooks` feature flag に紐付いており、ビルドによってはまだ `under development` のままです。feature が利用できない環境では、`/plugins` で Traceary plugin が enabled になっていても Codex は plugin manifest の hook 宣言を `~/.codex/hooks.json` に展開しないため、`traceary tail` や durable-memory extraction に Codex の event が一切流れません。

診断手順:

```sh
codex features list                          # plugin_hooks は stable + on か
cat ~/.codex/config.toml                     # [features].plugin_hooks = false が無いか
traceary doctor --client codex --json        # plugin_hooks fallback 用の actionable warning が表示される
```

`plugin_hooks` が利用できない場合は、event を捕捉するために hook を手動 install してください:

```sh
traceary hooks install --client codex --upgrade --traceary-bin "$(command -v traceary)"
traceary doctor --client codex --json
```

fallback は `~/.codex/hooks.json` に Traceary 管理のエントリ (`traceary-session-start` / `traceary-prompt` / `traceary-transcript` / `traceary-session-stop` / `traceary-audit`) を直接書き込みます。Traceary 以外のエントリは保持されます。

### 二重記録の注意点

将来 Codex 側で `plugin_hooks` が有効化されると、plugin manifest 経由の hook が自動で発火するようになります。このとき `~/.codex/hooks.json` に fallback で書き込まれた手動エントリがそのまま残っていると、**session / prompt / transcript / audit の各 event が二重に記録されます**。fallback を経由した install で plugin-managed hook を改めて有効化する前に、手動エントリを削除してください:

```sh
# ~/.codex/hooks.json を開き、fallback が追加した次の名前付きエントリを削除:
#   traceary-session-start / traceary-prompt / traceary-transcript /
#   traceary-session-stop / traceary-audit
```

cleanup 後に `traceary doctor --client codex --json` を再実行し、登録経路が一つだけになっていることを確認してください。

## Memory activation strategy

Codex は v0.12 で Traceary の full host-native activation を備えた最初の host でした。v0.13.0 では同じ activation 契約を Claude / Gemini に拡張し、これらは二ファイル import-stub 戦略を採用しています。Codex は single-file target のままです。accepted memory は引き続き Traceary の SQLite store を source of truth とし、activation command は Codex memory target（既定は `~/.codex/memories/traceary.md`）へ Traceary 管理ブロックだけを書きます。

```sh
traceary memory admin activate --target codex --status
traceary memory admin activate --target codex --dry-run --diff
traceary memory admin activate --target codex --apply
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
go run ./cmd/repo-tooling integrations verify
```

## 旧 install/uninstall の削除 (v0.14.0, v0.15.0)

`traceary integration codex install` は **v0.14.0** で削除されました (#920)。実行可能な install path としては機能しません。新規 install は上記の公式 `/plugins` flow を使ってください。旧コマンドを実行すると、v0.14.0 での削除と Codex 公式の `/plugins` flow を案内する usage error を返して終了します。

`traceary integration codex uninstall` は **v0.15.0** で削除されました (#957)。install / uninstall いずれも hidden な stub 扱いとなり、実行すると Codex 公式の `/plugins` flow を案内する usage error で非ゼロ終了します。`traceary integration codex --help` にも uninstall は表示されません。

### 旧 install を残した環境向けの手動 cleanup

v0.14 以前の `traceary integration codex install` 経路で配置した状態が残っている場合、まず Codex 公式の `/plugins` flow で Traceary plugin を uninstall してください（リポジトリ内で `codex` を起動 → `/plugins` → `Traceary` を uninstall）。Traceary が手動で配置していた残存ファイルは、次の手順で取り除いてください。

```sh
# 旧 active plugin cache を削除
rm -rf ~/.codex/plugins/cache/local-traceary-plugins/traceary

# 旧 marketplace copy を削除
rm -rf ~/.agents/plugins/plugins/traceary

# ~/.agents/plugins/marketplace.json から、source path が "./plugins/traceary" の
# "traceary" entry を削除。この local marketplace が Traceary だけを含んでいた場合は、
# copy 削除後に marketplace.json ごと削除してもかまいません。
# ~/.codex/config.toml から [plugins."traceary@local-traceary-plugins"] テーブルを削除
# ~/.codex/hooks.json から名前付きエントリ "traceary-session-start" / "traceary-session-stop" /
# "traceary-prompt" / "traceary-audit" を削除。`[features].codex_hooks` フラグは他の hook workflow の
# ために残します。
```

cleanup 後は上記の公式 `/plugins` flow で install し直し、plugin lifecycle は Codex 自身に委ねてください。
