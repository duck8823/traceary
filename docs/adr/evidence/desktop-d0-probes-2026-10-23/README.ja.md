# 再実行できるDesktop D0検証（2026-10-23）

[English](README.md)

[#2424](https://github.com/duck8823/traceary/issues/2424)配下の[#2443](https://github.com/duck8823/traceary/issues/2443)に対応する追加証跡を、Python標準ライブラリだけの隔離probeとして保存します。
製品コード、採用済みpublic protocol、D0受入の証明ではありません。
Museは保留し、呼び出しません。

## 再実行

repository rootでPython3.10以降を使います。

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s docs/adr/evidence/desktop-d0-probes-2026-10-23/resize_ack -v
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s docs/adr/evidence/desktop-d0-probes-2026-10-23/unix_ipc -v
```

resizeの31テストは合成fixtureです。
Unix socketの29テストはmacOSが必要で、ローカルAF_UNIX socketを作成し、libc getpeereidでpeer UIDを読みます。
clientの1つは同一UIDの別Python processです。
`/private/tmp`配下に所有する一時directoryだけを作成し、socketを閉じ、threadと子processを待ち、directoryの削除をassertします。
別OSでskipを合格として報告してはいけません。
Desktop bundle、provider login、資格情報の読み取り、外部network、global設定、権限変更は扱いません。
ローカルsocketとprocess作成を許可する実行環境は必要です。

## resizeレシートのモデル

ledgerはgeneration/window/source cohortを固定し、最大200件の要求ごとにID、目標寸法、呼出側が指定する単調時計deadlineを保持します。
即時getterは診断専用です。
完了captureにはnative側のcapture IDと、一貫したissue/application/capture/runner-receipt時刻が必要です。
capture IDまたは時刻pairを複数要求で再利用すると両方を曖昧なconflictにし、遅延・矛盾した証跡で成功へ復帰させません。
これは相関ロジックであり、実際のUI backendがその値を生成した証明ではありません。

31テストには元の150ms cadenceで200要求を交互に処理するfixtureを含みます。
その500ms deadlineは合成テストの値で、native性能の採用閾値ではありません。
主sessionはcapture再利用による誤った成功判定を修正前に再現し、Google系の独立静的reviewが修正を確認して残る指摘なしとしました。
reviewerはコマンドを実行していません。
native UI完了の出自、物理表示、native200-resize gateは未検証です。

## Unix socketの実行可能性probe

macOSで0700 directory/0600 socketのmode bit、同一UIDの別process間通信、kernel peer UID一致、既存pathの拒否と置換inodeを削除しないcleanup、分割・途中終了・過大・不正frame、合計250ms deadline、観測専用fixture handlerを検証します。
製品操作を実装せず、固定run/generation不一致と制御要求の拒否を試験します。
64KiB frame上限と250ms budgetはProposed ADRの値で、現在のCLI保証ではありません。

異なるUIDの実client拒否は実施しておらず、期待UIDを意図的に不一致にする分岐だけを試験しています。
mode bitの検証はACL監査ではありません。
TOCTOUを完全に防ぐ実装、敵対的な同一UID processの隔離、実行fileの同一性、完全な送信元認可、productionの安全性を証明しません。
実験sourceを常駐daemonやpublic CLI intakeとして導入してはいけません。
queue上限、観測projection、durable history、既存hook、store lease、PTY/crash/reconnect、native UI動作は実装・認定していません。

## 証跡と採用の限界

[最小化した結果metadata](../desktop-d0-probes-2026-10-23.json)にsourceと試験scopeを記録します。
英日ADRはProposedのままで、Electron/Wailsとも未採用です。
native IME、accessibility、全負荷入力、制御したforeground比較、sleep/wake/crash、複数display、五ホストの認定、ユーザーの採用判断は別gateです。
過去のnative不合格を合格へ書き換えていません。
このprobeで元のゴールを置き換えません。
