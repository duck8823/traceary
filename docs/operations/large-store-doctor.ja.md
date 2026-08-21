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

この mode の既定コマンドは data を変更しません。`--fix` は report を
metadata-only のまま保ちつつ hook spool に作用します。transient dead-letter を
再キューし、spool replay で未処理 record を drain し、14 日より古い
dead-letter と orphan `*.tmp` を prune します。spool replay は event 書き込みの
ためだけに SQLite を開くことがあります。dbstat、event body、store 容量は
読みません。`--fix --dry-run` は何も開きません。

`--fix` の apply フェーズ全体（dead-letter の再キュー、SQLite replay を含む
未処理 record の drain、後続の自動 fix）は、1 つの共有された 45 秒の wall
clock の下で実行されます。drain は各 record を claim する前に wall を確認します。
実行中の replay は最後まで完了するため wall には 1 record 分の slack がありますが、
wall 到達後に新しい SQLite replay は開始されません。未処理 record が残ったまま
wall に達した場合、fix の action は残りを `remaining=N` として報告し、後続の
自動 fix 可能な check は `skip: doctor --fix wall exhausted` でスキップされます。
wall 到達時点で apply フェーズは store を手放すため、後続の検査は O(1) page
metadata を「unavailable」ではなく正常に読めます。

## 2 つの doctor mode と check のスコープ

| Mode | いつ | Store アクセス | `hook-spool` の単位 |
|---|---|---|---|
| **Full** | store file が無い、または 2 GiB 未満 | store-scoped check のために SQLite を開く | 選択中 `--client` 向けの **decoded records**。比較用に `filesystem pending files (store-independent)=N` も同じ行に出す |
| **Metadata-only** | regular store file が 2 GiB 以上（`mode: "metadata_only_large_store"`） | filesystem metadata と O(1) の `mode=ro` page/projection 読み。`--fix` は hook spool replay のためだけに追加で SQLite を開くことがある | **files**（`metadata-only, store-independent`）。`pending=` はディレクトリエントリ数であり decoded record 数ではない |

store-independent な check（hook spool、hook-state residue、plugin cache、`path` / `config`）は出力行に `store-independent` と書きます。SQLite store ではなく host file を見ます。SessionEnd cancellation marker は境界付きの `mode=ro` sessions 主キー lookup で store と突き合わせるため（event body や dbstat は読まない）、もはや store-independent 専用ではありません。store-scoped な check（capacity、memory activation、projection）は `DB_PATH` の store を見ます。

doctor が `--db-path` または `TRACEARY_DB_PATH` 付きで走ったとき、store を対象とする hint（`traceary doctor`、`traceary store`、`traceary memory`）には `--db-path` が付きます。そのまま実行しても検査した store に届きます。host-only なコマンド（`claude plugin update`、`which -a traceary`）は変えません。既定の home store では `--db-path` を足しません。
