# MCP ツールスキーマ予算

[English](./mcp-tool-schema-budget.md)

Traceary の既定 MCP surface は一つです。`traceary mcp-server` は 9 個すべての tool を登録します。host ごとの tool profile、registry の分割、host による一部 tool の条件付き登録は行いません。

## 守る契約

`TestServer_ToolRegistrySnapshot` は in-memory MCP transport 経由で実行時の `tools/list` を呼びます。レビュー対象の fixture には、各 tool の name、description、annotation、input schema、自動推論された output schema が入ります。

`TestServer_ToolAdvertisementBudget` は同じ実行時応答の compact JSON を測定します。手編集で定める aggregate hard limit は二つです。

| 測定値 | hard limit | warning threshold |
| --- | ---: | ---: |
| 完全な `tools/list` result | 45,056 B | 90%（40,550 B） |
| input schema の合計 | 18,432 B | 90%（16,589 B） |

テストには tool ごとのレビュー済み hard cap もあります。`presentation/mcpserver/testdata/tool_schema_budget.golden.json` は増加元を示します。90% 以上は warning としてログに出し、hard limit 超過は CI を失敗させます。limit は生成結果ではなく policy です。変更時は理由と report をレビューしてください。

focused report は次で実行します。

```sh
go test ./presentation/mcpserver -run TestServer_ToolAdvertisementBudget -v
```

意図した contract 変更では両 fixture を再生成し、diff をレビューします。

```sh
go test ./presentation/mcpserver -run TestServer_ToolRegistrySnapshot -update
go test ./presentation/mcpserver -run TestServer_ToolAdvertisementBudget -update-tool-schema-budget
```

## host の command 登録

Claude、Codex、Gemini、Antigravity、Grok、Kimi の package はすべて同じ `traceary mcp-server` を登録します。`go run ./cmd/repo-tooling integrations verify` は、別 executable、引数欠落、profile 引数の追加を拒否します。Gemini の working directory のような host 固有 envelope field は別の tool surface を選びません。

## 本文を読まない loading 観測

この repository は、server が完全な実行時 advertisement を出すことと package の command 登録を、event や transcript の本文を読まずに検証できます。一方、third-party host が install 後のいつ schema を読み込むかは測定できません。次の状態は意図的に区別します。

| 状態 | 意味 | 現在の証拠 |
| --- | --- | --- |
| eager | setup 時に host が完全な registry を読む観測がある | 本文を読まない host 観測は未記録 |
| lazy | 利用時まで host が registry 読み込みを遅らせる観測がある | 本文を読まない host 観測は未記録 |
| unknown | package は command を登録するが、読み込み時点は未観測 | Claude、Codex、Gemini、Antigravity、Grok、Kimi |

`unknown` は lazy loading の根拠ではありません。再現可能で本文を読まない host 観測を記録するまで、host を eager / lazy と表記しません。
