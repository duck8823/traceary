# イベント応答予算

[English](./response-budget.md)

`list_events`、`search`、`get_context` は共通の応答予算を使用します。

- 既定のページは20イベントで、要求できる最大件数は100です。
- bounded本文は既定500 rune、最大16,384 runeです。
- `full_body=true` または `body_limit=0` でも、`body` と `body_blocks` のエンコード後合計は64 KiB以下です。
- 応答には`coverage`、`partial`、`reasons`が含まれます。ページ件数または応答全体の本文予算によって未返却eventが残る場合は、不透明な`continuation`も含まれます。

continuation は暗号化・改ざん検知・バージョン付けされ、ツールと正規化済みの要求形状に結び付けられます。`offset` と併用できません。3ツールとも同じ解決済み上限時刻を保持し、最後の `(created_at, event_id)` キーから再開するため、同時に追加された新しいイベントや同一時刻のイベントが後続ページをずらしたり重複させたりしません。トークンは発行した MCP server process の実行中だけ有効です。クライアントはトークンを不透明な値として扱い、server 再起動後は先頭から取得し直してください。

## 設計と振る舞いの不変条件

| 概念 | 責務の所有者 | 不変条件 |
|---|---|---|
| 応答予算 | application の値オブジェクト | 件数、イベント単位の本文、応答全体の上限をquery実行前に検証します。 |
| 候補ページ | SQLite query adapter | 候補を `(created_at_norm, id)` 順に並べ、continuation付きページでは複合キーによるkeyset seekを使用します。 |
| bounded hydration | bounded read usecaseとSQLite adapter | ページで選択した同一metadata候補をhydrateし、候補選択とhydrateの間にfilter queryを再実行しません。 |
| continuation | MCP adapter | tokenを一度だけdecodeして認証し、tool、正規化済みrequest、snapshot上限、最後に返したeventへ結び付けます。 |
| 本文予算内の連続prefix | MCP adapter | 並び順の先頭から連続したeventだけを返します。次のeventが本文を保持できない場合は返さず、continuationは最後に返したeventをanchorにします。 |

同一時刻のページング、同時insert、anchor付きquery plan、bounded候補の再利用、server再起動後のcontinuation error、次ページでの本文予算超過event回収、省略記号を含むtruncate判定、公開MCP tool schema fixtureを振る舞いテストで保護します。
