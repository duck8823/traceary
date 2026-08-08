# 検索プロジェクションの再構築

[English](search-projection-rebuild.md)

検索プロジェクションは派生データです。正本のイベントとコマンド監査からいつでも再構築でき、プロジェクションのライフサイクル操作が正本を変更することはありません。v0.34 以降、世代が complete のときは `traceary search` がこのプロジェクションを読みます。再構築後に記録されたイベントは正本テーブルから統合するため、再構築の合間に結果が古くなることはありません。

`traceary store search-projection start`で世代を開始します。`resume`は上限付きバッチを1回実行します。複数のバッチを個別にコミットしながら実行する例を次に示します。

プロジェクションschemaより前のstoreをupgradeした場合、最初の`resume`バッチ群はpayloadをdecodeする前に、過去のevent identityをinventoryします。このphaseは`status`に明示され、安定したevent ID cursorを使用し、行数、保存バイト数、論理書き込みバイト数、wall time、lock timeの上限に従います。processを再起動すると最後にatomic commitされたcursorから再開します。正本が並行変更された場合は、不完全なinventoryを受け入れずgenerationを無効化します。旧migration 38ですでに投入済みのstoreと新規の空storeは、正本tableをscanせずこのphaseを省略します。

```sh
traceary store search-projection resume --until-complete --max-batches 4000 --total-wall-time 8h
```

各バッチには、行数、保存バイト数、デコード後バイト数、論理書き込みバイト数、ロック時間、バッチ実行時間の上限が引き続き適用されます。キャンセル時は最後にコミットしたチェックポイントが残るため、同じコマンドで再開できます。

未完了の世代を破棄して異なる設定で再開する場合は、`traceary store search-projection abort`を使います。この操作は冪等であり、完了済みのactive世代を破棄しません。世代の状態、チェックポイント、high-water、容量証跡は`status`で確認します。完了だけではcutover可能とは判断せず、parity証跡も必要です。
