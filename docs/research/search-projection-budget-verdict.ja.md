# search-projection の予算判定 fallback (#1835)

[English](./search-projection-budget-verdict.md)

index-family 予算判定の scratch サイズ測定です。live の約 36 GiB ストアではありません。

## 決定

`-1` は不明のままです。split が取れずファミリ**合計**が取れるときは、その合計に対する粗い `0` / `1` を出します。合計も取れないときは `-1` のまま、`physical_evidence.reason` を必須にします。

新しいフラグも JSON フィールドも足しません。

mid-rebuild 118 サンプルがすべて `-1` だったのは、判定が cutover の 3 秒 split にしか書かれなかったからです。同じストアで `physical_bytes` は complete でした。

## テスト

- `TestSearchProjectionStatusBudgetVerdictUsesFamilyTotalWhenPersistedUnknown`
- `TestSearchProjectionStatusUnknownBudgetVerdictNamesReason`
- `TestSearchProjectionCutoverEvidence_CompletionSurvivesUnmeasurableFamily`（1ns split でも complete。after-bytes は合計）
