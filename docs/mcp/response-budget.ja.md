# イベント応答予算

[English](./response-budget.md)

`list_events`、`search`、`get_context` は共通の応答予算を使用します。

- 既定のページは20イベントで、要求できる最大件数は100です。
- bounded本文は既定500 rune、最大16,384 runeです。
- `full_body=true` または `body_limit=0` でも、`body` と `body_blocks` のエンコード後合計は64 KiB以下です。
- 追加ページまたは本文予算による削減がある応答には、`coverage`、`partial`、`reasons`、不透明な `continuation` が含まれます。

continuation はバージョン付きで、ツールと要求形状に結び付けられます。`offset` と併用できません。list と search は解決済みの上限時刻を保持するため、`to` を省略してもページング中に範囲が変動しません。クライアントはトークンを不透明な値として扱ってください。
