# イベント応答予算

[English](./response-budget.md)

`list_events`、`search`、`get_context` は共通の応答予算を使用します。

- 既定のページは20イベントで、要求できる最大件数は100です。
- bounded本文は既定500 rune、最大16,384 runeです。
- `full_body=true` または `body_limit=0` でも、`body` と `body_blocks` のエンコード後合計は64 KiB以下です。
- 追加ページまたは本文予算による削減がある応答には、`coverage`、`partial`、`reasons`、不透明な `continuation` が含まれます。

continuation は暗号化・改ざん検知・バージョン付けされ、ツールと正規化済みの要求形状に結び付けられます。`offset` と併用できません。3ツールとも同じ解決済み上限時刻を保持し、最後の `(created_at, event_id)` キーから再開するため、同時に追加された新しいイベントや同一時刻のイベントが後続ページをずらしたり重複させたりしません。トークンは発行した MCP server process の実行中だけ有効です。クライアントはトークンを不透明な値として扱い、server 再起動後は先頭から取得し直してください。
