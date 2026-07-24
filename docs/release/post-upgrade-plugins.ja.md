# バイナリ更新後の plugin 更新チェックリスト

[English](./post-upgrade-plugins.md)

#1361・親カット #1360 の一部です。

Homebrew / `go install` / release binary の更新では **Traceary CLI バイナリだけ**が更新されます。ホスト plugin パッケージは各ホストの cache / install root にあり、`brew upgrade traceary` では **自動更新されません**。

release 済みバイナリを更新するたびに、次を実行してください。

1. `traceary -v` でバイナリを確認する。
2. 下表に従い、導入済みホスト package をすべて更新する。
3. doctor の収集前に、そのホストの有効化を完了する。
4. 本文を読まない release QA gate を実行する。skip を付けていないホストの plugin-version はすべて `pass` でなければなりません。Antigravity だけは、一方の copy が pass のときに限り、不完全な dual-path twin の `skip` を追加で許可します。

   ```sh
   ./scripts/verify-post-upgrade-plugin-refresh.sh \
     --skip gemini='この機械にはレガシー extension を導入していない' \
     --skip antigravity='この機械では Antigravity を利用しない' \
     --skip grok='この機械では Grok Build を利用しない' \
     --skip kimi='この機械では Kimi Code を利用しない'
   ```

   例の skip は、この機械に意図的に存在しないホストに置き換えてください。導入済みの古い package を隠すために skip を使ってはいけません。この gate は `traceary doctor --client <host> --json --warnings-ok` を実行し、各 `*-plugin-version` の check 名と status だけを読みます。`warn` または `fail` なら失敗します。prompt・transcript・command・database event の本文は読みません。

## ホスト別の更新・有効化・検証マトリクス

| Host | 更新 | 有効化 | version 検証 | 許可する skip |
|---|---|---|---|---|
| Claude Code | Claude Code 内で `claude plugins update <Traceary marketplace key>`。正確な key は `traceary doctor --client claude --json` の `FixCommand` を使います。 | 更新済み package を読み込むため、Claude Code を再起動するか新しい process を開始します。 | `traceary doctor --client claude --json` の `claude-plugin-version` が `pass`。 | Claude Code / Traceary package を意図的に導入していない場合だけ `--skip claude='理由'`。 |
| Codex | `traceary -v` と一致する checkout の `plugins/traceary/` から再導入します。Codex plugin 文書も参照してください。 | `/plugins` で refresh / reinstall してから、新しい Codex session を開始します。 | `traceary doctor --client codex --json` の `codex-plugin-version` が `pass`。 | Codex / Traceary package を意図的に導入していない場合だけ `--skip codex='理由'`。 |
| Gemini CLI（レガシー extension） | `gemini extensions update traceary`。 | Gemini CLI を再起動します。 | `traceary doctor --client gemini --json` の `gemini-plugin-version` が `pass`。 | レガシー extension を意図的に導入していない場合だけ `--skip gemini='理由'`。 |
| Antigravity | 下記の安全な dual path 手順を行ってから、`agy plugin install integrations/antigravity-plugin`。 | Antigravity を終了して開き直すか、新しい CLI session を開始します。 | `traceary doctor --client antigravity --json` のすべての `antigravity-plugin-version` が `pass`。一方が pass で、もう一方が version のない不完全 twin なら、後者の `skip` は許可されます。 | Antigravity / Traceary package を意図的に導入していない場合だけ `--skip antigravity='理由'`。 |
| Grok Build | `./scripts/install-grok-plugin.sh`。 | Grok Build を再起動するか、新しい session を開始します。 | `traceary doctor --client grok --json` の `grok-plugin-version` が `pass`。 | Grok Build / Traceary package を意図的に導入していない場合だけ `--skip grok='理由'`。 |
| Kimi Code | `./scripts/install-kimi-plugin.sh`。installer は新しい generation を stage し、managed `traceary` symlink を atomic に切り替え、install record を保持します。 | `/plugins reload` を実行するか、**新しい Kimi session を開始**します。 | `traceary doctor --client kimi --json` の `kimi-plugin-version` と native `kimi-plugin` が健全。 | Kimi Code / Traceary package を意図的に導入していない場合だけ `--skip kimi='理由'`。 |

doctor が正確な非対話コマンドを `FixCommand` に出す場合は、そちらを優先してください。ホスト CLI のフラグを推測で追加してはいけません。

## Antigravity の stale dual path 修復

Antigravity では、独立して materialize された次の二つの package が残ることがあります。

- `~/.gemini/config/plugins/traceary`
- `~/.gemini/antigravity-cli/plugins/traceary`

以前 Gemini CLI 経由で取り込んだ copy は、古い top-level `{"hooks": ...}` 形式のまま残ることがあります。この場合、`agy plugin install` は成功と表示しても stale CLI-path package を置き換えません。先に `traceary doctor --client antigravity --json` を実行してください。stale または version 不一致の path が報告されたときは、Antigravity を停止し、**失敗している copy だけを quarantine** してから再導入します。doctor が version 一致と報告した copy を削除してはいけません。

```sh
# 対応する Traceary release checkout で実行します。PATH は doctor が
# 報告した失敗 path に置き換えます。move は元に戻せます。
stale_path="$HOME/.gemini/antigravity-cli/plugins/traceary"
quarantine_path="${stale_path}.stale.$(date +%Y%m%d%H%M%S)"
test ! -e "$quarantine_path"
mv "$stale_path" "$quarantine_path"
agy plugin install integrations/antigravity-plugin
traceary doctor --client antigravity --json
```

もう一方が健全で、不完全な残存 twin に `version` がない場合、doctor は twin を `skip` と報告します。これは自動で許可される唯一の dual-path skip です。健全な経路を確認してから未使用の不完全 directory を quarantine し、doctor を再実行してください。CLI package が不要なら direct hook も代替手段です。

```sh
traceary hooks install --client antigravity --upgrade
```

## Homebrew の注意

`brew upgrade traceary` は host plugin cache を書き換えません。dogfood 機では plugin refresh を必須の更新後手順として扱ってください。

## 関連

- [リリースガイド](./README.ja.md)
- [Integrations 概要](../integrations/README.ja.md)
