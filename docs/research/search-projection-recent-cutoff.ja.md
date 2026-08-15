# search-projection の recent-cutoff 行数上限 (#1807)

[English](./search-projection-recent-cutoff.md)

source-phase prefilter の scratch サイズ測定です。live の約 36 GiB ストアではありません。

## 決定

walk は **新しい行から数えた件数**（`20_000`）で打ち切り、2 秒の壁時計タイムアウトは使いません。フラグは足しません。より長い deadline での再試行もしません。

2 秒タイムアウトは小さいストア（cutoff 不要）では成功し、大きいストア（cutoff が必要）では失敗して空の `recent_cutoff_norm` と age-only の admit-then-evict になっていました。

## 手順

1. ceiling ≤ 0 → retain-nothing sentinel（変更なし）。
2. 最新 N 行で window。交差すればその `created_at_norm`。
3. 交差なしで sample `< N` → 空 cutoff、空 reason（コーパスが収まる）。
4. 交差なしで sample `== N` → cutoff は sample 内の最古 timestamp。
5. cancel / query error → 空 cutoff、`recent cutoff: …`（既存の degrade）。

eviction が exact ceiling を強制します。sample-tail cutoff は本来入る古い行を除外し得ますが、それは build コストを減らすだけです。

## テスト

- `TestSearchProjectionRecentCutoffUsesSampleTailWhenRowCapMissesCrossing`
- `TestSearchProjectionCutoffFailureDegradesNotBreaks`（cancel は degrade のまま）
- `TestSearchProjectionCutoffUsesBlobByteLength`（LIMIT bind）
