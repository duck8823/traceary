# JSON / snapshot contract tests

[English](./json-contract-tests.md)

Traceary は、公開 CLI の JSON / newline-delimited JSON (NDJSON) / 構造化テキスト出力、および MCP tool registry が意図せず変わらないように golden test を使います。golden fixture は、レビュー済みの byte-for-byte な surface output contract です。

contract surface は次の 3 種類です。

- **CLI `--json` 出力** — `presentation/cli/testdata/<command>/<case>.golden.json`。新しい CLI `--json` flag を追加するときは、同じ変更で対応する golden fixture も追加してください。代表的なカバー対象は `event list` / `event search` / `event show`、`session list` / `session tree` / `session start` / `session end` / `session active` / `session latest`、`memory list` / `memory search` / `memory show` および `memory inbox` / `memory admin hygiene` / `memory admin graph` 系一式、`sessions --snapshot --json` / `top --snapshot --json`、`bundle import --json`、`timeline --json`、`doctor --json` です。
- **CLI 構造化テキスト出力** — `presentation/cli/testdata/session_handoff/*.golden`、`presentation/cli/testdata/top/*.golden` など。`traceary session handoff` のように `--json` flag を持たないコマンドは、構造化テキストそのものが prompt-injection / resume tooling の parse 対象となる public contract です。これらの golden は field label (`SESSION_ID:` / `WORKING_STATE:` / `RECENT_COMMANDS:` / `RECENT_COMMAND_ITEMS:` / `MEMORIES:`) と並び順を守ります。
- **MCP tool registry snapshot** — `presentation/mcpserver/testdata/tool_registry.golden.json`。登録済みの MCP tool すべての name / description / annotations (`readOnlyHint` / `destructiveHint`) / 自動推論された input schema (property 名、型、説明、`required`) を 1 つの fixture に固定します。`TestServer_ToolRegistrySnapshot` が in-memory MCP transport で `ListTools` を呼び、tool 名でソートしてからスナップショットするため、tool の add / remove / rename だけでなく input schema 内部の field レベルのドリフトも diff として検出できます。

`traceary sessions --snapshot --json`（および恒久的な互換 alias の `traceary top --snapshot --json`）は `traceary session tree --json` とは意図的に分離された dashboard 専用 contract を持ち、`latest_event_*` field は dashboard snapshot contract 専用です。v0.14.0 以降、snapshot は `sessions` / `failures` / `recent_commands` / `candidates` (`{ count, items }`) を持つ envelope オブジェクトでラップされ、dashboard の各ペインと同じデータを返します。従来「session node の配列」を直接読み取っていた消費側は、envelope の `sessions` を読み取るように更新する必要があります。v0.15.0 以降は stale-memory ペインに対応する `stale_memories` (`{ count, items }`) も含まれます。stale row は durable-memory summary field に `reason` を加えた shape です。v0.16.0 以降、stale active session の明示確認は `sessions --allow-stale` または `top --allow-stale` で有効化できます。このとき stale session node には任意 field として `is_stale` / `stale_after_seconds` / `stale_age_seconds` が追加され、既存の session field は維持されます。

v0.19.0 以降、`traceary sessions --snapshot`（および恒久的な互換 alias の `traceary top --snapshot`）の text snapshot は raw な `workspace=` / `agent=` metadata の前に `name="..."` を含みます。この形状は text golden fixture で固定されています。位置ベースの安定性が必要な機械 consumer は、変更のない JSON envelope を使ってください。

v0.20.1 以降、JSON / text snapshot writer は downstream の broken pipe を通常の early-close として扱います。`traceary sessions --snapshot --json | head -c 1` のような command は、誤解を招く Traceary error を出さずに silent に終了します。一方で、query や JSON encoding の失敗は従来どおり loud に失敗します。

v0.21.0 以降、snapshot の `reliability.memory` オブジェクトに additive な `candidate_hygiene` オブジェクト (`stale_count` / `duplicate_count` / `fragment_like_count` / `extracted_hidden_count` / `likely_actionable_count`) が追加されます。この field は additive なので既存の snapshot consumer には影響せず、JSON 専用です (text snapshot renderer は意図的に変更していません)。各カウントは `scan_limit_reached` と同じ memory scan の範囲に束縛され、flag カウントは重複しうるもので、`likely_actionable_count` はその補集合 (どの hygiene flag も立っていない候補) です。

CLI コマンドの公開出力に対応する fixture が無い場合は、ad-hoc な string assertion ではなく、merge 前に fixture を追加してください。同様に、MCP tool を追加 / 削除 / 改名するときは同じ変更で registry snapshot も再生成してください。

## golden test の実行

開発中は対象の contract test だけを実行できます。

```sh
go test ./presentation/cli -run TestEventShow_JSON_Golden
```

MCP registry contract test を単独で実行する場合:

```sh
go test ./presentation/mcpserver -run TestServer_ToolRegistrySnapshot
```

contract 変更がまとまったら CLI / MCP test 全体も実行します。

```sh
go test ./presentation/cli ./presentation/mcpserver
```

helper は実際の出力と fixture を byte-for-byte の string diff で比較します。time / ID / ordering / whitespace の transformer は適用しないため、assertion に渡す前の test data を決定的にしてください。MCP registry test は tool を name でソートし、`encoding/json` の map key 順序ルールに従って input schema を再 marshal するため、実行ごと・プラットフォーム間で安定です。

## fixture の更新

意図した contract 変更では、標準の Go test flag で fixture を再生成します。

```sh
# CLI golden (JSON / NDJSON / structured text)
go test ./presentation/cli -run TestEventShow_JSON_Golden -update
go test ./presentation/cli -run TestSessionHandoff_TextGoldens -update

# MCP tool registry snapshot
go test ./presentation/mcpserver -run TestServer_ToolRegistrySnapshot -update
```

その後、`-update` なしで再実行し、check-in される fixture が clean であることを確認します。

```sh
go test ./presentation/cli -run TestEventShow_JSON_Golden
go test ./presentation/mcpserver -run TestServer_ToolRegistrySnapshot
```

commit 前に fixture diff を必ずレビューしてください。生成された byte 列は downstream script や MCP client が依存しうる API contract です。

## `-update` を使ってはいけない場合

失敗した test を通すためだけに `-update` を使わないでください。golden diff は次のような意図しない breaking change を示している可能性があります。

- CLI JSON field の rename / removal
- timestamp format の変更や NDJSON stream の順序変更
- 構造化テキスト contract (例: handoff) の whitespace / ラベルの変更
- ドキュメントや migration を伴わない MCP tool の add / remove / rename
- MCP input schema の JSON key rename、`required` からの脱落、型変更

`-update` は、その出力変更が意図したものであり、新しい public contract にする、と判断した後だけ使います。

## contract review process

1. diff の対象 surface (CLI コマンド + flag、構造化テキストコマンド、または MCP tool) と fixture を特定する。
2. 出力変更が互換かどうかを判断する。CLI の shape 変更でも MCP tool / schema 変更でも、breaking / user-visible なら migration note や release note を追加する。
3. その判断後にだけ `-update` で fixture を更新する。
4. `-update` なしで focused golden test を再実行する。
5. commit 前に通常の validation suite を再実行する。

```sh
go build ./...
go test ./...
go tool golangci-lint run
```

golden fixture を更新する PR では、contract が変わった理由と downstream impact の有無を説明してください。
