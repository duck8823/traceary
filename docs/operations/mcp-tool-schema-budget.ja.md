# MCP ツールスキーマ予算

[English](./mcp-tool-schema-budget.md)

Traceary の既定 MCP surface は一つです。`traceary mcp-server` は 9 個すべての tool を登録します。host ごとの tool profile、registry の分割、host による一部 tool の条件付き登録は行いません。

## 守る契約

`TestServer_ToolRegistrySnapshot` は in-memory MCP transport 経由で実行時の `tools/list` を呼びます。レビュー対象の fixture には、各 tool の name、description、annotation、input schema、自動推論された output schema が入ります。

`TestServer_ToolAdvertisementBudget` は同じ実行時応答の compact JSON を測定します。手編集で定める aggregate warning band と hard limit は二つです。

| 測定値 | pass 範囲 | warning band | hard limit |
| --- | ---: | ---: | ---: |
| 完全な `tools/list` result | 48 KiB 未満 | 48–52 KiB（49,152–53,248 B） | 52 KiB（53,248 B） |
| input schema の合計 | 16 KiB 未満 | 16–18 KiB（16,384–18,432 B） | 18 KiB（18,432 B） |

テストには tool ごとのレビュー済み hard cap もあります。`presentation/mcpserver/testdata/tool_schema_budget.golden.json` は増加元を示します。tool ごとの warning threshold は、手編集した hard cap の 90% のままです。warning band 内の値はログに出し、hard limit 超過は CI を失敗させます。limit は生成結果ではなく policy です。変更時は理由と report をレビューしてください。

完全な応答に対する band は repository の policy であり、host の上限を示すものではありません。初期値 41,028 B とレビュー済みの pagination schema 増分のどちらも warning band 未満になり、hard limit 直前の 4 KiB が明示的な対応期間になるように定めています。

focused report は次で実行します。

```sh
go test ./presentation/mcpserver -run TestServer_ToolAdvertisementBudget -v
```

意図した contract 変更では、最終的に統合した同一 commit から両 fixture を一緒に再生成し、diff をレビューします。

```sh
go test ./presentation/mcpserver \
  -run 'TestServer_Tool(RegistrySnapshot|AdvertisementBudget)$' \
  -update -update-tool-schema-budget
```

古い branch で片方だけを再生成し、並行する schema 変更へ fixture をコピーしてはいけません。対象の schema 変更をすべて rebase または統合してから combined command を一度実行し、同じ tree から生成された二つの diff を確認します。update flag が記録するのは観測した schema と byte 数だけであり、手編集の warning band や hard limit は変更しません。

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
