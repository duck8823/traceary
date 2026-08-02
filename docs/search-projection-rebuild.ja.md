# 検索プロジェクションの再構築

検索プロジェクションは派生データであり、正本ではありません。プロジェクションのライフサイクル操作は、正本のイベントやコマンド監査を変更しません。

`traceary store search-projection start`で世代を開始します。`resume`は上限付きバッチを1回実行します。複数のバッチを個別にコミットしながら実行する例を次に示します。

```sh
traceary store search-projection resume --until-complete --max-batches 4000 --total-wall-time 8h
```

各バッチには、行数、保存バイト数、デコード後バイト数、論理書き込みバイト数、ロック時間、バッチ実行時間の上限が引き続き適用されます。キャンセル時は最後にコミットしたチェックポイントが残るため、同じコマンドで再開できます。

未完了の世代を破棄して異なる設定で再開する場合は、`traceary store search-projection abort`を使います。この操作は冪等であり、完了済みのactive世代を破棄しません。世代の状態、チェックポイント、high-water、容量証跡は`status`で確認します。完了だけではcutover可能とは判断せず、parity証跡も必要です。
