# 大容量 live store における bounded `doctor`

[English](large-store-doctor.md)

live SQLite store が遅い、または lock contention が疑われる場合、最初に安全に
実行するコマンドは `traceary doctor --json` です。regular な store file が 2 GiB
以上の場合は、bounded な metadata-only report を返します。

```sh
traceary doctor --json --warnings-ok
```

report には `mode: "metadata_only_large_store"` と
`large-store-diagnostics` の警告が含まれます。これは出力欠落ではなく完了した
結果です。filesystem metadata だけを使い、SQLite の open、migration、event の
list、event body や command payload の読み取り、credential や
identifier sample の出力を行いません。

bounded report には host package identity（`*-plugin-version` ファミリーと
native な Grok/Kimi plugin 有効化チェック）が引き続き含まれます。これらは
host manifest・host plugin cache・host CLI probe（`grok inspect --json`、
`kimi.plugin.json` など）のみを読み、Traceary store は読みません。そのため
live store が大容量であることは、古い host package を報告しない理由には
なりません。それ以外の client 単位チェック（config 解決、event coverage、
hook route）は引き続き bounded report から除外されます。

## 結果の解釈

- **容量:** 警告は live store が大きいことを示します。1 GiB の上限ではなく、data
  を削除することもありません。回収は `traceary store compact` です。先に fold
  してください。`--force` は機械要約を書き、判断理由が失われることを明示します。
- **lock contention:** metadata-only の結果は、DB が unlocked であるとは主張しません。
  content-level の調査前に競合 writer を停止または分離してください。busy incident
  中に `doctor --fix` を繰り返し実行しないでください。
- **深い調査:** contention 解消後に、review 済みの bounded copy または狭く限定した
  read command を使用してください。raw event body、prompt/response payload、credential、
  identifier list を incident report にコピーしないでください。

この mode の既定コマンドは data を変更しません。`--fix` は small store では従来どおり
ですが、metadata-only の結果から retention 操作には進みません。

## 2 つの doctor mode と check のスコープ

| Mode | いつ | Store アクセス | `hook-spool` の単位 |
|---|---|---|---|
| **Full** | store file が無い、または 2 GiB 未満 | store-scoped check のために SQLite を開く | 選択中 `--client` 向けの **decoded records**。比較用に `filesystem pending files (store-independent)=N` も同じ行に出す |
| **Metadata-only** | regular store file が 2 GiB 以上（`mode: "metadata_only_large_store"`） | filesystem metadata と O(1) の `mode=ro` page/projection 読み | **files**（`metadata-only, store-independent`）。`pending=` はディレクトリエントリ数であり decoded record 数ではない |

store-independent な check（hook spool、hook-state residue、SessionEnd cancellation marker、plugin cache、`path` / `config`）は出力行に `store-independent` と書きます。SQLite store ではなく host file を見ます。store-scoped な check（capacity、memory activation、projection）は `DB_PATH` の store を見ます。

doctor が `--db-path` または `TRACEARY_DB_PATH` 付きで走ったとき、store を対象とする hint（`traceary doctor`、`traceary store`、`traceary memory`）には `--db-path` が付きます。そのまま実行しても検査した store に届きます。host-only なコマンド（`claude plugins update`、`which -a traceary`）は変えません。既定の home store では `--db-path` を足しません。
