# イベントprojectionと切り詰め契約

[English](./event-projection-contract.md)

**状態:** v0.30.0の採用設計  
**Issue:** #1439  
**親epic:** #1420

## 要求要約

現在のTracearyは、CLIまたはMCPが本文を切り詰める前に、完全な
`model.Event`を復元します。CLIのJSON出力も、`--fields`が本文以外だけを
指定していても完全なイベント形状を直列化します。そのためmetadataの
調査だけを意図した要求でも、巨大なprompt、transcript、tool、command本文を
読み込み、再出力する可能性があります。

v0.30.0では、既存の全文取得操作の意味を変えずに、本文を読まない契約を
追加します。

- MCPでprojectionを省略した場合は、既存の本文上限付き動作を維持する
- `body_limit=0`と`full_body=true`は、引き続き保存済み本文の全文を返す
- metadata-only読取は本文列をSELECTせず、完全なイベントaggregateを作らない
- `traceary show <event-id>`などの詳細取得は全文取得経路を維持する
- ingest、保存方針、responseでの切り詰めを区別する

retentionと削除は本契約の対象外であり、#1421で扱います。

## 概念モデル

| 概念 | 状態 | 振る舞い | 不変条件 |
|---|---|---|---|
| `EventMetadata` | ID、kind、帰属、source hook、時刻、保存済みのサイズ・切り詰め情報 | 本文を復元せず一覧・context・集計に使う | 本文文字列とbody blocksを保持しない |
| `CommandAuditMetadata` | event ID、exit code、failed状態、将来の構造化terminal classification | metadata rowへ任意のaudit列を供給する | command input/outputとevent本文を保持しない |
| `StoredEvent` | metadataと保存済み本文/body blocks | 本文が必要なconsumer向けにdomain eventを復元する | 保存済み本文は元payloadより短い場合がある |
| `EventProjection` | `metadata`、`bounded`、`full` | queryとresponse形状を決める | repository/query実行前に決定する |
| `BodyExtent` | 元、保存済み、返却済みのbyte数と任意のrune数 | どの層で内容が失われたか説明する | unknownと0を区別する |
| `TruncationProvenance` | ingest、保存方針、response | 切り詰めが起きた層を示す | 保存前に失われた内容をresponseで復元可能と表示しない |
| `ProjectionSnapshot` | filterと安定したread snapshot | page読取と集計を一貫させる | 同じsnapshot/filterの対象eventはprojectionによって変わらない |

### projection別の動作

| projection | SQLiteの返却列 | applicationの値 | response本文 | 互換性 |
|---|---|---|---|---|
| `metadata` | metadataと保存済みサイズ・切り詰め列のみ | `EventMetadata` | なし | v0.30.0で追加 |
| `bounded` | 保存済み本文とmetadata | 完全event/read row | 正の上限まで返す | MCP既定500 runeを維持 |
| `full` | 保存済み本文とmetadata | 完全event/read row | 保存済み本文の全文 | `body_limit=0`、`full_body=true`、詳細取得を維持 |

`full`は保存済み内容の全文を意味します。ingestまたは将来のretentionで失われた
byteは復元できません。

## 保存する本文情報

metadata-only queryで本文をGoへ読み込まずサイズを返せるよう、SQLite境界で
次の加算的な情報を保存します。

- `body_original_bytes`: ingest時に判明している元payloadサイズ。unknownを許容
- `body_stored_bytes`: 新規・更新行の保存済みbyte数
- `body_ingest_truncated`: ingest時に本文を削ったか
- `body_storage_truncated`: 将来、明示的な保存・retention処理が本文を削った場合に使う予約済み情報
- `body_metadata_version`: ingest時に抽出したtool-aware本文metadataのversion

既存行の`body_stored_bytes`は、TEXT文字数ではなく
`length(CAST(body AS BLOB))`等のbyte数式を使ってSQLite内でbackfillします。
元サイズとingest経路が
証明できない場合はunknownのままにし、0やfalseを作りません。

`body_returned_bytes`と`body_response_truncated`は要求ごとにpresentationで
算出し、永続化しません。

## 責務表

| 責務 | owner | 変更理由 | ownerにしない層 |
|---|---|---|---|
| projectionの語彙と検証 | `application/types` | consumerから見た読取意味の変更 | CLI/MCPが個別のmodeを作らない |
| metadata読取interface | `application/queryservice` | consumer-orientedなread model | domain repositoryを巨大なoptional interfaceにしない |
| 任意のcommand-audit metadata | metadata query serviceとSQLite left join | `exit_code`等のcompact fieldで詳細本文を読まない | CLI extras resolverでN+1の完全event読取を行わない |
| 列選択とrow scan | `infrastructure/sqlite` | DB/schema詳細の変更 | applicationへSQL/table名を漏らさない |
| CLI flagとJSON field | `presentation/cli` | CLI互換性と直列化の変更 | query serviceへCobraを漏らさない |
| MCP input/output | `presentation/mcpserver` | MCP契約の変更 | query serviceへMCP DTOを漏らさない |
| 保存前の切り詰め情報 | ingest/storage境界 | 実際に保存した内容を知る層 | response rendererで推測しない |
| response切り詰め情報 | presentation serializer | 要求ごとの上限変更 | persistenceへ要求固有状態を保存しない |

既存の`domain/model.Event`は本文を持つaggregateのまま維持します。metadata rowを
不完全に初期化したdomain eventとして扱いません。

`command_executed`では、event本文のextentは保存済みevent envelopeを表します。
既存の`command_audits.input_*` / `output_*` extentは別の構造化command input/output列を
表し、それぞれの列に対する正本のまま維持します。両者を加算しません。
metadata consumerはcommand input、command output、event本文をSELECTせずに
`exit_code`/`failed`をjoinできます。

## 正規情報と既存直列化keyの対応

applicationの語彙を正本とし、既存の公開surfaceでは加算的な互換keyを維持します。

| 正規情報 | CLI JSON互換 | MCP互換 | v0.30.0の規則 |
|---|---|---|---|
| 返却本文/message | `message` | `body` | metadata projectionではkey自体を出さない。空文字列を入れるのではなく専用metadata DTOを使う |
| responseで切り詰めたか | `truncated` | `body_truncated` | 既存keyを同じ正規情報の別名として維持する |
| response切り詰め前の元rune数 | `message_length` | `body_length` | 既存互換で必要な場合だけ出すrune数として維持する |
| 元・保存済み・返却済みbyte数 | `body_original_bytes`、`body_stored_bytes`、`body_returned_bytes` | 同じ名前 | 単位を明示した新しい加算key |
| response前のplain-text projection byte数 | 既存`message_bytes` | 現在は相当keyなし | v0.30.0でresponse要約・切り詰め前の`ExtractPlainBody`のbyte数へ統一する。raw保存本文のextentではない |
| ingest・保存時の切り詰め | `body_ingest_truncated`、`body_storage_truncated` | 同じ名前 | 新しい加算的provenance。unknownはnullまたは省略 |

v0.30.0では`message_length`や`body_length`の単位を暗黙に変えません。responseの
上限はrune基準、永続化するextentと新しい`*_bytes` keyはbyte基準です。
将来の破壊的契約で互換aliasを廃止できますが、本releaseでは改名しません。

## 境界とinterface

| 境界 | consumer | 隠す詳細 | error契約 |
|---|---|---|---|
| `EventMetadataQueryService` | CLI/MCP一覧、context、report | SQL projectionとschema | 不正なlimit/filterはtyped validation errorとし、scan失敗にはoperation contextを残す |
| 完全event query/repository | 詳細・本文consumer | 保存済み本文/body blocks | not-foundとstorage失敗を区別する |
| projection resolver | CLI/MCP adapter | legacy flag優先順位 | 明示的に矛盾するoptionは大きなpayloadを返さず失敗する |
| metadata serializer | JSON/MCP consumer | nullable表現 | unknownを既知の0として出さない |

既存repository methodすべてへ`includeBody bool`を追加しません。metadata consumerに
必要な操作だけを持つ小さなinterfaceを追加します。

### MCP互換性

MCP inputへ加算的な`projection` enumを追加します。

- 省略: 従来の`body_limit`/`full_body`解決
- `metadata`: 本文とbody-block fieldを返さない
- `bounded`: 正の`body_limit`。省略時は500
- `full`: 保存済み本文の全文

`projection=metadata`と`full_body=true`または正の`body_limit`はエラーにします。
従来のint inputでは`body_limit`省略と明示0を区別できないため、metadata指定時の
0は無視します。projection未指定時の0は従来どおり全文取得です。

### CLI互換性

- text出力の既定fieldと形式を維持する
- v0.30.0以降、JSONでも`--fields`を直列化へ適用する
- 明示的な`--fields`がないJSONは既存の完全なevent key集合を維持し、明示指定時だけkeyを減らす
- metadata-only preset/flagは文書化されたmetadata field集合へ展開する
- 本文/message fieldを選ばなければmetadata queryを使う
- `exit_code`等のcompact audit metadataを選んだ場合も、本文なしの`command_audits` join/read modelを使い、rowごとの完全detail取得を行わない
- 失敗色・highlightの解決もjoin済み`failed` metadataを使い、`hydrateForColor`にrowごとの完全detail fallbackを残さない
- metadata JSONは専用の本文なしoutput DTOを使い、本文keyの欠如と保存済み空本文を混同しない
- `--wide`と明示的な詳細取得は既存の本文契約を維持する

## 対象response surface

共有するresponse切り詰め語彙は、event list、search、context、
`top/sessions --snapshot`、handoff recent-command projectionへ適用します。
既定rune上限はsurfaceごとに異なっても、provenanceと単位は共通です。
詳細取得は明示的な保存済み本文全文の経路を維持します。v0.30.0で共有provenanceを
出せないsurfaceは既知の互換gapとして列挙し、別の意味を追加しません。

既存MCP handoffの`recent_commands`は`[]string`であり、itemごとのprovenanceを
保持できません。これはv0.29からの既知の互換gapです。v0.30.0では同fieldを
legacy mirrorとして維持し、event identity、response切り詰め、rune数、byte extentを
持つ構造化sibling itemを追加します。新consumerは構造化siblingを使います。
一般event listのprojectionからhandoff形状を暗黙変更しないよう、別sub-issueで扱います。

## 振る舞いテスト

| 振る舞い | Given | When | Then | Level |
|---|---|---|---|---|
| metadataで本文を復元しない | metadataが同じで本文サイズだけ異なる2行 | metadata list | 値とresponse割当量が本文サイズに依存しない | SQLite integration |
| SQLから本文列を除く | metadata projection | query実行 | scannerに本文/body-blockの読取先がない | SQLite integration |
| 対象eventがprojectionに依存しない | 同一snapshot/filter | metadata/full list | event IDと順序が一致 | query-service integration |
| MCP既定は上限付き | projection省略 | `list_events` | 従来どおり500 runeで切り詰める | MCP regression |
| MCP全文互換 | `body_limit=0`または`full_body=true` | list実行 | response切り詰めなしで保存済み本文を返す | MCP regression |
| 矛盾optionを拒否 | `projection=metadata`かつ`full_body=true` | validation | queryせずエラー | MCP behavior |
| JSON fieldsを直列化へ適用 | CLI JSONでmetadata fieldのみ選択 | list実行 | message/body keyが存在しない | CLI behavior |
| audit metadataでも本文を読まない | CLI metadata fieldに`exit_code`を含む、または失敗色を有効化 | list実行 | 1回のmetadata queryでexit code/failedを返し、event本文とcommand input/outputをSELECTしない | CLI/SQLite integration |
| 詳細取得は全文 | 大きな保存済み本文 | `traceary show` | 保存済み本文を全文返す | CLI regression |
| unknownを0にしない | 元サイズ不明の既存行 | metadata出力 | 元サイズ・provenanceがnullまたは省略 | migration behavior |
| self-inspectionをboundedにする | 大本文を持つ10,000 event | metadata-only list/context | 出力とGo側割当量が本文サイズに依存しない | end-to-end regression |

private helperやcall orderではなく、出力とquery形状を検証します。

## TDD計画

| slice | Red | Green | refactor対象 |
|---|---|---|---|
| #1428 metadata query | 本文列をscanしないこととfull読取とのmembership一致を失敗テストで示す | metadata read model、schema情報、migration、専用SELECTを追加 | filter/snapshot生成だけ共有しscannerは分ける |
| #1433 CLI JSON | `--fields`指定でも`message`が出る、`exit_code`と失敗色でdetail本文を読む、経路により`message_bytes`の意味が変わる失敗を固定 | fieldからmetadata query、任意audit metadata join、専用本文なしserializer、統一したresponse前plain-text byte数へroute | list/tail互換経路のprojection resolverを共通化し両方のN+1 extras hydrationを除く |
| #1433 MCP | 互換性と矛盾optionの失敗テスト | 明示projectionとmetadata outputを追加 | presentation DTOでなくapplication型だけ共有 |
| #1433規模回帰 | 10,000行fixtureの出力が本文サイズに比例する状態を再現 | metadata SQLと本文なしserializerへroute | fixtureを決定的かつprivate dataなしに保つ |

## migration・互換性・rollback

- schema変更は加算的にする。既存の完全event queryを残す
- 新しいサイズ・provenance列は新規行で埋め、既存行は保守的にbackfillする
- 本releaseでは本文を削除・書換しない
- v0.29.xへ戻しても加算列を無視して既存eventを読める
- CLI/MCP互換問題があれば、schemaを残したまま新presentation modeだけを無効化できる

## リスクと実装前checkpoint

- **意図しない本文復元:** metadata presentationが旧queryを呼ぶ可能性があるため、
  query列と本文サイズ非依存の双方をテストする
- **booleanの拡散:** usecase/repository全体へ`includeBody bool`を追加せず、typed
  projectionと専用read modelを使う
- **unknown破壊:** 既存行の元サイズを0にしない
- **searchの誤解:** metadata-only searchがSQLiteのWHEREで本文を使うことは許容するが、
  Goへ本文を返さない
- **CLI/MCP drift:** DTOは別でもprojectionの意味と切り詰め語彙はapplicationで一元化し、上の対応表を互換性の正本にする
- **version名の混同:** `body_metadata_version`は内部ingest抽出versionであり公開projection selectorではない。将来のschema契約が必要としない限りserializerへ出さない

本noteのreviewとmerge後に#1428の実装を開始します。
