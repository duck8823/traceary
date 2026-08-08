# 検索プロジェクションの再構築

[English](search-projection-rebuild.md)

検索プロジェクションは派生データです。正本のイベントとコマンド監査からいつでも再構築でき、プロジェクションのライフサイクル操作が正本を変更することはありません。v0.34 以降、世代が complete のときは `traceary search` がこのプロジェクションを読みます。再構築後に記録されたイベントは正本テーブルから統合するため、再構築の合間に結果が古くなることはありません。

世代を一度も作っていない store でも、最初の cutover にオペレータのコマンドは不要です。store を開くたびに generation 作業を上限付きで 1 単位進めます（initialize 時の event-search backfill と同じ形）。idle かつ source event があるときだけ start し、それ以外は一致する rebuild を resume します。世代が `complete` になるまで legacy の migration-032 索引が authoritative のままです。旧世代の行を回収する前に、構築中の世代に対する session tier の実クエリが成功する必要があります。`status` が報告する前後の物理バイトは **bounded_search_projection** ファミリのみです。legacy の `event_search_*` ファミリの数字ではありません。

オペレータは同じ機構を明示的に動かせます。`traceary store search-projection start`で世代を開始します。`resume`は上限付きバッチを1回実行します。複数のバッチを個別にコミットしながら実行する例を次に示します。

プロジェクションschemaより前のstoreをupgradeした場合、最初の`resume`バッチ群はpayloadをdecodeする前に、過去のevent identityをinventoryします。このphaseは`status`に明示され、安定したevent ID cursorを使用し、行数、保存バイト数、論理書き込みバイト数、wall time、lock timeの上限に従います。processを再起動すると最後にatomic commitされたcursorから再開します。過去行への並行の**update / delete**は、不完全なinventoryを受け入れずgenerationを無効化します。ライブの**insert**は無効化しません。events の insert trigger が新しい identity を `search_projection_source_sequence` へ無条件登録するため、inventory に追加作業はなく、store を開くたびに書く hook でも `complete` に到達できます。旧migration 38ですでに投入済みのstoreと新規の空storeは、正本tableをscanせずこのphaseを省略します。

オペレータが非デフォルトの budget で世代を開始したまま中断した場合、store open 時の自動 catch-up はその budget を乗っ取らず skip します。skip は理由付きで warning レベルに記録されます。進捗を再開するには、一致する budget で resume するか abort してください。

```sh
traceary store search-projection resume --until-complete --max-batches 4000 --total-wall-time 8h
```

各バッチには、行数、保存バイト数、デコード後バイト数、論理書き込みバイト数、ロック時間、バッチ実行時間の上限が引き続き適用されます。キャンセル時は最後にコミットしたチェックポイントが残るため、同じコマンドで再開できます。

未完了の世代を破棄して異なる設定で再開する場合は、`traceary store search-projection abort`を使います。この操作は冪等であり、完了済みのactive世代を破棄しません。世代の状態、チェックポイント、high-water、容量証跡は`status`で確認します。完了だけではcutover可能とは判断せず、parity証跡も必要です。
