# 取得時点のworkspace固定

[English](./workspace-capture.md)

hook deliveryは最初のhandler実行前に解決済みworkspaceを保存します。
`TRACEARY_WORKSPACE`、repository設定、hook workspace stateが変わっても、同じdeliveryの再送先workspaceは変えません。
これはCLIの取得・再送の保証であり、daemonや現在のagent状態APIではありません。

## Routing契約

private spool envelopeにoptionalな`resolved_workspace`を取得metadataとして保持します。
host payloadとは別であり、同名のhost fieldを信頼済みの固定値として採用しません。
既存のlocal reader/context境界を通して初回処理と再送に同じ値を渡し、native adapterの正規化やprompt/transcriptのconsolidation経路でも維持します。
native delivery identity、raw cwdの証拠、local receiptの重複抑止とは別の情報です。

- 空でない固定値は、後から変更された環境・Git検出・hook stateより優先します。
- 取得時点の空文字列は不明のままとし、後の環境から埋めません。
- optional fieldがない旧recordは従来の解決動作を維持します。取得時点のworkspaceを推測して復元しません。
- Session開始時のfallback条件とAntigravity pending tool cwdの優先順位は維持します。
- usage専用hookはbody/path-freeのままです。usageは保存済みSession identityとcanonical workspaceで関連付け、新しいworkspace引数は追加しません。
- Memory extraction jobは親hookが解決したworkspaceをenqueue時点で保持します。

取得時点の解決に失敗した場合、spoolに`workspace_capture_failed`を保持し、そのmarkerへ元のerrorやpayloadは保存しません。
初回handlerの既存の解決前診断は実行できますが、workspace解決は固定errorで失敗します。
再送は後の環境へ再関連付けせず、既存の回数制限付きretry/dead-letter処理を使います。
したがって一時的な取得失敗でも、後から別workspaceが利用可能になっただけでは自動回復しません。
既存のhook-spool診断を確認してください。この変更では自動再関連付け・破棄操作は追加しません。

## 互換性と制限

SQLite migration、Session lifecycle変更、新しいruntime entity、agent wrapper、PATH shim、public commandは追加しません。
既存のSession ID・database routing保証は拡張せず、未発火native callbackも復元しません。
旧recordのbackfillと保存済み履歴の書換えは行いません。

spool形式は追加的な変更ですが、古いbinaryは新fieldを無視するため固定保証を実行できません。
downgrade前にpending/dead-letter recordを評価してください。古いbinaryでも固定値が守られると考えて新recordを再送しないでください。
未解決recordは削除・暗黙再関連付けせず、明示的な診断のために保持してください。

## 検証

環境・repository変更、取得時点の不明と旧形式のfield欠落、host payloadの同名field、native正規化、初回と再送の整合、取得失敗時の保持、別delivery contextからの分離を回帰fixtureで検証します。
既存receipt・cancellation・usage・hookのテストも適用します。
fixtureは隔離hook-state directoryを使い、native host実行は別のintegration checkpointです。
