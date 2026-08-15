# search-projection 再構築のディスクピーク (#1753)

[English](./search-projection-rebuild-peak.md)

2 世代再構築ピークの scratch 測定です。live の約 36 GiB store は対象外です。

## 判断

**ピークを受け入れる。** `--index-family-bytes` は cleanup 後の complete 世代 1 つの定常目標です。再構築中のディスクは上限しません。

採用しなかった案:

- **再構築前に回収** — 再構築のあいだ検索が止まります。2 世代にした理由と逆です。
- **ピークを予算内に予約** — #1620 の下限は 1.43 GiB 目標の約 10 倍で、予約すると recent 窓が空になります。
- **より大きい再構築天井を別に置く** — 操作ノブが増えます。v0.37 では足しません。

操作レバーはこれまでどおり `traceary store search-projection abort` です。`store compact` は使用中ページを回収できません。

## scratch fixture（`TestSearchProjectionRebuildPeakExceedsConfiguredBudget`）

短い event 12 件。gen1 を 64 MiB 予算で完了し、resident family より小さい予算で gen2 を開始しました。

| 時点 | index-family dbstat | store ファイル |
|---|---|---|
| gen1 complete | 258,048 | 761,856 |
| `Start` 直後（旧世代が残存） | 258,048 | 761,856 |
| 再構築ピーク（gen2 中） | **405,504** | 761,856 |
| gen2 complete | 266,240 | 761,856 |

gen2 予算は 225,280。ピーク family はその 1.80 倍です。ファイルサイズは動きません（`VACUUM` なし。WAL / freelist はファイルに残ります）。

## 大規模コーパスの下限（既存記載、#1620）

408,893 イベント、上限 1.43 GiB: family 3.99 GB → 10.32 GB → **>= 14.31 GB** のあと `database or disk is full`。14.31 GB はピークではなく下限で、その複製は 4 世代を抱えていました。

## 約束

予算は再構築ディスクを上限しません。空き容量は `--index-family-bytes` ではなく、2 世代と FTS5 の inverse posting を見込んでください。
