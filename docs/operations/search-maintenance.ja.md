# 検索メンテナンス

[English](search-maintenance.md)

Traceary v0.34 は、通常検索の authority を projection table の存在とは
独立して記録します。既存 store と upgrade 済み store の初期状態は
`legacy/active` です。migration と通常起動は legacy 検索データを削除、
DROP、VACUUM しません。

退役は operator が明示的に実行します。

1. 実際の対象 store で `traceary store search-maintenance adopt-target` を
   実行します。copy 済み cursor key が更新され、projection は stale に
   なります。その後 bounded search projection と同一 HEAD の parity-v2 を
   完了します。copy store の artifact は互換性証跡であり、退役の認可には
   使用できません。
2. `traceary store search-maintenance start-retire --evidence ARTIFACT
   --expected-revision COMMIT` を実行します。Traceary は projection state、
   source high-water、集計値、store 固有鍵を fresh snapshot で再取得し、
   keyed target binding を constant-time で比較します。
3. `resume-retire --rows 128` を繰り返します。1 transaction が削除する
   legacy plaintext document は指定行数以内です。進捗と前後の logical /
   physical bytes を永続化するため、中断後も再開できます。
4. `status` を確認します。通常の CLI / MCP 検索は永続化された tiered
   authority を使用し、projection が incomplete / stale なら fail closed します。

rollback は codec decode を含む canonical history から再構築します。

1. `start-restore` を実行します。
2. status が `legacy/active` になるまで `resume-restore --rows 128` を
   繰り返します。

最後の canonical batch と legacy writer trigger の復元が同じ transaction で
完了するまで authority は legacy に戻りません。decode / write failure 時は
`tiered/restoring` に留まり、部分的な legacy projection は authority になりません。

SQLite は free page を保持するため、物理ファイルサイズが直ちに減らない場合が
あります。status は projection の logical bytes と DB の physical bytes を
記録します。この workflow は暗黙の `VACUUM` を実行しません。

production cutover 前に synthetic / copied-store artifact の legacy / tiered
latency を比較してください。membership parity が合格しても recent search に
実質的な退行があれば rollback します。
