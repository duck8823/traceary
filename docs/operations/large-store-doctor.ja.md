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
list、event body や command payload の読み取り、hook spool の検査、credential や
identifier sample の出力を行いません。

## 結果の解釈

- **容量:** 警告は live store が大きいことを示します。1 GiB の上限ではなく、data
  を削除することもありません。最初に `traceary store gc --dry-run` を実行し、
  retention を適用する前に archive を作成してください。
- **lock contention:** metadata-only の結果は、DB が unlocked であるとは主張しません。
  content-level の調査前に競合 writer を停止または分離してください。busy incident
  中に `doctor --fix` を繰り返し実行しないでください。
- **深い調査:** contention 解消後に、review 済みの bounded copy または狭く限定した
  read command を使用してください。raw event body、prompt/response payload、credential、
  identifier list を incident report にコピーしないでください。

この mode の既定コマンドは data を変更しません。`--fix` は small store では従来どおり
ですが、metadata-only の結果から retention 操作には進みません。
