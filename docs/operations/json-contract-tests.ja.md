# JSON contract tests

[English](./json-contract-tests.md)

Traceary は、公開 CLI の JSON / newline-delimited JSON (NDJSON) 出力が意図せず変わらないように golden test を使います。golden fixture は、レビュー済みの byte-for-byte な command output contract です。

新しい CLI `--json` flag を追加するときは、同じ変更で `presentation/cli/testdata/<command>/<case>.golden.json` 配下に対応する golden fixture も追加してください。既存の golden test が CI ゲートとして機能します — fixture が無い command output は、ad-hoc な string assertion で済ませず、merge 前に fixture を追加してください。

`traceary top --snapshot --json` は `traceary session tree --json` とは意図的に分離された top 専用 contract を持ち、`latest_event_*` field は top snapshot contract 専用です。

## golden test の実行

開発中は対象の contract test だけを実行できます。

```sh
go test ./presentation/cli -run TestEventShow_JSON_Golden
```

contract 変更がまとまったら CLI test 全体も実行します。

```sh
go test ./presentation/cli
```

helper は実際の出力と fixture を byte-for-byte の string diff で比較します。time / ID / ordering / whitespace の transformer は適用しないため、assertion に渡す前の test data を決定的にしてください。

## fixture の更新

意図した contract 変更では、標準の Go test flag で fixture を再生成します。

```sh
go test ./presentation/cli -run TestEventShow_JSON_Golden -update
```

その後、`-update` なしで再実行し、check-in される fixture が clean であることを確認します。

```sh
go test ./presentation/cli -run TestEventShow_JSON_Golden
```

commit 前に fixture diff を必ずレビューしてください。生成された byte 列は downstream script が依存しうる API contract です。

## `-update` を使ってはいけない場合

失敗した test を通すためだけに `-update` を使わないでください。golden diff は、field rename、field removal、timestamp format の変更、NDJSON stream の順序変更、whitespace shape の変更など、意図しない breaking change を示している可能性があります。

`-update` は、その出力変更が意図したものであり、新しい public contract にする、と判断した後だけ使います。

## contract review process

1. diff の対象 command、flag、fixture を特定する。
2. 出力変更が互換かどうかを判断する。breaking / user-visible な変更なら migration note や release note を追加する。
3. その判断後にだけ `-update` で fixture を更新する。
4. `-update` なしで focused golden test を再実行する。
5. commit 前に通常の validation suite を再実行する。

```sh
go build ./...
go test ./...
go tool golangci-lint run
```

golden fixture を更新する PR では、contract が変わった理由と downstream impact の有無を説明してください。
