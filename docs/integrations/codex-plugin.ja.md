# Codex plugin

[English](./codex-plugin.md)

Traceary の Codex 向け plugin は `plugins/traceary/` にあり、Codex CLI 公式の `/plugins` flow に乗せて使えます。
MCP server / slash command / session-history skill は、公式 flow で plugin を install した時点で自動配線されます。plugin hook には追加の安全確認があります。Codex は non-managed hook の現在の定義をユーザーが確認して trust するまで実行しません。install 後と、hook 定義が変わる plugin update 後に `/hooks` を開き、Traceary の entry を確認して trust してください。`traceary doctor --client codex` は Codex が判定した有効な trust 状態を検査し、untrusted・変更済み・無効な hook を警告します。

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

4. Codex 内で `/plugins` を開き、marketplace として `Traceary Plugins` を選び、`Traceary` plugin を install します。Codex が plugin を管理下の cache に展開し、manifest に記述された hook を発見します。

5. `/hooks` を開き、Traceary plugin の hook command を確認して、現在の定義を trust します。install だけでは plugin hook は trust されず、定義が変わると再確認が必要です。

6. 新しい thread を開いて確認します。

```sh
traceary doctor --client codex --json
```

## 公式 flow が自動で組み込むもの

- `traceary mcp-server` を呼ぶ `traceary` MCP server
- `SessionStart`, `UserPromptSubmit`, `Stop`（turn 境界の transcript。session 終了ではない — #1170）, `PostToolUse` hook（`plugins/traceary/hooks.json` で宣言、manifest から参照）。Codex は現在の定義が `/hooks` で trust された後にだけ実行します。
- slash command: `/traceary:help`, `/traceary:doctor`
- 文脈に効く skill: `traceary-session-history` / `traceary-memory-review` / `traceary-memory-remember`。`traceary-memory-review` は review 意図の発話 (「Traceary inbox」「review memory candidates」「session recap」など) で発火し inbox の curate を案内、`traceary-memory-remember` は明示 write 発話 (「覚えておいて」「remember that」など) のみで発火します。

## Hook trust 診断と legacy fallback

現在の Codex は `/hooks` で有効な hook 状態を示します。`trusted` な hook は実行され、`untrusted`・変更済み (`modified`)・無効な hook は実行されません。Traceary は Codex 内部の hash algorithm を複製せず、現在の定義との比較を Codex に委譲します。

診断手順:

```sh
traceary doctor --client codex --json        # 有効な plugin hook trust を検査
codex                                        # /hooks を開いて Traceary entry を確認
```

古い Codex build が plugin hook 状態を提示できない場合、doctor は pass とせず未確認として報告します。まず Codex を更新してください。plugin-managed hook を本当に読み込めない環境では、手動登録を互換 fallback として使えます。

```sh
traceary hooks install --client codex --upgrade --traceary-bin "$(command -v traceary)"
traceary doctor --client codex --json
```

fallback は `~/.codex/hooks.json` に Traceary 管理のエントリ (`traceary-session-start` / `traceary-prompt` / `traceary-transcript` / `traceary-session-stop` / `traceary-audit`) を直接書き込みます。Traceary 以外のエントリは保持されます。

### 二重記録の注意点

trust 済みの plugin hook と `~/.codex/hooks.json` の手動 entry が同時に有効だと、**session / prompt / transcript / audit の各 event が二重に記録されます**。doctor で古い手動経路を検出・削除してください:

```sh
traceary doctor --client codex --json
traceary doctor --fix --dry-run --client codex
traceary doctor --fix --client codex
```

doctor が cleanup を提示するのは、Codex が現在の Traceary plugin hook 定義を trusted と報告した場合だけです。名前付きの Traceary 管理エントリ (`traceary-session-start` / `traceary-prompt` / `traceary-transcript` / `traceary-session-stop` / `traceary-audit`) だけを削除し、Traceary 以外の hook と top-level field は保持します。trust が未確認・untrusted・変更済み・無効な場合は手動 fallback を残します。

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
