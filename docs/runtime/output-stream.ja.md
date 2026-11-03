# 有界なローカルPTY出力

[English](./output-stream.md)

DaemonはPTYの生バイトを**メモリ内だけ**に保持し、readerの寿命から分離します。attach clientの終了・クラッシュ・キャンセルは、そのreaderのdetachだけです。所有する子プロセスを再起動・終了しません。Daemonクラッシュ後のPTY復元は保証せず、安全なnative sessionの明示再開は別の契約です。

```sh
traceary runtime attach --runtime-dir /absolute/private/runtime \
  --epoch <epoch> --run <run> --generation <generation> \
  --worktree <worktree-id> --profile <profile-id> --cursor 0
```

5つの不変な選択フィールドは、現在のDaemon epochが実際に所有する子プロセスと一致する必要があります。保存PID、旧epoch、spawn前ticket、異なるgenerationはattach権限になりません。現在の所有runは、終了後やpublication uncertainでも読み取れます。attachはhost起動やcore DB初期化を行いません。

## フレームとカウンター

management専用の一方向streamは、`snapshot`、`data`、`gap`、`end`のJSON行を出力します。`data`のバイトはbase64、offsetとresize sequenceは**正規形の10進文字列**です。JavaScript数値ではありません。offsetはUnicode文字ではなくバイトを数え、chunkはUTF-8やescape sequenceの途中で分割されます。CLIはescapeを描画せず、OSCのclipboard/link操作を実行しません。信頼できない内容をdecodeして描画する場合は、別途terminalの安全方針が必要です。

同じ選択と、最後に処理したdataの`end`またはgapの`to`を指定して明示的に再接続します。snapshotは保持範囲の`base`、capture済みの`head`、capture終了、最後のowner適用resize acknowledgmentを示します。配送receiptやprocess終了通知ではありません。`head`より先のcursorは拒否します。`base`より古いcursorには、正確な`evicted` gapを先に返し、黙って切り詰めません。古いscrollbackの再生中にもevictionが発生します。

`end`はcaptureの終了であり、processの終了とは限りません。子が生存中でもEOF/EIOでcaptureが終了することがあります。実際のowned Wait後、単一PTY readerがkernel内の末尾を最大250 msまたは2 MiBまでdrainします。子孫がslaveを保持する場合やtail予算を使い切った場合は、`delivery_unknown: true`の明示的な`tail_unknown` gapを返します。実際の欠落長が不明なので、既知範囲がゼロ幅でもunknown gapになり得ます。

## 固定上限とslow reader

- 各owned runの保持は最大2 MiB、遅延確保です。native/read chunkは最大32 KiBです。
- 各runのreaderは最大4、management接続全体は最大32です。
- readerごとのpending wire予算はbase64とframe overhead込みで最大256 KiBです。古いscrollbackは共有ringから遅延再生し、queueへ複製しません。新しいlive backlogだけを課金し、空のbacklogはゼロです。
- 予算を超えたslow readerは、配送不明を示す`slow_consumer` gapでdetachします。socket writeにも期限があり、書けない接続は閉じます。peerは配送不明として、自分が最後に処理したcursorから明示再接続する必要があります。producerはreaderを待ちません。
- epoch内のlaunch record最大1,024件から、terminal byteの最悪上限は2 GiBと有界metadataです。これは計算上の上限であり、メモリ性能の認証ではありません。終了runのcompactionは別作業です。

idle stream readにRPC期限を引き継ぎません。admissionは8秒以内、各frame writeは設定済みI/O期限内です。admission後のEOFまたは追加client byteは、その購読だけを閉じます。clientキャンセル/Closeはstreamと接続slotを解放しますが、子へsignalを送りません。自動retry・起動・再開は行いません。

resize evidenceはownerのPTY設定成功後に限り、sequence `0`、24行×80列で開始します。owner適用acknowledgmentだけで進み、完全一致の重複は冪等、矛盾・古いacknowledgmentは拒否します。子の自己resizeや出力テキストはこのevidenceを偽装できません。現在のnative geometryや過去のbyte layoutを保証するものでもありません。公開resize/input/stop操作は別作業です。

## 検証とrollback

合成native PTYテストで正確なbyte count、Unicode/TUI/OSC byte、resize storm、exit tail、slow reader eviction、キャンセル、同一generation再接続を確認します。実AI host、native IME、VoiceOver、UI frameworkの認証には代用しません。credential、wrapper、別課金経路は使用しません。

rollbackでは新規attach admissionを止めreaderをdetachし、子の所有権と安全なlaunch metadataは維持します。揮発性の履歴はDaemon再起動後に復元しません。欠落を配送済みと示してはいけません。
