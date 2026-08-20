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

   例の skip は、この機械に意図的に存在しないホストに置き換えてください。導入済みの古い package を隠すために skip を使ってはいけません。この gate は `traceary doctor --client <host> --json --warnings-ok` を実行し、各ホストの canonical plugin check 名と status だけを読みます（Grok は `grok-plugin`、他ホストは `*-plugin-version`）。`warn` または `fail` なら失敗します。prompt・transcript・command・database event の本文は出力しません。

   この gate は live の既定 store path に対して有効です。store が bounded
   doctor の閾値（2 GiB）以上でも変わりません。host package identity
   ファミリー（`*-plugin-version` と native な Grok/Kimi plugin check）は
   host manifest・host plugin cache・host CLI probe だけを読むため、doctor
   が `mode: "metadata_only_large_store"` を返しても report に残ります。
   store が大容量であることは、ホストを `--skip` する理由には**なりません**。
   `--skip` はこの機械に意図的に導入していないホスト専用です。

## ホスト別の更新・有効化・検証マトリクス

| Host | 更新 | 有効化 | version 検証 | 許可する skip |
|---|---|---|---|---|
| Claude Code | headless: `claude plugin marketplace update traceary-plugins` のあと `claude plugin update traceary@traceary-plugins`（user scope）と `--scope local`。短い名前 `claude plugin update traceary` は "Plugin not found" になる。対話の `/plugin update traceary` は従来どおり有効。更新後は再起動。再開した session は完全に再起動するまで古い snapshot hook を使い続けることがある。 | 更新済み package を読み込むため、Claude Code を再起動するか新しい process を開始します。 | `traceary doctor --client claude --json` の `claude-plugin-version` が `pass`。 | Claude Code / Traceary package を意図的に導入していない場合だけ `--skip claude='理由'`。 |
| Codex | `codex plugin marketplace list` で root を確認。Git clone: その clone で `traceary -v` と一致する tag を `git checkout`。local-path（checkout 自体が marketplace root。manifest は `.agents/plugins/marketplace.json`）: `git -C <marketplace-root> checkout` で同じ tag。そのあと `codex plugin add traceary@traceary-marketplace`。local-path marketplace では `codex plugin marketplace upgrade` は "No configured Git marketplaces" になるので使わない。対話の `/plugins` は従来どおり有効。 | add（または `/plugins` refresh）のあと新しい Codex session を開始します。 | `traceary doctor --client codex --json` の `codex-plugin-version` が `pass`。 | Codex / Traceary package を意図的に導入していない場合だけ `--skip codex='理由'`。 |
| Gemini CLI（レガシー extension） | `./scripts/install-gemini-extension.sh`（uninstall + `install --consent`、tag pin）。その後 managed hook generation を更新（下記参照）。 | Gemini CLI を再起動します。 | `traceary doctor --client gemini --json` の `gemini-plugin-version` と `gemini-config` がどちらも `pass`。 | レガシー extension を意図的に導入していない場合だけ `--skip gemini='理由'`。 |
| Antigravity | `traceary -v` と一致する checkout から `rsync -a --delete integrations/antigravity-plugin/` を `~/.gemini/config/plugins/traceary/`（と、あればレガシー `~/.gemini/antigravity-cli/plugins/traceary/`）へ、または `agy plugin install integrations/antigravity-plugin`。doctor がまだ stale twin を出す場合は下記の dual path 手順。 | Antigravity を終了して開き直すか、新しい CLI session を開始します。 | `traceary doctor --client antigravity --json` のすべての `antigravity-plugin-version` が `pass`。一方が pass で、もう一方が version のない不完全 twin なら、後者の `skip` は許可されます。 | Antigravity / Traceary package を意図的に導入していない場合だけ `--skip antigravity='理由'`。 |
| Grok Build | `./scripts/install-grok-plugin.sh`。 | Grok Build を再起動するか、新しい session を開始します。 | `traceary doctor --client grok --json` の `grok-plugin` が `pass`。 | Grok Build / Traceary package を意図的に導入していない場合だけ `--skip grok='理由'`。 |
| Kimi Code | `./scripts/install-kimi-plugin.sh`。installer は新しい generation を stage し、managed `traceary` symlink を atomic に切り替え、install record を保持します。 | `/plugins reload` を実行するか、**新しい Kimi session を開始**します。 | `traceary doctor --client kimi --json` の `kimi-plugin-version` と native `kimi-plugin` が健全。 | Kimi Code / Traceary package を意図的に導入していない場合だけ `--skip kimi='理由'`。 |

doctor が正確な非対話コマンドを `FixCommand` に出す場合は、そちらを優先してください。ホスト CLI のフラグを推測で追加してはいけません。

## 古いプロセス

Homebrew / `go install` の更新は PATH 上のバイナリを置き換えますが、**すでに動いているプロセスは古い実行ファイルのまま**です。2026-08-18 の dogfood では、置き換わった Cellar バイナリ（0.32–0.34）上の `traceary mcp-server` が長時間残っていました。MCP は v0.35.0 で引退しており、これらのプロセスは store-lease プロトコルより前のものです。stale なホスト session が compact 中にそれらへリクエストすると、排他制御なしで書き込めます。

`traceary doctor` はこれを `stale-processes` チェックとして報告します（store-independent、ps-level）。pid・version・age と reap 案内付きの WARN で、該当がなければ黙って PASS します。plugin-cache の WARN は **live process を対象にしません**。

バイナリを更新するたびに:

1. `traceary doctor --json` を実行し、`stale-processes` を確認する。
2. 各 pid を起動したホスト session を終了し、store lease なしの書き込みを止める。
3. `ps -p <pid>` で確認し、未使用なら `kill <pid>`。
4. 残っている `mcp-server` エントリを host config から削除する。[MCP の引退](../mcp/README.ja.md) を参照。

plugin-version が `pass` でも、古いバイナリのプロセスが残っていない証明にはなりません。

## Gemini managed hook generation の更新

`./scripts/install-gemini-extension.sh` は `~/.gemini/extensions/traceary/` 内の extension パッケージを入れ直します（uninstall のあと `install --consent`、実行中 CLI の tag に pin）。`~/.gemini/settings.json` 内にすでに存在する Traceary 管理の hook エントリは**書き換えません**。それらのエントリは古い hook generation によって書かれたものであり、timeout が古い値のまま残ることがあります（例: 現行の 10000 ms に対して 5000 ms）。Doctor はこれを `gemini-config=warn` として `installed=…ms desired=…ms` のドリフトメッセージと共に報告します。

ローカル install に `gemini extensions update traceary` を使わないでください。対話プロンプトで止まります。スクリプトのあと、次を実行してください。

```sh
# プレビュー — Traceary 管理エントリへの変更内容だけを表示します。
traceary doctor --fix --dry-run --client gemini --project-dir <dir>

# 適用 — Traceary 管理エントリだけを書き換え、Traceary 以外の hook は保持します。
traceary doctor --fix --client gemini --project-dir <dir>
```

dry-run の出力が Traceary 管理の hook エントリだけを変更する場合にのみ適用してください。
適用後は `traceary doctor --client gemini --json` を再実行し、`gemini-plugin-version` と `gemini-config` の両方が `pass` になっていることを確認してください。

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
